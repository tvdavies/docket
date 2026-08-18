package task

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
	"gopkg.in/yaml.v3"
)

// Attachment is one entry in a task's attachments/manifest.yaml.
type Attachment struct {
	File    string `yaml:"file" json:"file"`
	Mime    string `yaml:"mime" json:"mime"`
	Caption string `yaml:"caption,omitempty" json:"caption,omitempty"`
	AddedBy string `yaml:"added_by" json:"added_by"`
	AddedAt string `yaml:"added_at" json:"added_at"`
	Bytes   int64  `yaml:"bytes" json:"bytes"`
}

const manifestName = "manifest.yaml"

// AttachFile copies src into the task's attachments/ folder and records it in
// the manifest under the task lock. Returns the recorded entry.
func AttachFile(ws *workspace.Workspace, id, src, caption, addedBy string) (*Attachment, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("read source: %w", err)
	}
	return AttachData(ws, id, filepath.Base(src), data, caption, addedBy)
}

// AttachData stores in-memory file data as a durable task attachment.
func AttachData(ws *workspace.Workspace, id, name string, data []byte, caption, addedBy string) (*Attachment, error) {
	dir, err := resolveDir(ws, id)
	if err != nil {
		return nil, err
	}
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" || base == "." || base != name || strings.ContainsAny(base, `/\\`) {
		return nil, fmt.Errorf("invalid attachment filename %q", name)
	}
	attachDir := filepath.Join(dir, "attachments")
	if err := store.EnsureDir(attachDir); err != nil {
		return nil, err
	}
	att := &Attachment{
		File: base, Mime: detectMime(base, data), Caption: strings.TrimSpace(caption),
		AddedBy: strings.TrimSpace(addedBy), AddedAt: Now().Format(timeLayout), Bytes: int64(len(data)),
	}
	err = store.WithLock(filepath.Join(dir, ".lock"), func() error {
		dest := uniqueDest(attachDir, base)
		att.File = filepath.Base(dest)
		manifest, err := loadManifest(attachDir)
		if err != nil {
			return err
		}
		if err := store.WriteAtomic(dest, data, 0o644); err != nil {
			return err
		}
		manifest = append(manifest, att)
		if err := saveManifest(attachDir, manifest); err != nil {
			_ = os.Remove(dest)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return att, nil
}

const timeLayout = "2006-01-02T15:04:05Z07:00"

// RemoveAttachment removes an exact manifest entry and its file. It is used to
// roll back an attachment when the corresponding durable event cannot commit.
func RemoveAttachment(ws *workspace.Workspace, id, name string) error {
	dir, err := resolveDir(ws, id)
	if err != nil {
		return err
	}
	return store.WithLock(filepath.Join(dir, ".lock"), func() error {
		attachDir := filepath.Join(dir, "attachments")
		manifest, err := loadManifest(attachDir)
		if err != nil {
			return err
		}
		index := -1
		for i, item := range manifest {
			if item != nil && item.File == name {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("attachment %q not found", name)
		}
		if err := os.Remove(filepath.Join(attachDir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
		manifest = append(manifest[:index], manifest[index+1:]...)
		return saveManifest(attachDir, manifest)
	})
}

// AttachmentPath returns an exact manifest-backed path. Names not recorded in
// the manifest are rejected even if a similarly named file exists on disk.
func (t *Task) AttachmentPath(name string) (string, *Attachment, error) {
	if filepath.Base(name) != name || name == "" || strings.ContainsAny(name, `/\\`) {
		return "", nil, fmt.Errorf("invalid attachment filename %q", name)
	}
	manifest, err := t.Attachments()
	if err != nil {
		return "", nil, err
	}
	for _, item := range manifest {
		if item != nil && item.File == name {
			return filepath.Join(t.AttachmentsDir(), name), item, nil
		}
	}
	return "", nil, fmt.Errorf("attachment %q not found", name)
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
func (t *Task) Attachments() ([]*Attachment, error) { return loadManifest(t.AttachmentsDir()) }

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

func detectMime(name string, data []byte) string {
	if value := mime.TypeByExtension(filepath.Ext(name)); value != "" {
		if i := strings.IndexByte(value, ';'); i >= 0 {
			value = value[:i]
		}
		return value
	}
	value := http.DetectContentType(data)
	if i := strings.IndexByte(value, ';'); i >= 0 {
		value = value[:i]
	}
	return value
}
