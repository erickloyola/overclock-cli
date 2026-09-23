package pool

import (
	"testing"
	"time"
)

func TestKeyPoolRoundRobin(t *testing.T) {
	keys := []string{"key-1", "key-2", "key-3"}
	pool, err := NewKeyPool(keys)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	seen := make(map[string]int)
	for i := 0; i < 6; i++ {
		state, wait, err := pool.Acquire()
		if err != nil {
			t.Fatalf("acquire failed: %v", err)
		}
		if wait > 0 {
			t.Fatalf("unexpected wait duration: %v", wait)
		}
		seen[state.Key]++
	}

	for _, k := range keys {
		if seen[k] != 2 {
			t.Errorf("expected key %s to be picked 2 times, got %d", k, seen[k])
		}
	}
}

func TestKeyPoolRateLimitFailover(t *testing.T) {
	keys := []string{"key-1", "key-2"}
	pool, err := NewKeyPool(keys)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	state1, _, _ := pool.Acquire()
	// Put state1 into quarantine for 10 seconds
	pool.MarkRateLimited(state1, 10*time.Second)

	expectedOtherKey := "key-2"
	if state1.Key == "key-2" {
		expectedOtherKey = "key-1"
	}

	// Next acquires should consistently skip state1 and pick the other key
	for i := 0; i < 3; i++ {
		nextState, wait, err := pool.Acquire()
		if err != nil {
			t.Fatalf("acquire failed: %v", err)
		}
		if wait > 0 {
			t.Fatalf("unexpected wait duration: %v", wait)
		}
		if nextState.Key != expectedOtherKey {
			t.Errorf("expected %s during %s cooldown, got %s", expectedOtherKey, state1.Key, nextState.Key)
		}
	}
}
