package atomicfile

import (
	"errors"
	"testing"
	"time"
)

var errBusy = errors.New("file is in use")

// fakeClock advances only when retryReplace sleeps.
type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) clock() retryClock {
	return retryClock{
		now:   func() time.Time { return c.now },
		sleep: func(d time.Duration) { c.sleeps = append(c.sleeps, d); c.now = c.now.Add(d) },
	}
}

func isBusy(err error) bool { return errors.Is(err, errBusy) }

func TestRetryReplaceRetriesATransientError(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	attempts := 0
	err := retryReplace(time.Second, 25*time.Millisecond, clock.clock(), isBusy, func() error {
		attempts++
		if attempts < 3 {
			return errBusy
		}
		return nil
	})
	if err != nil || attempts != 3 || len(clock.sleeps) != 2 {
		t.Fatalf("err=%v attempts=%d sleeps=%v, want success on the third attempt", err, attempts, clock.sleeps)
	}
}

func TestRetryReplaceGivesUpAfterTheTimeout(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	attempts := 0
	err := retryReplace(100*time.Millisecond, 30*time.Millisecond, clock.clock(), isBusy, func() error {
		attempts++
		if attempts > 100 {
			t.Fatal("retryReplace kept retrying after the timeout")
		}
		return errBusy
	})
	// Sleeps of 30, 30, 30 and the last 10 ms reach the deadline; the attempt
	// after that is the last one.
	if !errors.Is(err, errBusy) || attempts != 5 {
		t.Fatalf("err=%v attempts=%d sleeps=%v, want errBusy after 5 attempts", err, attempts, clock.sleeps)
	}
	var slept time.Duration
	for _, d := range clock.sleeps {
		slept += d
	}
	if slept != 100*time.Millisecond {
		t.Fatalf("slept %v, want exactly the timeout", slept)
	}
}

func TestRetryReplaceStopsAtAPermanentError(t *testing.T) {
	for name, failure := range map[string]error{
		"not transient": errors.New("permission denied"),
		"unsafe target": &UnsafeTargetError{Reason: "path is a symlink"},
	} {
		clock := &fakeClock{now: time.Unix(0, 0)}
		attempts := 0
		// Even a transient check that accepts everything must not retry an
		// unsafe target.
		transient := func(err error) bool { return name == "unsafe target" || isBusy(err) }
		err := retryReplace(time.Second, 25*time.Millisecond, clock.clock(), transient, func() error {
			attempts++
			return failure
		})
		if !errors.Is(err, failure) || attempts != 1 || len(clock.sleeps) != 0 {
			t.Fatalf("%s: err=%v attempts=%d sleeps=%v, want one attempt", name, err, attempts, clock.sleeps)
		}
	}
}
