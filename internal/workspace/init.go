package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// internalGitignore lists rebuildable / machine-local paths inside .docket that
// should not be committed.
const internalGitignore = `# Rebuildable cache and machine-local session state — safe to ignore.
.index/
.sessions/
.cursors/
*.lock
.next-id.lock
.next-project-id.lock
`

// Init scaffolds a new `.docket/` workspace under root and returns it. It fails
// if a workspace already exists there.
func Init(root string) (*Workspace, error) {
	docketDir := filepath.Join(root, DirName)
	if isDir(docketDir) {
		return nil, fmt.Errorf("workspace already exists at %s", docketDir)
	}
	for _, sub := range []string{"", "tasks", "projects"} {
		if err := os.MkdirAll(filepath.Join(docketDir, sub), 0o755); err != nil {
			return nil, err
		}
	}

	cfg := DefaultConfig()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	header := "# docket workspace config. Statuses double as board lanes, in order.\n"
	if err := os.WriteFile(filepath.Join(docketDir, "config.yaml"), append([]byte(header), data...), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(docketDir, ".gitignore"), []byte(internalGitignore), 0o644); err != nil {
		return nil, err
	}

	return &Workspace{Root: docketDir, Config: cfg}, nil
}
