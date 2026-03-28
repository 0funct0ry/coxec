package server

import (
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
