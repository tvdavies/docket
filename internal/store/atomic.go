// Package store holds the low-level filesystem primitives shared by docket's
// data types: atomic writes, per-resource locking, monotonic id allocation,
// and YAML-frontmatter parsing. Nothing here knows about tasks specifically.
package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path durably: it writes to a temp file in the
// same directory, fsyncs, then renames over the target. A reader never sees a
// partial file. Parent directories are created as needed.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	// Fsync the directory so the rename is durable.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// AppendLine appends a single line (newline added) to a file, creating it if
// needed. O_APPEND writes of a single small line are atomic on local
// filesystems, so concurrent appenders never interleave.
func AppendLine(path string, line []byte) error {
	return AppendLines(path, [][]byte{line})
}

// AppendLines appends a group of lines in one O_APPEND write. It is used when
// one atomic task mutation produces several ordered events that must not be
// interleaved with another writer's event group.
func AppendLines(path string, lines [][]byte) error {
	if len(lines) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var payload bytes.Buffer
	for _, line := range lines {
		payload.Write(line)
		payload.WriteByte('\n')
	}
	return WithLock(path+".lock", func() error {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		originalSize := info.Size()
		rollback := func(cause error) error {
			truncateErr := f.Truncate(originalSize)
			syncErr := f.Sync()
			if truncateErr != nil {
				cause = errors.Join(cause, fmt.Errorf("truncate partial append: %w", truncateErr))
			}
			if syncErr != nil {
				cause = errors.Join(cause, fmt.Errorf("sync append rollback: %w", syncErr))
			}
			return cause
		}
		written, err := f.Write(payload.Bytes())
		if err != nil {
			return rollback(err)
		}
		if written != payload.Len() {
			return rollback(io.ErrShortWrite)
		}
		if err := f.Sync(); err != nil {
			return rollback(err)
		}
		return nil
	})
}

// EnsureDir creates a directory (and parents) if absent.
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

// Exists reports whether a path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ErrExists indicates a resource that should be created already exists.
var ErrExists = fmt.Errorf("already exists")
