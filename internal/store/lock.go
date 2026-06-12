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
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	fl := flock.New(lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := fl.TryLockContext(ctx, 25*time.Millisecond); err != nil {
		return err
	}
	defer fl.Unlock()
	return fn()
}
