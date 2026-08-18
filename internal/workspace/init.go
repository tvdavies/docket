package workspace

import (
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

// Init ensures a `.docket/` workspace exists under root and returns it.
// Re-running it is a no-op that loads the existing workspace.
func Init(root string) (*Workspace, error) {
	docketDir := filepath.Join(root, DirName)
	if isDir(docketDir) {
		return openRoot(docketDir)
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
	header := `# docket workspace config. Statuses double as board lanes, in order.
#
# Optional post-hoc event handlers use exactly one of lua or run. Paths are
# relative to the directory containing .docket/:
# handlers:
#   notify:
#     on: [task.moved]
#     match:
#       data.to: done
#     lua: hooks/notify.lua
#     delivery: service  # optional; async via docket.service (default: inline)
`
	if err := os.WriteFile(filepath.Join(docketDir, "config.yaml"), append([]byte(header), data...), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(docketDir, ".gitignore"), []byte(internalGitignore), 0o644); err != nil {
		return nil, err
	}

	return &Workspace{Root: docketDir, Config: cfg}, nil
}
