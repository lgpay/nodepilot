package server

// StopBackgroundWorkers stops all periodic control-plane tasks and drains
// their currently queued work.
func StopBackgroundWorkers() {
	stopProbeScheduler()
	stopCertRenewScheduler()
	stopAlertScheduler()
}
