package taskqueue

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestPeriodicStopsAndDrains(t *testing.T) {
	var count atomic.Int32
	p := NewPeriodic(2, 1, time.Hour, true, func() { count.Add(1) })
	p.Stop()
	p.Stop()
	if count.Load() != 1 {
		t.Fatalf("count = %d, want 1", count.Load())
	}
}
