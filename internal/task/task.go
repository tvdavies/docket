// Package task implements tadu's core data type: a file-backed task folder
// containing a markdown dossier (`task.md`), append-only comments, attachments,
// and a session audit log. All mutating operations take a per-task flock and
// write atomically.
package task

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tvdavies/tadu/internal/store"
	"github.com/tvdavies/tadu/internal/workspace"
)

// Now is the clock used for timestamps; overridable in tests.
var Now = func() time.Time { return time.Now().UTC() }

// Task is the in-memory view of a task. The yaml tags define its frontmatter;
// Description is the markdown body and dir is the on-disk location (neither is
// serialised into frontmatter).
type Task struct {
	ID            string              `yaml:"id"`
	Title         string              `yaml:"title"`
	Status        string              `yaml:"status"`
	Project       string              `yaml:"project,omitempty"`
	Labels        []string            `yaml:"labels"`
	Assignee      string              `yaml:"assignee,omitempty"`
	Relationships map[string][]string `yaml:"relationships,omitempty"`
	CreatedAt     time.Time           `yaml:"created_at"`
	UpdatedAt     time.Time           `yaml:"updated_at"`

	Description string `yaml:"-"`
	dir         string `yaml:"-"`
}

// Dir is the absolute path to the task's folder.
func (t *Task) Dir() string { return t.dir }

// TaskFile is the path to the task's markdown dossier.
func (t *Task) TaskFile() string { return filepath.Join(t.dir, "task.md") }

// LockFile is the per-task flock path.
func (t *Task) LockFile() string { return filepath.Join(t.dir, ".lock") }

// CommentsDir holds append-only comment files.
func (t *Task) CommentsDir() string { return filepath.Join(t.dir, "comments") }

// AttachmentsDir holds attachment files plus their manifest.
func (t *Task) AttachmentsDir() string { return filepath.Join(t.dir, "attachments") }

// SessionsFile is the append-only attach/detach audit log.
func (t *Task) SessionsFile() string { return filepath.Join(t.dir, "sessions.jsonl") }

// slug turns a title into a filesystem-friendly suffix.
func slug(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 50 {
		s = strings.Trim(s[:50], "-")
	}
	return s
}

// resolveDir finds the on-disk folder for an id, which carries a title slug
// suffix (e.g. tasks/TASK-0007-fix-login-cache). Falls back to an exact match.
func resolveDir(ws *workspace.Workspace, id string) (string, error) {
	exact := ws.TaskDir(id)
	if store.Exists(exact) {
		return exact, nil
	}
	matches, _ := filepath.Glob(ws.TaskDir(id) + "-*")
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("ambiguous task id %q matches %d folders", id, len(matches))
	}
	return "", fmt.Errorf("task %q not found", id)
}

// Load reads a task by id.
func Load(ws *workspace.Workspace, id string) (*Task, error) {
	dir, err := resolveDir(ws, id)
	if err != nil {
		return nil, err
	}
	return loadDir(dir)
}

func loadDir(dir string) (*Task, error) {
	data, err := os.ReadFile(filepath.Join(dir, "task.md"))
	if err != nil {
		return nil, err
	}
	var t Task
	body, err := store.ParseFrontmatter(data, &t)
	if err != nil {
		return nil, err
	}
	t.Description = strings.TrimRight(body, "\n")
	t.dir = dir
	return &t, nil
}

// save writes the dossier atomically. Callers must hold the task lock.
func (t *Task) save() error {
	if t.Labels == nil {
		t.Labels = []string{}
	}
	data, err := store.RenderFrontmatter(t, t.Description)
	if err != nil {
		return err
	}
	return store.WriteAtomic(t.TaskFile(), data, 0o644)
}

// maxExistingID scans the tasks directory for the highest numeric id.
func maxExistingID(ws *workspace.Workspace) int {
	prefix := ws.Config.Settings.IDPrefix
	entries, err := os.ReadDir(ws.TasksDir())
	if err != nil {
		return 0
	}
	max := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Strip slug suffix: TASK-0007-foo → TASK-0007.
		idPart := name
		if i := strings.Index(name, "-"); i >= 0 {
			if j := strings.Index(name[i+1:], "-"); j >= 0 {
				idPart = name[:i+1+j]
			}
		}
		if n, ok := store.ParseIDNumber(prefix, idPart); ok && n > max {
			max = n
		}
	}
	return max
}

// CreateOptions configures a new task.
type CreateOptions struct {
	Title       string
	Description string
	Project     string
	Labels      []string
	Assignee    string
	Status      string // defaults to the first configured status
}

// Create allocates an id, scaffolds the task folder, and writes task.md.
func Create(ws *workspace.Workspace, opts CreateOptions) (*Task, error) {
	if strings.TrimSpace(opts.Title) == "" {
		return nil, fmt.Errorf("title is required")
	}
	status := opts.Status
	if status == "" {
		if len(ws.Config.Statuses) == 0 {
			return nil, fmt.Errorf("config has no statuses")
		}
		status = ws.Config.Statuses[0]
	}
	if !ws.Config.HasStatus(status) {
		return nil, fmt.Errorf("unknown status %q", status)
	}

	id, err := store.NextID(
		ws.Path(".next-id"),
		ws.Path(".next-id.lock"),
		ws.Config.Settings.IDPrefix,
		ws.Config.Settings.IDPadding,
		maxExistingID(ws),
	)
	if err != nil {
		return nil, err
	}

	now := Now()
	dirName := id
	if s := slug(opts.Title); s != "" {
		dirName = id + "-" + s
	}
	t := &Task{
		ID:            id,
		Title:         strings.TrimSpace(opts.Title),
		Status:        status,
		Project:       opts.Project,
		Labels:        opts.Labels,
		Assignee:      opts.Assignee,
		Relationships: map[string][]string{},
		CreatedAt:     now,
		UpdatedAt:     now,
		Description:   strings.TrimRight(opts.Description, "\n"),
		dir:           filepath.Join(ws.TasksDir(), dirName),
	}
	if t.Labels == nil {
		t.Labels = []string{}
	}
	if store.Exists(t.dir) {
		return nil, fmt.Errorf("task folder %s already exists", dirName)
	}
	if err := store.EnsureDir(t.dir); err != nil {
		return nil, err
	}
	if err := t.save(); err != nil {
		return nil, err
	}
	return t, nil
}

// Update runs fn against the task under an exclusive lock, bumps updated_at,
// and persists the result. fn mutates the loaded task in place.
func Update(ws *workspace.Workspace, id string, fn func(t *Task) error) (*Task, error) {
	dir, err := resolveDir(ws, id)
	if err != nil {
		return nil, err
	}
	var result *Task
	err = store.WithLock(filepath.Join(dir, ".lock"), func() error {
		t, err := loadDir(dir)
		if err != nil {
			return err
		}
		if err := fn(t); err != nil {
			return err
		}
		t.UpdatedAt = Now()
		if err := t.save(); err != nil {
			return err
		}
		result = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// All loads every task in the workspace (scan-on-read), sorted by id.
func All(ws *workspace.Workspace) ([]*Task, error) {
	entries, err := os.ReadDir(ws.TasksDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var tasks []*Task
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := loadDir(filepath.Join(ws.TasksDir(), e.Name()))
		if err != nil {
			// Skip unreadable folders rather than failing the whole listing.
			continue
		}
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks, nil
}

// HasLabel reports whether the task carries label l.
func (t *Task) HasLabel(l string) bool {
	for _, x := range t.Labels {
		if x == l {
			return true
		}
	}
	return false
}
