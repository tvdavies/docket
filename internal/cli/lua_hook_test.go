package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/workspace"
)

func TestHiddenLuaHookCommandRunsProductionCommandPath(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "hooks", "cli.lua")
	source := `
function handle(event, docket)
    local file = assert(io.open(docket.path("from-cli.txt"), "w"))
    file:write(event.task)
    file:close()
end
`
	if err := os.WriteFile(script, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	command := newRootCmd()
	command.SetArgs([]string{"__lua-hook", script})
	command.SetIn(strings.NewReader("{\"seq\":1,\"type\":\"task.created\",\"task\":\"TASK-0042\"}\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatalf("hidden Lua command: %v (%s)", err, output.String())
	}

	data, err := os.ReadFile(filepath.Join(root, "from-cli.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "TASK-0042" {
		t.Fatalf("Lua command wrote %q", data)
	}
}
