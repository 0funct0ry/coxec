package server

import (
	"fmt"
	"testing"
	"time"
)

func TestInMemoryJobStore_Prune(t *testing.T) {
	store := NewInMemoryJobStore()
	now := time.Now()

	// 1. Completed job, 2 hours old
	completedAt1 := now.Add(-2 * time.Hour)
	job1 := &Job{
		ID:          "job1",
		Status:      JobStatusCompleted,
		CreatedAt:   now.Add(-3 * time.Hour),
		CompletedAt: &completedAt1,
	}

	// 2. Completed job, 30 mins old
	completedAt2 := now.Add(-30 * time.Minute)
	job2 := &Job{
		ID:          "job2",
		Status:      JobStatusCompleted,
		CreatedAt:   now.Add(-1 * time.Hour),
		CompletedAt: &completedAt2,
	}

	// 3. Failed job, 15 mins old
	completedAt3 := now.Add(-15 * time.Minute)
	job3 := &Job{
		ID:          "job3",
		Status:      JobStatusFailed,
		CreatedAt:   now.Add(-45 * time.Minute),
		CompletedAt: &completedAt3,
	}

	// 4. Running job, 2 hours old (should NOT be pruned)
	job4 := &Job{
		ID:        "job4",
		Status:    JobStatusRunning,
		CreatedAt: now.Add(-2 * time.Hour),
	}

	// 5. Queued job, 3 hours old (should NOT be pruned)
	job5 := &Job{
		ID:        "job5",
		Status:    JobStatusQueued,
		CreatedAt: now.Add(-3 * time.Hour),
	}

	store.Create(job1)
	store.Create(job2)
	store.Create(job3)
	store.Create(job4)
	store.Create(job5)
	
	store.SetIdempotencyKey("key1", "job1")
	store.SetIdempotencyKey("key2", "job2")
	store.SetIdempotencyKey("key3", "job3")
	store.SetIdempotencyKey("key4", "job4")

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
			t.Error("idempotency key1 should have been pruned")
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
		if _, ok := store.GetByIdempotencyKey("key2"); ok {
			t.Error("idempotency key2 should have been pruned")
		}
		if _, ok := store.Get("job3"); !ok {
			t.Error("job3 should NOT have been pruned")
		}
		if _, ok := store.Get("job4"); !ok {
			t.Error("job4 (running) should NOT have been pruned")
		}
	})
	
	t.Run("CombinedPruning", func(t *testing.T) {
		// Reset store
		store = NewInMemoryJobStore()
		store.Create(job1)
		store.Create(job2)
		store.Create(job3)
		
		// TTL 1h removes job1.
		// History limit 1 will then remove job2 (oldest remaining terminal).
		// Final result: only job3 remains.
		count, _ := store.Prune(1, 1*time.Hour)
		if count != 2 {
			t.Errorf("expected 2 jobs pruned, got %d", count)
		}
		if _, ok := store.Get("job1"); ok {
			t.Error("job1 should have been pruned by TTL")
		}
		if _, ok := store.Get("job2"); ok {
			t.Error("job2 should have been pruned by history limit")
		}
		if _, ok := store.Get("job3"); !ok {
			t.Error("job3 should remain")
		}
	})
}

func TestInMemoryJobStore_List(t *testing.T) {
	store := NewInMemoryJobStore()
	now := time.Now()

	// Create 10 jobs with different timestamps
	for i := 1; i <= 10; i++ {
		job := &Job{
			ID:        fmt.Sprintf("job%02d", i),
			Status:    JobStatusCompleted,
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
		}
		store.Create(job)
	}

	t.Run("FullList", func(t *testing.T) {
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
	})

	t.Run("Pagination", func(t *testing.T) {
		// Page 1: limit 3, offset 0
		jobs, total, _ := store.List(ListFilter{Limit: 3, Offset: 0})
		if total != 10 {
			t.Errorf("total should be 10, got %d", total)
		}
		if len(jobs) != 3 {
			t.Errorf("length should be 3, got %d", len(jobs))
		}
		if jobs[0].ID != "job10" || jobs[1].ID != "job09" || jobs[2].ID != "job08" {
			t.Errorf("unexpected order in page 1")
		}

		// Page 2: limit 3, offset 3
		jobs, _, _ = store.List(ListFilter{Limit: 3, Offset: 3})
		if len(jobs) != 3 {
			t.Errorf("length should be 3, got %d", len(jobs))
		}
		if jobs[0].ID != "job07" || jobs[1].ID != "job06" || jobs[2].ID != "job05" {
			t.Errorf("unexpected order in page 2")
		}
	})

	t.Run("TTLFiltering", func(t *testing.T) {
		// Mark job01-job05 as old
		for i := 1; i <= 5; i++ {
			id := fmt.Sprintf("job%02d", i)
			job, _ := store.Get(id)
			comp := now.Add(-2 * time.Hour)
			job.CompletedAt = &comp
			store.Update(job)
		}

		// List with TTL 1h. Should only see job06-job10.
		jobs, total, _ := store.List(ListFilter{TTL: 1 * time.Hour})
		if total != 5 {
			t.Errorf("expected 5 matching jobs, got %d", total)
		}
		if len(jobs) != 5 {
			t.Errorf("expected length 5, got %d", len(jobs))
		}
		for _, j := range jobs {
			if j.ID < "job06" {
				t.Errorf("job %s should have been filtered by TTL", j.ID)
			}
		}
	})
}

func TestInMemoryJobStore_Idempotency(t *testing.T) {
	store := NewInMemoryJobStore()
	
	store.SetIdempotencyKey("key1", "job1")
	
	if id, ok := store.GetByIdempotencyKey("key1"); !ok || id != "job1" {
		t.Errorf("expected job1 for key1, got %s, %v", id, ok)
	}
	
	if id, ok := store.GetByIdempotencyKey("unknown"); ok {
		t.Errorf("expected false for unknown key, got %s", id)
	}
}
