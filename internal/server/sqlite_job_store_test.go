package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteJobStore(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "coxec-sqlite-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := NewSQLiteJobStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite job store: %v", err)
	}
	defer func() {
		if store != nil {
			store.Close()
		}
	}()

	now := time.Now().Truncate(time.Second)

	// 1. Completed job, 2 hours old
	completedAt1 := now.Add(-2 * time.Hour)
	job1 := &Job{
		ID:          "job1",
		Status:      JobStatusCompleted,
		CreatedAt:   now.Add(-3 * time.Hour),
		CompletedAt: &completedAt1,
		Request: ExecRequest{
			Exec: "ls",
		},
	}

	// 2. Completed job, 30 mins old
	completedAt2 := now.Add(-30 * time.Minute)
	job2 := &Job{
		ID:          "job2",
		Status:      JobStatusCompleted,
		CreatedAt:   now.Add(-1 * time.Hour),
		CompletedAt: &completedAt2,
		Request: ExecRequest{
			Exec: "echo hello",
		},
	}

	// 3. Failed job, 15 mins old
	completedAt3 := now.Add(-15 * time.Minute)
	job3 := &Job{
		ID:          "job3",
		Status:      JobStatusFailed,
		CreatedAt:   now.Add(-45 * time.Minute),
		CompletedAt: &completedAt3,
		Request: ExecRequest{
			Exec: "exit 1",
		},
	}

	// 4. Running job, 2 hours old (should NOT be pruned)
	job4 := &Job{
		ID:        "job4",
		Status:    JobStatusRunning,
		CreatedAt: now.Add(-2 * time.Hour),
		Request: ExecRequest{
			Exec: "sleep 100",
		},
	}

	// 5. Queued job, 3 hours old (should NOT be pruned)
	job5 := &Job{
		ID:        "job5",
		Status:    JobStatusQueued,
		CreatedAt: now.Add(-3 * time.Hour),
		Request: ExecRequest{
			Exec: "sleep 200",
		},
	}

	_ = store.Create(job1)
	_ = store.Create(job2)
	_ = store.Create(job3)
	_ = store.Create(job4)
	_ = store.Create(job5)
	
	store.SetIdempotencyKey("key1", "job1")
	store.SetIdempotencyKey("key2", "job2")
	store.SetIdempotencyKey("key3", "job3")
	store.SetIdempotencyKey("key4", "job4")

	t.Run("GetAndPersistence", func(t *testing.T) {
		j, ok := store.Get("job1")
		if !ok {
			t.Fatal("failed to get job1")
		}
		if j.ID != "job1" || j.Status != JobStatusCompleted {
			t.Errorf("unexpected job details: %+v", j)
		}
		if j.Request.Exec != "ls" {
			t.Errorf("expected exec 'ls', got %v", j.Request.Exec)
		}

		// Close and reopen to test persistence
		store.Close()
		store2, err := NewSQLiteJobStore(dbPath)
		if err != nil {
			t.Fatalf("failed to reopen sqlite job store: %v", err)
		}

		j2, ok := store2.Get("job1")
		if !ok {
			t.Fatal("failed to get job1 after reopen")
		}
		if j2.ID != "job1" {
			t.Errorf("expected job1, got %s", j2.ID)
		}
		
		// Set store back for remaining tests
		store = store2
	})

	t.Run("TTLPruning", func(t *testing.T) {
		// Prune jobs older than 1 hour. Should remove job1.
		count, err := store.Prune(0, 1*time.Hour)
		if err != nil {
			t.Fatalf("Prune failed: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 job pruned, got %d", count)
		}
		if _, ok := store.Get("job1"); ok {
			t.Error("job1 should have been pruned")
		}
		if _, ok := store.GetByIdempotencyKey("key1"); ok {
			// In SQLite job store, idempotency keys are NOT automatically pruned by Prune() 
			// unless we explicitly add that logic or use ON DELETE CASCADE + delete the job.
			// My implementation DOES NOT delete from idempotency_keys in Prune() explicitly, 
			// but I used ON DELETE CASCADE.
			// Let's check if it actually deleted it.
			t.Error("idempotency key1 should have been pruned via CASCADE")
		}
		if _, ok := store.Get("job2"); !ok {
			t.Error("job2 should NOT have been pruned")
		}
		if _, ok := store.Get("job4"); !ok {
			t.Error("job4 (running) should NOT have been pruned")
		}
	})

	t.Run("HistoryLimitPruning", func(t *testing.T) {
		// Currently terminal jobs: job2, job3.
		// Set limit to 1. Should remove job2 (it was created before job3).
		count, err := store.Prune(1, 0)
		if err != nil {
			t.Fatalf("Prune failed: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 job pruned, got %d", count)
		}
		if _, ok := store.Get("job2"); ok {
			t.Error("job2 should have been pruned by history limit")
		}
		if _, ok := store.Get("job3"); !ok {
			t.Error("job3 should NOT have been pruned")
		}
		if _, ok := store.Get("job4"); !ok {
			t.Error("job4 (running) should NOT have been pruned")
		}
	})

	t.Run("ListAndPagination", func(t *testing.T) {
		// Clear store and add fresh jobs
		store.db.Exec("DELETE FROM jobs")
		for i := 1; i <= 10; i++ {
			job := &Job{
				ID:        fmt.Sprintf("job%02d", i),
				Status:    JobStatusCompleted,
				CreatedAt: now.Add(time.Duration(i) * time.Minute),
				Request: ExecRequest{
					Exec: "ls",
				},
			}
			store.Create(job)
		}

		// Full list
		jobs, total, err := store.List(ListFilter{})
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		if total != 10 {
			t.Errorf("expected 10 jobs, got %d", total)
		}
		if len(jobs) != 10 {
			t.Errorf("expected length 10, got %d", len(jobs))
		}
		// Newest first
		if jobs[0].ID != "job10" {
			t.Errorf("expected first job to be job10, got %s", jobs[0].ID)
		}

		// Pagination
		jobs, total, _ = store.List(ListFilter{Limit: 3, Offset: 0})
		if len(jobs) != 3 {
			t.Errorf("expected length 3, got %d", len(jobs))
		}
		if jobs[0].ID != "job10" || jobs[1].ID != "job09" || jobs[2].ID != "job08" {
			t.Errorf("unexpected order in page 1")
		}

		jobs, _, _ = store.List(ListFilter{Limit: 3, Offset: 3})
		if len(jobs) != 3 {
			t.Errorf("expected length 3, got %d", len(jobs))
		}
		if jobs[0].ID != "job07" || jobs[1].ID != "job06" || jobs[2].ID != "job05" {
			t.Errorf("unexpected order in page 2")
		}
	})

	t.Run("Idempotency", func(t *testing.T) {
		// Ensure job exists because of FK constraint
		store.Create(&Job{ID: "newjob", Status: JobStatusQueued, CreatedAt: now})
		store.SetIdempotencyKey("newkey", "newjob")
		if id, ok := store.GetByIdempotencyKey("newkey"); !ok || id != "newjob" {
			t.Errorf("expected newjob for newkey, got %s, %v", id, ok)
		}
		
		// Update key
		store.Create(&Job{ID: "newerjob", Status: JobStatusQueued, CreatedAt: now})
		store.SetIdempotencyKey("newkey", "newerjob")
		if id, ok := store.GetByIdempotencyKey("newkey"); !ok || id != "newerjob" {
			t.Errorf("expected newerjob for newkey, got %s, %v", id, ok)
		}
	})
}
