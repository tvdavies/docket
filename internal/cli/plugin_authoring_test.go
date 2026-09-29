package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/plugin/scaffold"
	docketservice "github.com/tvdavies/docket/internal/service"
)

func TestPluginValidateReportsMissingFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "example")
	if _, err := scaffold.Create(dir, scaffold.Options{Name: "example", Page: true}); err != nil {
		t.Fatal(err)
	}
	run := func() (string, error) {
		command := newPluginValidateCmd()
		var out bytes.Buffer
		command.SetOut(&out)
		command.SetErr(&bytes.Buffer{})
		command.SetArgs([]string{dir})
		err := command.Execute()
		return out.String(), err
	}
	if out, err := run(); err != nil || !strings.Contains(out, "example 0.1.0: ok") {
		t.Fatalf("output %q err %v", out, err)
	}
	if err := os.Remove(filepath.Join(dir, "ui", "page.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := run(); err == nil || !strings.Contains(err.Error(), "ui.pages[0].entry: ui/page.html does not exist") {
		t.Fatalf("err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docket-plugin.yaml"), []byte("name: example\nversion: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(); err == nil || !strings.Contains(err.Error(), "invalid manifest") {
		t.Fatalf("err = %v", err)
	}
}

func TestPluginNewRejectsBuiltinNames(t *testing.T) {
	command := newPluginNewCmd()
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"docs", "--dir", filepath.Join(t.TempDir(), "docs")})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "builtin") {
		t.Fatalf("err = %v", err)
	}
}

func TestDescribePluginChange(t *testing.T) {
	loaded := &docketservice.PluginState{Name: "p", Version: "1.0.0", ManifestHash: "m1", UIHash: "u1", UIBase: "/plugin-ui/p/u1"}
	running := *loaded
	running.Service = &docketservice.ServiceStatus{State: "running", PID: 42}
	reloaded := running
	reloaded.UIHash, reloaded.UIBase = "u2", "/plugin-ui/p/u2"
	restarted := reloaded
	restarted.Service = &docketservice.ServiceStatus{State: "backoff", Restarts: 1, LastError: "exit status 1"}
	broken := restarted
	broken.Error, broken.ManifestHash, broken.Service = "bad version", "m2", nil
	steps := []struct {
		previous, current *docketservice.PluginState
		want              []string
	}{
		{nil, loaded, []string{"plugin p 1.0.0 loaded (ui /plugin-ui/p/u1)"}},
		{loaded, &running, []string{"service running (pid 42)"}},
		{&running, &running, nil},
		{&running, &reloaded, []string{"ui reloaded: open frames now use /plugin-ui/p/u2"}},
		{&reloaded, &restarted, []string{"service backoff, 1 restart(s): exit status 1"}},
		{&restarted, &broken, []string{"plugin error: bad version", "manifest reloaded (1.0.0)", "service stopped"}},
		{&broken, nil, []string{"plugin is no longer installed"}},
	}
	for index, step := range steps {
		if got := describePluginChange(step.previous, step.current); strings.Join(got, "|") != strings.Join(step.want, "|") {
			t.Fatalf("step %d: got %q, want %q", index, got, step.want)
		}
	}
}

func TestPrefixWriterForwardsCompleteLines(t *testing.T) {
	lines := make(chan string, 4)
	writer := &prefixWriter{prefix: "> ", out: lines}
	_, _ = writer.Write([]byte("one\ntw"))
	_, _ = writer.Write([]byte("o\n"))
	if got := []string{<-lines, <-lines}; got[0] != "> one" || got[1] != "> two" || len(lines) != 0 {
		t.Fatalf("lines = %q", got)
	}
}
