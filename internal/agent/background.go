package agent

import "sync"

var (
	backgroundStop = make(chan struct{})
	backgroundWG   sync.WaitGroup
)

func startBackground() {
	backgroundWG.Add(1)
}

func finishBackground() {
	backgroundWG.Done()
}

// StopBackgroundWorkers stops heartbeat and traffic collection and waits for
// in-flight periodic work to finish.
func StopBackgroundWorkers() {
	select {
	case <-backgroundStop:
	default:
		close(backgroundStop)
	}
	backgroundWG.Wait()
}
