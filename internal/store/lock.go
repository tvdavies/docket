package store

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// WithLock acquires an exclusive flock on lockPath, runs fn, then releases.
// The lock file is created if absent. It blocks (with a generous timeout) for
// another holder rather than failing immediately, so concurrent writers
// serialise instead of erroring.
func WithLock(lockPath string, fn func() error) error {
	return WithLockContext(context.Background(), lockPath, fn)
}

// WithLockContext is WithLock with cancellation propagated by the caller. A
// 30-second upper bound still protects callers that provide no deadline.
func WithLockContext(parent context.Context, lockPath string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	fl := flock.New(lockPath)
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if _, err := fl.TryLockContext(ctx, 25*time.Millisecond); err != nil {
		return err
	}
	defer fl.Unlock()
	return fn()
}

// TryWithLock runs fn under an exclusive flock when it can acquire the lock
// immediately. A false acquired result is not an error. This is used by
// post-hoc event handlers so a handler-triggered docket command can drain other
// handlers without deadlocking on the handler that invoked it.
func TryWithLock(lockPath string, fn func() error) (acquired bool, err error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return false, err
	}
	fl := flock.New(lockPath)
	acquired, err = fl.TryLock()
	if err != nil || !acquired {
		return acquired, err
	}
	defer fl.Unlock()
	return true, fn()
}
