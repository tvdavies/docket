package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/cli"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestInstalledPluginCLIPassthroughReceivesVerbatimArgsAndEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	pluginRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pluginRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: example\nversion: 1.0.0\ncli: {run: bin/docket-example}\n"
	if err := os.WriteFile(filepath.Join(pluginRoot, plugin.ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s|%s|%s|%s|%s' \"$1\" \"$2\" \"$DOCKET_PLUGIN\" \"$DOCKET_PLUGIN_ROOT\" \"$DOCKET_HOME\"\n"
	if err := os.WriteFile(filepath.Join(pluginRoot, "bin", "docket-example"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "registry.yaml")
	registry := "plugins:\n  - name: example\n    path: " + pluginRoot + "\n    source: {type: local}\n    version: 1.0.0\n"
	if err := os.WriteFile(configPath, []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	ws, err := workspace.Init(project)
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(project, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=TestPluginPassthroughHelperProcess")
	command.Dir = nested
	command.Env = append(os.Environ(), "DOCKET_TEST_PASSTHROUGH_HELPER=1", "DOCKET_CONFIG="+configPath, "DOCKET_HOME=")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("passthrough helper: %v: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "--hello|world|example|"+pluginRoot+"|"+ws.Root {
		t.Fatalf("passthrough output = %q", got)
	}
}

func TestPluginPassthroughHelperProcess(t *testing.T) {
	if os.Getenv("DOCKET_TEST_PASSTHROUGH_HELPER") != "1" {
		return
	}
	os.Args = []string{"docket", "example", "--hello", "world"}
	os.Exit(cli.Execute())
}
