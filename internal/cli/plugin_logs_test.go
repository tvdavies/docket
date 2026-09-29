package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPrintTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		lines int
		want  string
	}{{2, "two\nthree\n"}, {0, "one\ntwo\nthree\n"}, {10, "one\ntwo\nthree\n"}} {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		offset, err := printTail(file, &out, test.lines)
		file.Close()
		if err != nil || out.String() != test.want || offset != 14 {
			t.Fatalf("lines=%d: %q offset %d err %v", test.lines, out.String(), offset, err)
		}
	}
}

func TestPluginLogsReadsServiceLog(t *testing.T) {
	t.Setenv("DOCKET_STATE_DIR", t.TempDir())
	path := filepath.Join(os.Getenv("DOCKET_STATE_DIR"), "plugins", "example", "service.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("docket: started\nlistening\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newPluginLogsCmd()
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetArgs([]string{"example"})
	if err := command.Execute(); err != nil || out.String() != "docket: started\nlistening\n" {
		t.Fatalf("output %q err %v", out.String(), err)
	}
	missing := newPluginLogsCmd()
	missing.SetArgs([]string{"absent"})
	missing.SetOut(&bytes.Buffer{})
	missing.SetErr(&bytes.Buffer{})
	if err := missing.Execute(); err == nil {
		t.Fatal("missing log did not fail")
	}
}
