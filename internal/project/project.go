// Package project implements named groupings of tasks. A project is a single
// markdown file with frontmatter — optional metadata, not a container.
package project

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

// Now is the clock for timestamps; overridable in tests.
var Now = func() time.Time { return time.Now().UTC() }

// Project is a named grouping of tasks.
type Project struct {
	ID        string    `yaml:"id"`
	Name      string    `yaml:"name"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`

	Description string `yaml:"-"`
	file        string `yaml:"-"`
}

func slug(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
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
	return strings.Trim(b.String(), "-")
}

func resolveFile(ws *workspace.Workspace, id string) (string, error) {
	matches, _ := filepath.Glob(filepath.Join(ws.ProjectsDir(), id+"-*.md"))
	exact := filepath.Join(ws.ProjectsDir(), id+".md")
	if store.Exists(exact) {
		matches = append(matches, exact)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("project %q not found", id)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous project id %q", id)
	}
}

// Load reads a project by id.
func Load(ws *workspace.Workspace, id string) (*Project, error) {
	file, err := resolveFile(ws, id)
	if err != nil {
		return nil, err
	}
	return loadFile(file)
}

func loadFile(file string) (*Project, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var p Project
	body, err := store.ParseFrontmatter(data, &p)
	if err != nil {
		return nil, err
	}
	p.Description = strings.TrimRight(body, "\n")
	p.file = file
	return &p, nil
}

func maxExistingID(ws *workspace.Workspace) int {
	prefix := ws.Config.Settings.ProjectPrefix
	entries, err := os.ReadDir(ws.ProjectsDir())
	if err != nil {
		return 0
	}
	max := 0
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".md")
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

// Create allocates an id and writes a new project file.
func Create(ws *workspace.Workspace, name, description string) (*Project, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("project name is required")
	}
	id, err := store.NextID(
		ws.Path(".next-project-id"),
		ws.Path(".next-project-id.lock"),
		ws.Config.Settings.ProjectPrefix,
		ws.Config.Settings.ProjectPadding,
		maxExistingID(ws),
	)
	if err != nil {
		return nil, err
	}
	now := Now()
	fileName := id
	if s := slug(name); s != "" {
		fileName = id + "-" + s
	}
	p := &Project{
		ID:          id,
		Name:        strings.TrimSpace(name),
		CreatedAt:   now,
		UpdatedAt:   now,
		Description: strings.TrimRight(description, "\n"),
		file:        filepath.Join(ws.ProjectsDir(), fileName+".md"),
	}
	data, err := store.RenderFrontmatter(p, p.Description)
	if err != nil {
		return nil, err
	}
	if err := store.WriteAtomic(p.file, data, 0o644); err != nil {
		return nil, err
	}
	return p, nil
}

// All lists every project, sorted by id.
func All(ws *workspace.Workspace) ([]*Project, error) {
	entries, err := os.ReadDir(ws.ProjectsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var projects []*Project
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p, err := loadFile(filepath.Join(ws.ProjectsDir(), e.Name()))
		if err != nil {
			continue
		}
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	return projects, nil
}
