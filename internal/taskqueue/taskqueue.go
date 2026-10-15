// Package taskqueue provides a small bounded in-process worker queue.
package taskqueue

import "sync"

type Job func()

type Queue struct {
	jobs     chan Job
	workers  sync.WaitGroup
	mu       sync.RWMutex
	stopped  bool
	stopOnce sync.Once
}

func New(buffer, workerCount int) *Queue {
	if buffer < 1 {
		buffer = 1
	}
	if workerCount < 1 {
		workerCount = 1
	}
	q := &Queue{jobs: make(chan Job, buffer)}
	q.workers.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go q.run()
	}
	return q
}

func (q *Queue) run() {
	defer q.workers.Done()
	for job := range q.jobs {
		func() {
			defer func() { _ = recover() }()
			job()
		}()
	}
}

func (q *Queue) Submit(job Job) bool {
	if job == nil {
		return false
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.stopped {
		return false
	}
	select {
	case q.jobs <- job:
		return true
	default:
		return false
	}
}

// Stop rejects new jobs, drains queued jobs, and waits for workers to exit.
func (q *Queue) Stop() {
	q.stopOnce.Do(func() {
		q.mu.Lock()
		q.stopped = true
		close(q.jobs)
		q.mu.Unlock()
		q.workers.Wait()
	})
}
