package taskqueue

import (
	"sync"
	"time"
)

// Periodic runs a job on a bounded queue at a fixed interval.
// Stop prevents future runs, drains the queued job, and waits for workers.
type Periodic struct {
	queue    *Queue
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func NewPeriodic(buffer, workers int, interval time.Duration, immediate bool, job Job) *Periodic {
	p := &Periodic{
		queue: New(buffer, workers),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go func() {
		defer close(p.done)
		if immediate && !p.queue.Submit(job) {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.queue.Submit(job)
			case <-p.stop:
				p.queue.Stop()
				return
			}
		}
	}()
	return p
}

// Stop is idempotent.
func (p *Periodic) Stop() {
	p.stopOnce.Do(func() { close(p.stop) })
	<-p.done
}
