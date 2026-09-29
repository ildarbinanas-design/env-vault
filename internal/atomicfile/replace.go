package atomicfile

import "time"

// retryClock lets tests drive retryReplace without waiting.
type retryClock struct {
	now   func() time.Time
	sleep func(time.Duration)
}

// retryReplace runs replaceOnce until it succeeds, fails with an error that
// transient does not accept, or timeout has passed since the first attempt.
// An unsafe target is never retried. It returns the last error.
func retryReplace(timeout, delay time.Duration, clock retryClock, transient func(error) bool, replaceOnce func() error) error {
	deadline := clock.now().Add(timeout)
	for {
		err := replaceOnce()
		if err == nil || IsUnsafeTarget(err) || !transient(err) {
			return err
		}
		remaining := deadline.Sub(clock.now())
		if remaining <= 0 {
			return err
		}
		clock.sleep(min(delay, remaining))
	}
}
