package plugin

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// MaxUIFiles bounds the static asset tree a plugin may publish.
const MaxUIFiles = 4096

// UIDir returns the absolute static UI directory, or "" when none is declared.
func (m *Manifest) UIDir() string {
	if m.UI.Dir == "" {
		return ""
	}
	return filepath.Join(m.Root, filepath.FromSlash(m.UI.Dir))
}

// UIHash fingerprints the UI directory by path, size and modification time.
// It names an immutable asset URL generation, so any edit yields a new hash
// without reading file contents on every board request.
func (m *Manifest) UIHash() (string, error) {
	dir := m.UIDir()
	if dir == "" {
		return "", nil
	}
	digest := sha256.New()
	digest.Write([]byte(m.Version))
	count := 0
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		count++
		if count > MaxUIFiles {
			return errors.New("ui.dir contains too many files")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(dir, path)
		digest.Write([]byte(filepath.ToSlash(relative)))
		var buffer [17]byte
		binary.LittleEndian.PutUint64(buffer[:8], uint64(info.Size()))
		binary.LittleEndian.PutUint64(buffer[8:16], uint64(info.ModTime().UnixNano()))
		digest.Write(buffer[:])
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil))[:16], nil
}
