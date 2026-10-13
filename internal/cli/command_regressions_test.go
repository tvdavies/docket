package cli_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/workspace"
)

func TestPluginValidateHonoursJSONAfterBuiltinNameCheck(t *testing.T) {
	plugin, err := filepath.Abs("../../examples/plugins/hello-hooks")
	if err != nil {
		t.Fatal(err)
	}
	out, stderr, err := runDocket(t, t.TempDir(), "plugin", "validate", plugin, "--json")
	if err != nil {
		t.Fatalf("docket: %v: %s", err, stderr)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
}

func TestLinkRemoveRejectsSameTaskWithoutWaitingForLock(t *testing.T) {
	dir := t.TempDir()
	if _, err := workspace.Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := runDocket(t, dir, "new", "--title", "Only task"); err != nil {
		t.Fatalf("new: %v: %s", err, stderr)
	}
	start := time.Now()
	_, stderr, err := runDocket(t, dir, "link", "TASK-0001", "--blocks", "TASK-0001", "--remove")
	if err == nil || !strings.Contains(stderr, "two different tasks") {
		t.Fatalf("err = %v, stderr = %q", err, stderr)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("self-unlink took %s; it should fail before taking locks", elapsed)
	}
}
