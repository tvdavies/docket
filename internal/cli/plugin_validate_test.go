package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginValidateReportsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	manifest := "name: example\nversion: 0.1.0\nhandlers:\n  react: {on: [task.created], lua: hooks/react.lua}\ncli: {run: bin/example}\n" +
		"service: {url: 'http://127.0.0.1:9', command: [bin/serve]}\nui:\n  dir: ui\n  pages: [{id: home, title: Home, entry: page.html}]\n"
	for path, body := range map[string]string{"docket-plugin.yaml": manifest, "hooks/react.lua": "function handle() end\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() (string, string, error) {
		command := newPluginValidateCmd()
		var out, errOut bytes.Buffer
		command.SetOut(&out)
		command.SetErr(&errOut)
		command.SetArgs([]string{dir})
		err := command.Execute()
		return out.String(), errOut.String(), err
	}
	if _, _, err := run(); err == nil || !strings.Contains(err.Error(), "cli.run: bin/example does not exist") {
		t.Fatalf("err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "example"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The absent ui directory and service.command binary do not invalidate
	// the plugin; the unlaunched command is a warning.
	out, errOut, err := run()
	if err != nil || !strings.Contains(out, "example 0.1.0: ok") {
		t.Fatalf("output %q err %v", out, err)
	}
	if !strings.Contains(errOut, "warning: service.command") {
		t.Fatalf("stderr %q lacks the hosting warning", errOut)
	}
	if err := os.WriteFile(filepath.Join(dir, "docket-plugin.yaml"), []byte("name: example\nversion: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(); err == nil || !strings.Contains(err.Error(), "invalid manifest") {
		t.Fatalf("err = %v", err)
	}
}
