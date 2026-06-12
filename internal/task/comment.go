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
// never rewritten, so this needs no task lock — only the per-task comment
// sequence is taken under a short lock to keep numbering monotonic.
func AddComment(ws *workspace.Workspace, id, author, session, body string) (*Comment, error) {
	dir, err := resolveDir(ws, id)
	if err != nil {
		return nil, err
	}
	commentsDir := filepath.Join(dir, "comments")
	if err := store.EnsureDir(commentsDir); err != nil {
		return nil, err
	}

	c := &Comment{Author: author, Session: session, CreatedAt: Now()}
	var fname string
	err = store.WithLock(filepath.Join(dir, ".lock"), func() error {
		seq := nextCommentSeq(commentsDir)
		fname = fmt.Sprintf("%04d--%s.md", seq, fsTimestamp(c.CreatedAt))
		data, err := store.RenderFrontmatter(c, strings.TrimRight(body, "\n"))
		if err != nil {
			return err
		}
		return store.WriteAtomic(filepath.Join(commentsDir, fname), data, 0o644)
	})
	if err != nil {
		return nil, err
	}
	c.Body = strings.TrimRight(body, "\n")
	c.File = fname
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
