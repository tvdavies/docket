// Package store holds the low-level filesystem primitives shared by tadu's
// data types: atomic writes, per-resource locking, monotonic id allocation,
// and YAML-frontmatter parsing. Nothing here knows about tasks specifically.
package store

import (
	"fmt"
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
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
