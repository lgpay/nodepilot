package taskqueue

import (
	"sync/atomic"
	"testing"
)

func TestQueueRunsJobsAndStops(t *testing.T) {
	q := New(2, 1)
	var count atomic.Int32
	if !q.Submit(func() { count.Add(1) }) {
		t.Fatal("first job should be accepted")
	}
	q.Stop()
	if count.Load() != 1 {
		t.Fatalf("count = %d, want 1", count.Load())
	}
	if q.Submit(func() {}) {
		t.Fatal("submit after Stop should be rejected")
	}
}

func TestQueueRecoversJobPanic(t *testing.T) {
	q := New(1, 1)
	if !q.Submit(func() { panic("test") }) {
		t.Fatal("panic job should be accepted")
	}
	q.Stop()
}
