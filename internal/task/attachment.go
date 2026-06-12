package task

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tvdavies/tadu/internal/store"
	"github.com/tvdavies/tadu/internal/workspace"
	"gopkg.in/yaml.v3"
)

// Attachment is one entry in a task's attachments/manifest.yaml.
type Attachment struct {
	File    string `yaml:"file"`
	Mime    string `yaml:"mime"`
	Caption string `yaml:"caption,omitempty"`
	AddedBy string `yaml:"added_by"`
	AddedAt string `yaml:"added_at"`
	Bytes   int64  `yaml:"bytes"`
}

const manifestName = "manifest.yaml"

// AttachFile copies src into the task's attachments/ folder and records it in
// the manifest under the task lock. Returns the recorded entry.
func AttachFile(ws *workspace.Workspace, id, src, caption, addedBy string) (*Attachment, error) {
	dir, err := resolveDir(ws, id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("read source: %w", err)
	}
	attachDir := filepath.Join(dir, "attachments")
	if err := store.EnsureDir(attachDir); err != nil {
		return nil, err
	}

	base := filepath.Base(src)
	att := &Attachment{
		File:    base,
		Mime:    detectMime(base, data),
		Caption: caption,
		AddedBy: addedBy,
		AddedAt: Now().Format("2006-01-02T15:04:05Z07:00"),
		Bytes:   int64(len(data)),
	}

	err = store.WithLock(filepath.Join(dir, ".lock"), func() error {
		dest := uniqueDest(attachDir, base)
		att.File = filepath.Base(dest)
		if err := store.WriteAtomic(dest, data, 0o644); err != nil {
			return err
		}
		manifest, err := loadManifest(attachDir)
		if err != nil {
			return err
		}
		manifest = append(manifest, att)
		return saveManifest(attachDir, manifest)
	})
	if err != nil {
		return nil, err
	}
	return att, nil
}

// uniqueDest avoids clobbering an existing attachment with the same name.
func uniqueDest(dir, base string) string {
	dest := filepath.Join(dir, base)
	if !store.Exists(dest) {
		return dest
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		if !store.Exists(candidate) {
			return candidate
		}
	}
}

// Attachments returns the task's manifest entries.
func (t *Task) Attachments() ([]*Attachment, error) {
	return loadManifest(t.AttachmentsDir())
}

func loadManifest(attachDir string) ([]*Attachment, error) {
	data, err := os.ReadFile(filepath.Join(attachDir, manifestName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var manifest []*Attachment
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return manifest, nil
}

func saveManifest(attachDir string, manifest []*Attachment) error {
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return err
	}
	return store.WriteAtomic(filepath.Join(attachDir, manifestName), data, 0o644)
}

// detectMime resolves a content type from the extension, falling back to
// sniffing the leading bytes.
func detectMime(name string, data []byte) string {
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		if i := strings.IndexByte(t, ';'); i >= 0 {
			t = t[:i]
		}
		return t
	}
	t := http.DetectContentType(data)
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}
	return t
}
