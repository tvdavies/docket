package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// internalGitignore lists rebuildable / machine-local paths inside .tadu that
// should not be committed.
const internalGitignore = `# Rebuildable cache and machine-local session state — safe to ignore.
.index/
.sessions/
.cursors/
*.lock
.next-id.lock
.next-project-id.lock
`

// Init scaffolds a new `.tadu/` workspace under root and returns it. It fails
// if a workspace already exists there.
func Init(root string) (*Workspace, error) {
	taduDir := filepath.Join(root, DirName)
	if isDir(taduDir) {
		return nil, fmt.Errorf("workspace already exists at %s", taduDir)
	}
	for _, sub := range []string{"", "tasks", "projects"} {
		if err := os.MkdirAll(filepath.Join(taduDir, sub), 0o755); err != nil {
			return nil, err
		}
	}

	cfg := DefaultConfig()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	header := "# tadu workspace config. Statuses double as board lanes, in order.\n"
	if err := os.WriteFile(filepath.Join(taduDir, "config.yaml"), append([]byte(header), data...), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(taduDir, ".gitignore"), []byte(internalGitignore), 0o644); err != nil {
		return nil, err
	}

	return &Workspace{Root: taduDir, Config: cfg}, nil
}
