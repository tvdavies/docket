package task

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
)

// Comment is an immutable, append-only log entry on a task — one file per
// comment under comments/.
type Comment struct {
	Author    string    `yaml:"author"`
	Session   string    `yaml:"session,omitempty"`
	CreatedAt time.Time `yaml:"created_at"`

	Body string `yaml:"-"`
	File string `yaml:"-"` // base filename, for reference
}

// fsTimestamp renders a timestamp safe for filenames: 2026-06-12T15-10-04Z.
func fsTimestamp(t time.Time) string {
	return strings.ReplaceAll(t.UTC().Format("2006-01-02T15-04-05Z"), ":", "-")
}

// AddComment appends a comment to a task. Comment files are uniquely named and
// never rewritten; the task lock is held only to keep numbering monotonic.
func AddComment(ws *workspace.Workspace, id, author, session, body string) (*Comment, error) {
	return AddCommentWithCommit(ws, id, author, session, body, nil)
}

// AddCommentWithCommit is AddComment plus a commit callback run under the task
// lock with the task as loaded there. A failed commit removes the new comment.
func AddCommentWithCommit(ws *workspace.Workspace, id, author, session, body string, commit func(*Task, *Comment) error) (*Comment, error) {
	dir, err := resolveDir(ws, id)
	if err != nil {
		return nil, err
	}
	commentsDir := filepath.Join(dir, "comments")
	if err := store.EnsureDir(commentsDir); err != nil {
		return nil, err
	}

	c := &Comment{Author: author, Session: session, CreatedAt: Now(), Body: strings.TrimRight(body, "\n")}
	err = store.WithLock(filepath.Join(dir, ".lock"), func() error {
		value, err := loadDir(dir)
		if err != nil {
			return err
		}
		seq := nextCommentSeq(commentsDir)
		c.File = fmt.Sprintf("%04d--%s.md", seq, fsTimestamp(c.CreatedAt))
		data, err := store.RenderFrontmatter(c, c.Body)
		if err != nil {
			return err
		}
		path := filepath.Join(commentsDir, c.File)
		if err := store.WriteAtomic(path, data, 0o644); err != nil {
			return err
		}
		if commit == nil {
			return nil
		}
		if err := commit(value, c); err != nil {
			if Committed(err) {
				return err
			}
			if rollbackErr := os.Remove(path); rollbackErr != nil && !os.IsNotExist(rollbackErr) {
				return errors.Join(err, fmt.Errorf("roll back comment: %w", rollbackErr))
			}
			return err
		}
		return nil
	})
	if err != nil {
		if Committed(err) {
			return c, err
		}
		return nil, err
	}
	return c, nil
}

func nextCommentSeq(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 1
	}
	max := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(name, "%04d--", &n); err == nil && n > max {
			max = n
		}
	}
	return max + 1
}

// Comments loads all comments for a task in chronological (filename) order.
func (t *Task) Comments() ([]*Comment, error) {
	entries, err := os.ReadDir(t.CommentsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []*Comment
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(t.CommentsDir(), name))
		if err != nil {
			continue
		}
		var c Comment
		body, err := store.ParseFrontmatter(data, &c)
		if err != nil {
			continue
		}
		c.Body = strings.TrimRight(body, "\n")
		c.File = name
		out = append(out, &c)
	}
	return out, nil
}
