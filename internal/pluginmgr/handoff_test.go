package pluginmgr_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/pluginmgr"
	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
	"gopkg.in/yaml.v3"
)

// fixture is a fully isolated four-handler workspace plus a stub plugin whose
// handlers mirror the legacy ones by name. Every handler appends one line per
// delivered event to a counter file keyed by identity, so replay is visible as
// duplicate (identity-independent) event lines. Nothing here inherits the real
// DOCKET_HOME, registry, Dispatch hooks or agent launch commands.
type fixture struct {
	t          *testing.T
	home       string
	project    string
	pluginRoot string
	counters   string
	template   string
	names      []string
}

const fixturePlugin = "stub"

// handlerSpecs are the four stub handlers: two unconditional, one filtered by
// event type and one matching a data predicate; one uses service delivery.
var handlerSpecs = map[string]struct {
	on       []string
	match    map[string]any
	delivery string
}{
	"alpha": {on: []string{"*"}},
	"beta":  {on: []string{events.TaskMoved}},
	"gamma": {on: []string{events.TaskMoved}, match: map[string]any{"data.to": "done"}},
	"delta": {on: []string{"*"}, delivery: "service"},
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	// Clear everything, then allow only fixture-local state and required
	// system helpers. In particular no outer Dispatch runner or wake command
	// can be found through either environment or PATH.
	tools := map[string]string{}
	for _, name := range []string{"sh", "sed", "sleep", "touch", "cat"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		tools[name] = path
	}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, path := range tools {
		if err := os.Symlink(path, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("TMPDIR", home)
	t.Chdir(home)
	t.Setenv("HOME", home)
	t.Setenv("DOCKET_CONFIG", filepath.Join(home, "registry.yaml"))
	t.Setenv("DOCKET_PLUGIN_DIR", filepath.Join(home, "plugins"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg-data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "xdg-state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "xdg-cache"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "xdg-runtime"))
	for _, name := range []string{"DOCKET_HOME", "DOCKET_HANDLER_STACK", "DOCKET_HANDLER", "DOCKET_ACTOR", "DOCKET_PLUGIN", "DOCKET_PLUGIN_ROOT", "DOCKET_PLUGIN_CONFIG", "DOCKET_SESSION", "DISPATCH_TASK_ID", "DISPATCH_SESSION", "DISPATCH_WAKE_DIR", "DISPATCH_META"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	f := &fixture{t: t, home: home, project: filepath.Join(home, "project"), pluginRoot: filepath.Join(home, "stub-plugin"), counters: filepath.Join(home, "counters")}
	for _, name := range []string{"alpha", "beta", "delta", "gamma"} {
		f.names = append(f.names, name)
	}
	if err := os.MkdirAll(f.counters, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Init(f.project); err != nil {
		t.Fatal(err)
	}
	// Legacy scripts in the project and plugin scripts in the plugin root use
	// the same stub body: append "<identity>\t<seq>" per event.
	for _, base := range []string{f.project, f.pluginRoot} {
		if err := os.MkdirAll(filepath.Join(base, "hooks"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range f.names {
			f.writeScript(base, name, "")
		}
	}
	manifest := "name: " + fixturePlugin + "\nversion: 1.0.0\nhandlers:\n"
	for _, name := range f.names {
		manifest += "  " + name + ": " + f.handlerYAML(name) + "\n"
	}
	manifest += "statuses:\n  - {name: merge, after: in-review}\n"
	if err := os.WriteFile(filepath.Join(f.pluginRoot, plugin.ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginmgr.Add(f.pluginRoot, "", "dev"); err != nil {
		t.Fatal(err)
	}
	legacy := workspace.DefaultConfig()
	legacy.Statuses = []string{"backlog", "ready", "in-progress", "blocked", "in-review", "merge", "done"}
	legacy.Handlers = map[string]workspace.HandlerConfig{}
	for _, name := range f.names {
		legacy.Handlers[name] = f.handlerConfig(name)
	}
	if err := workspace.WriteDeclaredConfig(f.root(), legacy); err != nil {
		t.Fatal(err)
	}
	f.template = filepath.Join(home, "legacy-template.yaml")
	templateBytes, err := yaml.Marshal(&workspace.Config{Handlers: legacy.Handlers})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.template, templateBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) root() string { return filepath.Join(f.project, workspace.DirName) }

func (f *fixture) handlerConfig(name string) workspace.HandlerConfig {
	spec := handlerSpecs[name]
	return workspace.HandlerConfig{On: spec.on, Match: spec.match, Run: "hooks/" + name, Delivery: spec.delivery}
}

func (f *fixture) handlerYAML(name string) string {
	spec := handlerSpecs[name]
	quoted := make([]string, 0, len(spec.on))
	for _, value := range spec.on {
		quoted = append(quoted, `"`+value+`"`)
	}
	parts := []string{"on: [" + strings.Join(quoted, ", ") + "]", "run: hooks/" + name}
	if spec.match != nil {
		parts = append(parts, `match: {"data.to": done}`)
	}
	if spec.delivery != "" {
		parts = append(parts, "delivery: "+spec.delivery)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// writeScript installs the stub for one handler. extra runs after the counter
// append and may exit non-zero to simulate a partial side effect.
func (f *fixture) writeScript(base, name, extra string) {
	f.t.Helper()
	body := "#!/bin/sh\nset -eu\nwhile IFS= read -r line; do\n" +
		"  seq=$(printf '%s' \"$line\" | sed -n 's/.*\"seq\":\\([0-9]*\\).*/\\1/p')\n" +
		"  printf '%s\\t%s\\n' \"$DOCKET_HANDLER\" \"$seq\" >> " + shellQuote(filepath.Join(f.counters, name)) + "\n" +
		"done\n" + extra
	if err := os.WriteFile(filepath.Join(base, "hooks", name), []byte(body), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) open() *workspace.Workspace {
	f.t.Helper()
	ws, err := workspace.OpenRoot(f.project)
	if err != nil {
		f.t.Fatal(err)
	}
	return ws
}

func (f *fixture) appendMove(to string) {
	f.t.Helper()
	if err := events.Append(f.open(), events.Event{Type: events.TaskMoved, Task: "TASK-0001", Data: map[string]any{"to": to}}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) appendCreated() {
	f.t.Helper()
	if err := events.Append(f.open(), events.Event{Type: events.TaskCreated, Task: "TASK-0002"}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) drain() []handlers.Failure {
	return handlers.DrainAll(f.open(), handlers.Options{RefreshConfig: true, Scope: handlers.ScopeAll})
}

func (f *fixture) configHash() string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root(), "config.yaml"))
	if err != nil {
		f.t.Fatal(err)
	}
	return workspace.ConfigHash(data)
}

func (f *fixture) templateBytes() []byte {
	data, err := os.ReadFile(f.template)
	if err != nil {
		f.t.Fatal(err)
	}
	return data
}

func (f *fixture) receiptDir(name string) string {
	return filepath.Join(f.home, "receipt-"+name)
}

func (f *fixture) forward(receipt string) (pluginmgr.HandoffResult, error) {
	return pluginmgr.Handoff(pluginmgr.HandoffRequest{WorkspacePath: f.project, Plugin: fixturePlugin, Direction: pluginmgr.Forward, ReceiptDir: receipt, EngineVersion: "dev"})
}

func (f *fixture) reverse(receipt string) (pluginmgr.HandoffResult, error) {
	return pluginmgr.Handoff(pluginmgr.HandoffRequest{
		WorkspacePath: f.project, Plugin: fixturePlugin, Direction: pluginmgr.Reverse,
		LegacyTemplate: f.templateBytes(), ExpectConfigHash: f.configHash(), ReceiptDir: receipt, EngineVersion: "dev",
	})
}

// deliveries returns, per handler, the sorted list of "identity\tseq" lines.
func (f *fixture) deliveries(name string) []string {
	f.t.Helper()
	file, err := os.Open(filepath.Join(f.counters, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		f.t.Fatal(err)
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			lines = append(lines, scanner.Text())
		}
	}
	return lines
}

// seqs extracts the delivered sequence numbers regardless of identity.
func (f *fixture) seqs(name string) []int {
	var out []int
	for _, line := range f.deliveries(name) {
		_, value, _ := strings.Cut(line, "\t")
		n, _ := strconv.Atoi(value)
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

func (f *fixture) assertNoDuplicates(name string) {
	f.t.Helper()
	seen := map[int]string{}
	for _, line := range f.deliveries(name) {
		identity, value, _ := strings.Cut(line, "\t")
		n, _ := strconv.Atoi(value)
		if previous, ok := seen[n]; ok {
			f.t.Fatalf("handler %s delivered seq %d twice (%s then %s): %v", name, n, previous, identity, f.deliveries(name))
		}
		seen[n] = identity
	}
}

func (f *fixture) checkpoint(identity string) (handlers.Checkpoint, error) {
	return handlers.ReadCheckpoint(&workspace.Workspace{Root: f.root()}, identity)
}

func (f *fixture) mustCheckpoint(identity string) handlers.Checkpoint {
	f.t.Helper()
	checkpoint, err := f.checkpoint(identity)
	if err != nil {
		f.t.Fatalf("checkpoint %s: %v", identity, err)
	}
	return checkpoint
}

func (f *fixture) cursorBytes(identity string) string {
	data, _ := os.ReadFile(filepath.Join(f.root(), ".cursors", "handlers", identity+".cursor"))
	return string(data)
}

func (f *fixture) declared() *workspace.Config {
	f.t.Helper()
	declared, err := workspace.LoadDeclaredRoot(f.project)
	if err != nil {
		f.t.Fatal(err)
	}
	return declared
}

func (f *fixture) assertLegacyActive() {
	f.t.Helper()
	declared := f.declared()
	if _, enabled := declared.Plugins.Values[fixturePlugin]; enabled {
		f.t.Fatal("plugin is enabled")
	}
	for _, name := range f.names {
		if _, ok := declared.Handlers[name]; !ok {
			f.t.Fatalf("legacy handler %s is not declared", name)
		}
	}
}

func (f *fixture) assertPluginActive() {
	f.t.Helper()
	declared := f.declared()
	if _, enabled := declared.Plugins.Values[fixturePlugin]; !enabled {
		f.t.Fatal("plugin is not enabled")
	}
	for _, name := range f.names {
		if _, ok := declared.Handlers[name]; ok {
			f.t.Fatalf("legacy handler %s is still declared", name)
		}
	}
}

func readJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestHandoffRoundTripDeliversEachMatchingEventOnce(t *testing.T) {
	f := newFixture(t)
	// Unequal real delivery progress: beta consumes event 1, the other
	// handlers consume 1..2, and event 3 remains pending. Never rewind an
	// acknowledged checkpoint to manufacture this fixture.
	f.appendMove("in-progress") // seq 1: alpha, beta, delta
	betaOnly := f.open()
	betaOnly.Config.Handlers = map[string]workspace.HandlerConfig{"beta": f.handlerConfig("beta")}
	if failures := handlers.DrainAll(betaOnly, handlers.Options{Scope: handlers.ScopeAll}); len(failures) != 0 {
		t.Fatal(failures)
	}
	f.appendMove("done") // seq 2: all four
	others := f.open()
	delete(others.Config.Handlers, "beta")
	if failures := handlers.DrainAll(others, handlers.Options{Scope: handlers.ScopeAll}); len(failures) != 0 {
		t.Fatal(failures)
	}
	f.appendCreated() // seq 3: alpha, delta pending
	before := map[string]string{}
	for _, name := range f.names {
		before[name] = f.cursorBytes(name)
	}
	beforeHash := f.configHash()

	result, err := f.forward(f.receiptDir("forward-1"))
	if err != nil {
		t.Fatalf("forward: %v (%+v)", err, result)
	}
	if summary, err := json.Marshal(result); err == nil {
		t.Logf("forward fixture summary: %s", summary)
	}
	if result.Status != pluginmgr.StatusCommitted || result.BeforeConfigHash != beforeHash || result.TargetConfigHash != f.configHash() {
		t.Fatalf("forward result = %+v", result)
	}
	if !result.PowerLossDurable {
		t.Fatalf("directory sync not reported on tmpfs/local fs: %+v", result.Publication)
	}
	f.assertPluginActive()
	// Contributed status stayed at its pinned position.
	if statuses := strings.Join(f.open().Config.Statuses, ","); statuses != "backlog,ready,in-progress,blocked,in-review,merge,done" {
		t.Fatalf("statuses = %s", statuses)
	}
	for _, name := range f.names {
		if f.cursorBytes(name) != before[name] {
			t.Fatalf("source %s cursor changed", name)
		}
		source := f.mustCheckpoint(name)
		destination := f.mustCheckpoint(fixturePlugin + "/" + name)
		if source.Position != destination.Position || source.PrefixHash != destination.PrefixHash {
			t.Fatalf("%s: source %+v destination %+v", name, source, destination)
		}
	}
	if len(result.Transfers) != 4 || result.Transfers[1].Source != "beta" || result.Transfers[1].Position != 1 || result.Transfers[1].ObservedEnd != 3 {
		t.Fatalf("transfers = %+v", result.Transfers)
	}
	var prepared map[string]any
	readJSON(t, filepath.Join(result.ReceiptDir, "prepared.json"), &prepared)
	if prepared["attempt_id"] != result.AttemptID {
		t.Fatalf("prepared receipt attempt = %v", prepared["attempt_id"])
	}
	if info, err := os.Stat(result.ReceiptDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("receipt dir mode = %v, %v", info.Mode(), err)
	}
	if info, err := os.Stat(filepath.Join(result.ReceiptDir, "config-before.yaml")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private snapshot mode = %v, %v", info.Mode(), err)
	}
	if _, err := os.Stat(filepath.Join(result.ReceiptDir, "committed.json")); err != nil {
		t.Fatal(err)
	}

	// After cutover: pending suffix reaches the plugin identities exactly once.
	f.appendMove("done") // seq 4
	if failures := f.drain(); len(failures) != 0 {
		t.Fatalf("plugin drain: %v", failures)
	}
	want := map[string][]int{"alpha": {1, 2, 3, 4}, "beta": {1, 2, 4}, "gamma": {2, 4}, "delta": {1, 2, 3, 4}}
	for name, expected := range want {
		f.assertNoDuplicates(name)
		if got := f.seqs(name); fmt.Sprint(got) != fmt.Sprint(expected) {
			t.Fatalf("%s delivered %v, want %v (%v)", name, got, expected, f.deliveries(name))
		}
	}
	for _, line := range f.deliveries("alpha")[3:] {
		if !strings.HasPrefix(line, fixturePlugin+"/alpha\t") {
			t.Fatalf("post-cutover delivery came from %q", line)
		}
	}
	// Legacy cursors are now frozen behind the plugin ones: config-only
	// rollback would replay seq 3/4 for alpha. Reverse handoff must not.
	if f.mustCheckpoint("alpha").Position != 2 || f.mustCheckpoint(fixturePlugin+"/alpha").Position != 4 {
		t.Fatalf("expected frozen legacy 2 and plugin 4, got %d/%d", f.mustCheckpoint("alpha").Position, f.mustCheckpoint(fixturePlugin+"/alpha").Position)
	}

	f.appendCreated() // seq 5 pending on plugin side before reverse
	pluginBytes := map[string]string{}
	for _, name := range f.names {
		pluginBytes[name] = f.cursorBytes(fixturePlugin + "/" + name)
	}
	reverse, err := f.reverse(f.receiptDir("reverse-1"))
	if err != nil {
		t.Fatalf("reverse: %v (%+v)", err, reverse)
	}
	if summary, err := json.Marshal(reverse); err == nil {
		t.Logf("reverse fixture summary: %s", summary)
	}
	if reverse.Status != pluginmgr.StatusCommitted {
		t.Fatalf("reverse result = %+v", reverse)
	}
	f.assertLegacyActive()
	if statuses := strings.Join(f.open().Config.Statuses, ","); statuses != "backlog,ready,in-progress,blocked,in-review,merge,done" {
		t.Fatalf("reverse statuses = %s", statuses)
	}
	for _, name := range f.names {
		if f.cursorBytes(fixturePlugin+"/"+name) != pluginBytes[name] {
			t.Fatalf("reverse changed source %s", name)
		}
		if f.mustCheckpoint(name).Position != 4 {
			t.Fatalf("legacy %s not restored to plugin position: %+v", name, f.mustCheckpoint(name))
		}
	}
	for _, transfer := range reverse.Transfers {
		if transfer.DestinationHadCheckpoint != true {
			t.Fatalf("reverse should record the frozen legacy checkpoint it overwrote: %+v", transfer)
		}
	}
	var reversePrepared struct {
		Destinations []struct {
			Identity string `json:"identity"`
			Raw      string `json:"raw"`
		} `json:"destinations"`
	}
	readJSON(t, filepath.Join(reverse.ReceiptDir, "prepared.json"), &reversePrepared)
	if len(reversePrepared.Destinations) != 4 || !strings.Contains(reversePrepared.Destinations[0].Raw, `"position":2`) {
		t.Fatalf("original destination bytes not preserved: %+v", reversePrepared.Destinations)
	}
	f.appendMove("done") // seq 6
	if failures := f.drain(); len(failures) != 0 {
		t.Fatalf("legacy drain after reverse: %v", failures)
	}
	want = map[string][]int{"alpha": {1, 2, 3, 4, 5, 6}, "beta": {1, 2, 4, 6}, "gamma": {2, 4, 6}, "delta": {1, 2, 3, 4, 5, 6}}
	for name, expected := range want {
		f.assertNoDuplicates(name)
		if got := f.seqs(name); fmt.Sprint(got) != fmt.Sprint(expected) {
			t.Fatalf("%s after round trip delivered %v, want %v", name, got, expected)
		}
	}

	// Second forward: destinations have older valid inactive checkpoints (4)
	// that are behind the now-active legacy sources (6); they are overwritten.
	second, err := f.forward(f.receiptDir("forward-2"))
	if err != nil || second.Status != pluginmgr.StatusCommitted {
		t.Fatalf("second forward = %+v, %v", second, err)
	}
	for _, name := range f.names {
		if f.mustCheckpoint(fixturePlugin+"/"+name).Position != 6 {
			t.Fatalf("second forward left %s at %d", name, f.mustCheckpoint(fixturePlugin+"/"+name).Position)
		}
	}
	// Retrying any earlier receipt is inspection-only and changes nothing.
	for _, receipt := range []string{"forward-1", "reverse-1", "forward-2"} {
		hash := f.configHash()
		inspect, err := f.forward(f.receiptDir(receipt))
		if err != nil || inspect.Status != pluginmgr.StatusAlreadyCommitted {
			t.Fatalf("inspect %s = %+v, %v", receipt, inspect, err)
		}
		if f.configHash() != hash {
			t.Fatalf("inspection of %s mutated config", receipt)
		}
		for _, name := range f.names {
			if f.mustCheckpoint(fixturePlugin+"/"+name).Position != 6 || f.mustCheckpoint(name).Position != 6 {
				t.Fatalf("inspection of %s rewound a cursor", receipt)
			}
		}
	}
	if inspect, _ := f.forward(f.receiptDir("forward-1")); !strings.Contains(strings.Join(inspect.Diagnosis, " "), "historical") {
		t.Fatalf("historical receipt not flagged: %+v", inspect.Diagnosis)
	}
}

func TestHandoffRejectsUnsafePreconditionsWithoutMutation(t *testing.T) {
	f := newFixture(t)
	f.appendMove("done")
	if failures := f.drain(); len(failures) != 0 {
		t.Fatal(failures)
	}
	snapshot := func() map[string]string {
		state := map[string]string{"config": f.configHash()}
		for _, name := range f.names {
			state[name] = f.cursorBytes(name)
			state[fixturePlugin+"/"+name] = f.cursorBytes(fixturePlugin + "/" + name)
		}
		return state
	}
	baseline := snapshot()
	expectRejected := func(name string, result pluginmgr.HandoffResult, err error, fragment string) {
		t.Helper()
		if !errors.Is(err, pluginmgr.ErrHandoffRejected) || result.Status != pluginmgr.StatusRejected {
			t.Fatalf("%s: status %s err %v", name, result.Status, err)
		}
		if !strings.Contains(strings.Join(result.Diagnosis, " "), fragment) {
			t.Fatalf("%s: diagnosis %v lacks %q", name, result.Diagnosis, fragment)
		}
		if fmt.Sprint(snapshot()) != fmt.Sprint(baseline) {
			t.Fatalf("%s mutated state", name)
		}
		if result.ReceiptDir != "" {
			if _, err := os.Stat(result.ReceiptDir); err == nil {
				t.Fatalf("%s created receipt dir %s", name, result.ReceiptDir)
			}
		}
	}

	result, err := pluginmgr.Handoff(pluginmgr.HandoffRequest{WorkspacePath: f.project, Plugin: fixturePlugin, Direction: pluginmgr.Reverse, LegacyTemplate: f.templateBytes(), ExpectConfigHash: f.configHash(), ReceiptDir: f.receiptDir("r"), EngineVersion: "dev"})
	expectRejected("reverse while legacy active", result, err, "not enabled")

	result, err = pluginmgr.Handoff(pluginmgr.HandoffRequest{WorkspacePath: f.project, Plugin: fixturePlugin, Direction: pluginmgr.Forward, ExpectConfigHash: strings.Repeat("0", 64), ReceiptDir: f.receiptDir("r"), EngineVersion: "dev"})
	expectRejected("wrong expected hash", result, err, "does not match expected")

	result, err = pluginmgr.Handoff(pluginmgr.HandoffRequest{WorkspacePath: f.project, Plugin: "absent", Direction: pluginmgr.Forward, EngineVersion: "dev"})
	expectRejected("unknown plugin", result, err, "not installed")

	result, err = pluginmgr.Handoff(pluginmgr.HandoffRequest{WorkspacePath: f.project, Plugin: fixturePlugin, Direction: pluginmgr.Forward, LegacyTemplate: []byte("x"), EngineVersion: "dev"})
	expectRejected("template on forward", result, err, "reverse-only")

	t.Setenv("DOCKET_HANDLER_STACK", "alpha")
	result, err = f.forward(f.receiptDir("nested"))
	expectRejected("nested", result, err, "inside a handler")
	os.Unsetenv("DOCKET_HANDLER_STACK")

	result, err = pluginmgr.Handoff(pluginmgr.HandoffRequest{WorkspacePath: f.project, Plugin: fixturePlugin, Direction: pluginmgr.Forward, ReceiptDir: f.root(), EngineVersion: "dev"})
	if err == nil || result.Status == pluginmgr.StatusCommitted {
		t.Fatalf("receipt dir at workspace root accepted: %+v", result)
	}
	result, err = pluginmgr.Handoff(pluginmgr.HandoffRequest{WorkspacePath: f.project, Plugin: fixturePlugin, Direction: pluginmgr.Forward, ReceiptDir: filepath.Join(f.root(), "tasks", "x"), EngineVersion: "dev"})
	expectRejected("receipt inside tasks", result, err, "task or project data")

	// Missing source checkpoint: a legacy handler that never drained.
	if err := os.Remove(filepath.Join(f.root(), ".cursors", "handlers", "gamma.cursor")); err != nil {
		t.Fatal(err)
	}
	result, err = f.forward(f.receiptDir("missing-source"))
	if !errors.Is(err, pluginmgr.ErrHandoffRejected) || !strings.Contains(err.Error(), `source "gamma" has no checkpoint`) {
		t.Fatalf("missing source = %v", err)
	}
	if _, err := f.checkpoint(fixturePlugin + "/alpha"); !errors.Is(err, handlers.ErrCheckpointMissing) {
		t.Fatal("rejected attempt prepared a destination")
	}
	if _, err := os.Stat(f.receiptDir("missing-source")); err == nil {
		t.Fatal("rejected attempt created a receipt directory")
	}
	// Corrupt source hash.
	if err := store.WriteAtomic(filepath.Join(f.root(), ".cursors", "handlers", "gamma.cursor"), []byte(`{"position":1,"prefix_hash":"`+strings.Repeat("0", 64)+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err = f.forward(f.receiptDir("corrupt-source"))
	if err == nil || !strings.Contains(err.Error(), "prefix hash does not match") {
		t.Fatalf("corrupt source = %v", err)
	}
	// Negative and plain-integer sources.
	for name, body := range map[string]string{"negative": `{"position":-1,"prefix_hash":""}`, "plain": "1\n"} {
		if err := store.WriteAtomic(filepath.Join(f.root(), ".cursors", "handlers", "gamma.cursor"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := f.forward(f.receiptDir(name)); !errors.Is(err, pluginmgr.ErrHandoffRejected) {
			t.Fatalf("%s source accepted: %v", name, err)
		}
	}
	// Restore gamma and corrupt an inactive destination instead.
	hash1, _, _ := events.PrefixHash(f.open(), 1)
	if err := store.WriteAtomic(filepath.Join(f.root(), ".cursors", "handlers", "gamma.cursor"), []byte(`{"position":1,"prefix_hash":"`+hash1+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteAtomic(filepath.Join(f.root(), ".cursors", "handlers", fixturePlugin, "beta.cursor"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.forward(f.receiptDir("corrupt-destination")); err == nil || !strings.Contains(err.Error(), "corrupt inactive checkpoint") {
		t.Fatalf("corrupt destination = %v", err)
	}
	// Destination ahead of source.
	f.appendMove("done")
	hash2, _, _ := events.PrefixHash(f.open(), 2)
	if err := store.WriteAtomic(filepath.Join(f.root(), ".cursors", "handlers", fixturePlugin, "beta.cursor"), []byte(`{"position":2,"prefix_hash":"`+hash2+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.forward(f.receiptDir("ahead")); err == nil || !strings.Contains(err.Error(), "is ahead of source") {
		t.Fatalf("ahead destination = %v", err)
	}
	if err := os.Remove(filepath.Join(f.root(), ".cursors", "handlers", fixturePlugin, "beta.cursor")); err != nil {
		t.Fatal(err)
	}
	// Legacy declaration missing.
	if err := workspace.MutateDeclaredConfig(f.root(), func(config *workspace.Config) error {
		delete(config.Handlers, "delta")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.forward(f.receiptDir("missing-legacy")); err == nil || !strings.Contains(err.Error(), `legacy handler "delta" is not declared`) {
		t.Fatalf("missing legacy = %v", err)
	}
	// Legacy declaration with a different filter.
	if err := workspace.MutateDeclaredConfig(f.root(), func(config *workspace.Config) error {
		config.Handlers["delta"] = workspace.HandlerConfig{On: []string{events.TaskMoved}, Run: "hooks/delta"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.forward(f.receiptDir("drift")); err == nil || !strings.Contains(err.Error(), "event filters differ") {
		t.Fatalf("filter drift = %v", err)
	}
}

func TestReverseHandoffValidatesTemplateStrictly(t *testing.T) {
	f := newFixture(t)
	f.appendMove("done")
	if failures := f.drain(); len(failures) != 0 {
		t.Fatal(failures)
	}
	if _, err := f.forward(""); err != nil {
		t.Fatal(err)
	}
	f.assertPluginActive()
	attempt := func(template string) error {
		_, err := pluginmgr.Handoff(pluginmgr.HandoffRequest{
			WorkspacePath: f.project, Plugin: fixturePlugin, Direction: pluginmgr.Reverse,
			LegacyTemplate: []byte(template), ExpectConfigHash: f.configHash(), ReceiptDir: filepath.Join(f.home, "r-"+strconv.FormatInt(time.Now().UnixNano(), 10)), EngineVersion: "dev",
		})
		return err
	}
	full := string(f.templateBytes())
	cases := map[string]string{
		"no handlers":       "statuses: [a]\n",
		"missing handler":   strings.Replace(full, "delta:", "omega:", 1),
		"extra handler":     full + "  extra: {on: ['*'], run: hooks/extra}\n",
		"absolute path":     strings.Replace(full, "run: hooks/alpha", "run: /bin/sh", 1),
		"traversal":         strings.Replace(full, "run: hooks/alpha", "run: ../outside", 1),
		"lua runtime":       strings.Replace(full, "run: hooks/alpha", "lua: hooks/alpha", 1),
		"delivery mismatch": strings.Replace(full, "delivery: service", "delivery: inline", 1),
		"missing script":    strings.Replace(full, "run: hooks/alpha", "run: hooks/absent", 1),
		"yaml garbage":      "handlers: [",
	}
	for name, template := range cases {
		err := attempt(template)
		if !errors.Is(err, pluginmgr.ErrHandoffRejected) {
			t.Errorf("%s: err = %v", name, err)
		}
		f.assertPluginActive()
	}
	// Missing --expect-config-sha256 / receipt dir / template are usage errors.
	if _, err := pluginmgr.Handoff(pluginmgr.HandoffRequest{WorkspacePath: f.project, Plugin: fixturePlugin, Direction: pluginmgr.Reverse, LegacyTemplate: f.templateBytes(), ReceiptDir: f.receiptDir("x"), EngineVersion: "dev"}); err == nil || !strings.Contains(err.Error(), "expect-config-sha256") {
		t.Fatalf("missing hash = %v", err)
	}
	if _, err := pluginmgr.Handoff(pluginmgr.HandoffRequest{WorkspacePath: f.project, Plugin: fixturePlugin, Direction: pluginmgr.Reverse, LegacyTemplate: f.templateBytes(), ExpectConfigHash: f.configHash(), EngineVersion: "dev"}); err == nil || !strings.Contains(err.Error(), "receipt-dir") {
		t.Fatalf("missing receipt dir = %v", err)
	}
	// Template labels/statuses/plugin settings must not leak in, and unrelated
	// workspace configuration must survive the reverse.
	if err := workspace.MutateDeclaredConfig(f.root(), func(config *workspace.Config) error {
		config.Labels = []string{"keep-me"}
		config.Handlers = map[string]workspace.HandlerConfig{"unrelated": {On: []string{"*"}, Run: "hooks/alpha"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var extra map[string]any
	if err := yaml.Unmarshal([]byte(full), &extra); err != nil {
		t.Fatal(err)
	}
	extra["labels"] = []string{"injected"}
	extra["statuses"] = []string{"only"}
	extra["plugins"] = map[string]any{"other": map[string]any{}}
	extraBytes, err := yaml.Marshal(extra)
	if err != nil {
		t.Fatal(err)
	}
	if err := attempt(string(extraBytes)); err != nil {
		t.Fatalf("template with extra sections: %v", err)
	}
	declared := f.declared()
	if fmt.Sprint(declared.Labels) != "[keep-me]" || declared.Plugins.Order != nil {
		t.Fatalf("template leaked into config: labels %v plugins %v", declared.Labels, declared.Plugins.Order)
	}
	if _, ok := declared.Handlers["unrelated"]; !ok {
		t.Fatal("unrelated handler removed")
	}
	if strings.Join(declared.Statuses, ",") != "backlog,ready,in-progress,blocked,in-review,merge,done" {
		t.Fatalf("statuses after reverse = %v", declared.Statuses)
	}
}

func TestExistingReceiptIsInspectionOnlyAndClassifiesState(t *testing.T) {
	f := newFixture(t)
	f.appendMove("done")
	if failures := f.drain(); len(failures) != 0 {
		t.Fatal(failures)
	}
	// Simulate a crash after the prepared receipt and destination writes but
	// before publication, using the in-process hook.
	stop := errors.New("stop")
	pluginmgr.SetCrashHook(func(stage string) {
		if stage == "before_publish" {
			panic(stop)
		}
	})
	func() {
		defer func() {
			if recovered := recover(); recovered != stop {
				t.Fatalf("unexpected panic %v", recovered)
			}
		}()
		f.forward(f.receiptDir("interrupted"))
	}()
	pluginmgr.SetCrashHook(nil)
	f.assertLegacyActive()
	if f.mustCheckpoint(fixturePlugin+"/alpha").Position != 1 {
		t.Fatal("destination was not prepared before the crash point")
	}
	inspect, err := f.forward(f.receiptDir("interrupted"))
	if err != nil || inspect.Status != pluginmgr.StatusSourceActive {
		t.Fatalf("inspect prepared-only = %+v, %v", inspect, err)
	}
	f.assertLegacyActive()

	// A fresh attempt with a new directory completes; the inert destination
	// checkpoint (1) is validated and overwritten by the fresh capture.
	f.appendMove("done")
	if failures := f.drain(); len(failures) != 0 {
		t.Fatal(failures)
	}
	fresh, err := f.forward(f.receiptDir("fresh"))
	if err != nil || fresh.Status != pluginmgr.StatusCommitted {
		t.Fatalf("fresh = %+v, %v", fresh, err)
	}
	if f.mustCheckpoint(fixturePlugin+"/alpha").Position != 2 || !fresh.Transfers[0].DestinationHadCheckpoint {
		t.Fatalf("fresh attempt did not recapture: %+v", fresh.Transfers[0])
	}
	// Config bytes can match after a fresh attempt with newer checkpoints;
	// inspection describes current wiring, never reapplies the old capture.
	inspect, err = f.forward(f.receiptDir("interrupted"))
	if err != nil || inspect.Status != pluginmgr.StatusTargetActive {
		t.Fatalf("stale prepared receipt = %+v, %v", inspect, err)
	}
	f.assertPluginActive()

	// Corrupt receipts and receipts from another workspace are diagnosed.
	if err := os.WriteFile(filepath.Join(f.receiptDir("interrupted"), "prepared.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspect, err = f.forward(f.receiptDir("interrupted"))
	if err == nil || inspect.Status != pluginmgr.StatusNeedsInspection {
		t.Fatalf("corrupt receipt = %+v, %v", inspect, err)
	}
	if err := os.MkdirAll(f.receiptDir("empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if inspect, err := f.forward(f.receiptDir("empty")); err == nil || inspect.Status != pluginmgr.StatusNeedsInspection {
		t.Fatalf("empty receipt dir = %+v, %v", inspect, err)
	}
	other := f.receiptDir("other-plugin")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(f.receiptDir("fresh"), "prepared.json"))
	if err := os.WriteFile(filepath.Join(other, "prepared.json"), []byte(strings.Replace(string(data), `"plugin": "stub"`, `"plugin": "else"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if inspect, err := f.forward(other); err == nil || !strings.Contains(strings.Join(inspect.Diagnosis, " "), "does not belong") && !strings.Contains(strings.Join(inspect.Diagnosis, " "), "not") {
		t.Fatalf("foreign receipt = %+v, %v", inspect, err)
	}
	f.assertPluginActive()
}

func TestHandoffWaitsForInFlightSourceAndBlocksConcurrentTransition(t *testing.T) {
	f := newFixture(t)
	started := filepath.Join(f.home, "alpha-started")
	release := filepath.Join(f.home, "alpha-release")
	f.writeScript(f.project, "alpha", "touch "+shellQuote(started)+"\nwhile [ ! -f "+shellQuote(release)+" ]; do sleep 0.01; done\n")
	f.appendMove("done")
	drainDone := make(chan []handlers.Failure, 1)
	go func() { drainDone <- f.drain() }()
	waitFile(t, started)

	forwardDone := make(chan error, 1)
	go func() { _, err := f.forward(f.receiptDir("a")); forwardDone <- err }()
	select {
	case err := <-forwardDone:
		t.Fatalf("handoff completed while a source handler was executing: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	// A second concurrent transition is also queued behind the same locks.
	secondDone := make(chan pluginmgr.HandoffResult, 1)
	go func() { result, _ := f.forward(f.receiptDir("b")); secondDone <- result }()
	if err := os.WriteFile(release, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if failures := <-drainDone; len(failures) != 0 {
		t.Fatalf("drain: %v", failures)
	}
	first := <-forwardDone
	second := <-secondDone
	committed := 0
	if first == nil {
		committed++
	}
	if second.Status == pluginmgr.StatusCommitted {
		committed++
	}
	if committed != 1 {
		t.Fatalf("exactly one of two concurrent transitions must commit: first=%v second=%+v", first, second)
	}
	f.assertPluginActive()
	// The in-flight legacy delivery finished and was acknowledged before the
	// capture: the plugin identity starts after it.
	if f.mustCheckpoint(fixturePlugin+"/alpha").Position != 1 {
		t.Fatalf("plugin/alpha = %+v", f.mustCheckpoint(fixturePlugin+"/alpha"))
	}
	f.appendMove("done")
	if failures := f.drain(); len(failures) != 0 {
		t.Fatal(failures)
	}
	f.assertNoDuplicates("alpha")
	if got := f.seqs("alpha"); fmt.Sprint(got) != "[1 2]" {
		t.Fatalf("alpha = %v", got)
	}
}

func TestQueuedDrainAndConcurrentMutationsDuringHandoff(t *testing.T) {
	f := newFixture(t)
	f.appendMove("done")
	if failures := f.drain(); len(failures) != 0 {
		t.Fatal(failures)
	}
	// Hold a foreign lock inside the handoff so we can interleave: the crash
	// hook is used as a barrier rather than a crash.
	entered := make(chan struct{})
	proceed := make(chan struct{})
	pluginmgr.SetCrashHook(func(stage string) {
		if stage == "before_publish" {
			close(entered)
			<-proceed
		}
	})
	defer pluginmgr.SetCrashHook(nil)
	result := make(chan pluginmgr.HandoffResult, 1)
	go func() { r, _ := f.forward(f.receiptDir("barrier")); result <- r }()
	<-entered
	// A drain queued now must wait; a task mutation (event append) and an
	// unrelated config write must not deadlock or be lost.
	drainDone := make(chan []handlers.Failure, 1)
	go func() { drainDone <- f.drain() }()
	f.appendMove("done") // seq 2 while locks are held: becomes pending suffix
	var wg sync.WaitGroup
	wg.Add(1)
	configDone := make(chan error, 1)
	go func() {
		defer wg.Done()
		configDone <- workspace.MutateDeclaredConfig(f.root(), func(config *workspace.Config) error {
			config.Labels = append(config.Labels, "during")
			return nil
		})
	}()
	select {
	case <-drainDone:
		t.Fatal("queued drain ran while handoff held the handler locks")
	case err := <-configDone:
		t.Fatalf("config mutation bypassed the config lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(proceed)
	r := <-result
	if r.Status != pluginmgr.StatusCommitted {
		t.Fatalf("handoff = %+v", r)
	}
	wg.Wait()
	if err := <-configDone; err != nil {
		t.Fatal(err)
	}
	if failures := <-drainDone; len(failures) != 0 {
		t.Fatal(failures)
	}
	// A stale drain skips removed identities. A fresh destination drain (the
	// service's config watcher supplies this) delivers the pending suffix.
	if failures := f.drain(); len(failures) != 0 {
		t.Fatal(failures)
	}
	f.assertNoDuplicates("alpha")
	lines := f.deliveries("alpha")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], fixturePlugin+"/alpha\t2") {
		t.Fatalf("alpha deliveries = %v", lines)
	}
	if labels := f.declared().Labels; !strings.Contains(fmt.Sprint(labels), "during") {
		t.Fatalf("concurrent label mutation lost: %v", labels)
	}
	f.assertPluginActive()
}

func TestHandoffTransfersFailedBatchPendingAndExposesPartialEffectLimit(t *testing.T) {
	f := newFixture(t)
	// beta fails after recording its side effect (partial effect before the
	// checkpoint). gamma fails before any effect.
	f.writeScript(f.project, "beta", "exit 3\n")
	f.writeScript(f.project, "gamma", "")
	if err := os.WriteFile(filepath.Join(f.project, "hooks", "gamma"), []byte("#!/bin/sh\nexit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Establish zero checkpoints before any delivery, as with a prior
	// --from-start enable. Never repair a failed batch by editing its cursor.
	for _, name := range []string{"beta", "gamma"} {
		if err := store.WithLock(handlers.LockPath(&workspace.Workspace{Root: f.root()}, name), func() error {
			return handlers.ResetCursor(&workspace.Workspace{Root: f.root()}, name)
		}); err != nil {
			t.Fatal(err)
		}
	}
	f.appendMove("done") // seq 1
	failures := f.drain()
	if len(failures) != 2 {
		t.Fatalf("expected beta and gamma to fail: %v", failures)
	}
	if f.mustCheckpoint("beta").Position != 0 || f.mustCheckpoint("gamma").Position != 0 {
		t.Fatal("failed batch advanced its checkpoint")
	}
	result, err := f.forward(f.receiptDir("with-failures"))
	if err != nil || result.Status != pluginmgr.StatusCommitted {
		t.Fatalf("forward = %+v, %v", result, err)
	}
	var prepared struct {
		Ledger struct {
			Pending map[string][2]int `json:"pending"`
		} `json:"ledger"`
	}
	readJSON(t, filepath.Join(result.ReceiptDir, "prepared.json"), &prepared)
	if prepared.Ledger.Pending[fixturePlugin+"/beta"] != [2]int{0, 1} || prepared.Ledger.Pending[fixturePlugin+"/alpha"] != [2]int{1, 1} {
		t.Fatalf("pending ranges = %v", prepared.Ledger.Pending)
	}
	if failures := f.drain(); len(failures) != 0 {
		t.Fatalf("plugin drain: %v", failures)
	}
	f.assertNoDuplicates("alpha")
	f.assertNoDuplicates("delta")
	// gamma failed before effects: exactly one delivery after handoff.
	if got := f.deliveries("gamma"); len(got) != 1 || !strings.HasPrefix(got[0], fixturePlugin+"/gamma\t1") {
		t.Fatalf("gamma = %v", got)
	}
	// beta had a partial effect before failing: the normal at-least-once retry
	// duplicates it. This is the documented limit, not handoff-induced replay.
	if got := f.deliveries("beta"); len(got) != 2 || got[0] != "beta\t1" || got[1] != fixturePlugin+"/beta\t1" {
		t.Fatalf("beta = %v (expected the at-least-once duplicate)", got)
	}
}

// TestHandoffCrashAtEveryBoundary re-executes the test binary as a subprocess
// that terminates at one commit boundary, then classifies the state with the
// receipt and completes or continues with a fresh attempt. Only fixture
// processes are killed.
func TestHandoffCrashAtEveryBoundary(t *testing.T) {
	stages := []string{
		"before_prepared", "after_prepared",
		"before_destination:" + fixturePlugin + "/alpha", "after_destination:" + fixturePlugin + "/alpha",
		"before_destination:" + fixturePlugin + "/delta", "after_destination:" + fixturePlugin + "/delta",
		"before_destination:" + fixturePlugin + "/beta", "after_destination:" + fixturePlugin + "/beta",
		"before_destination:" + fixturePlugin + "/gamma", "after_destination:" + fixturePlugin + "/gamma",
		"before_publish", "config:before_rename", "config:after_rename", "config:before_directory_sync", "config:after_directory_sync",
		"after_publish", "before_committed", "after_committed",
	}
	for _, direction := range []pluginmgr.Direction{pluginmgr.Forward, pluginmgr.Reverse} {
		for _, originalStage := range stages {
			stage := originalStage
			if direction == pluginmgr.Reverse {
				stage = strings.ReplaceAll(stage, fixturePlugin+"/", "")
			}
			t.Run(string(direction)+"/"+stage, func(t *testing.T) {
				f := newFixture(t)
				f.appendMove("done")
				if failures := f.drain(); len(failures) != 0 {
					t.Fatal(failures)
				}
				if direction == pluginmgr.Reverse {
					if _, err := f.forward(f.receiptDir("initial")); err != nil {
						t.Fatal(err)
					}
				}
				attempt := f.forward
				assertSource, assertTarget := f.assertLegacyActive, f.assertPluginActive
				if direction == pluginmgr.Reverse {
					attempt = f.reverse
					assertSource, assertTarget = f.assertPluginActive, f.assertLegacyActive
				}
				f.appendCreated() // pending seq 2
				beforeHash := f.configHash()
				receipt := f.receiptDir("crash")
				cmd := exec.Command(os.Args[0], "-test.run=TestHandoffCrashHelperProcess$", "-test.v")
				cmd.Env = append(os.Environ(),
					"DOCKET_TEST_CRASH_HELPER=1", "DOCKET_TEST_CRASH_STAGE="+stage,
					"DOCKET_TEST_PROJECT="+f.project, "DOCKET_TEST_RECEIPT="+receipt,
					"DOCKET_TEST_DIRECTION="+string(direction), "DOCKET_TEST_TEMPLATE="+f.template, "DOCKET_TEST_CONFIG_HASH="+beforeHash,
				)
				output, err := cmd.CombinedOutput()
				if err == nil {
					t.Fatalf("helper did not crash at %s:\n%s", stage, output)
				}
				if !strings.Contains(string(output), "CRASH:"+stage) {
					t.Fatalf("helper output lacks crash marker:\n%s", output)
				}
				published := f.configHash() != beforeHash
				_, receiptExists := os.Stat(filepath.Join(receipt, "prepared.json"))
				_, committedExists := os.Stat(filepath.Join(receipt, "committed.json"))
				switch {
				case strings.HasPrefix(stage, "before_prepared"):
					if published || receiptExists == nil {
						t.Fatalf("state after %s: published=%v receipt=%v", stage, published, receiptExists)
					}
				case stage == "after_prepared" || strings.HasPrefix(stage, "before_destination") || strings.HasPrefix(stage, "after_destination") || stage == "before_publish" || stage == "config:before_rename":
					if published || receiptExists != nil || committedExists == nil {
						t.Fatalf("state after %s: published=%v receipt=%v committed=%v", stage, published, receiptExists, committedExists)
					}
					assertSource()
					inspect, err := f.forward(receipt)
					if err != nil || inspect.Status != pluginmgr.StatusSourceActive {
						t.Fatalf("inspect after %s = %+v, %v", stage, inspect, err)
					}
				case stage == "after_publish" || stage == "before_committed" || stage == "config:after_rename" || stage == "config:before_directory_sync" || stage == "config:after_directory_sync":
					if !published || committedExists == nil {
						t.Fatalf("state after %s: published=%v committed=%v", stage, published, committedExists)
					}
					assertTarget()
					inspect, err := f.forward(receipt)
					if err != nil || inspect.Status != pluginmgr.StatusTargetActive {
						t.Fatalf("inspect after %s = %+v, %v", stage, inspect, err)
					}
				case stage == "after_committed":
					if !published || committedExists != nil {
						t.Fatalf("state after %s: published=%v committed=%v", stage, published, committedExists)
					}
					inspect, err := f.forward(receipt)
					if err != nil || inspect.Status != pluginmgr.StatusAlreadyCommitted {
						t.Fatalf("inspect after %s = %+v, %v", stage, inspect, err)
					}
				}
				// Whatever the boundary, the source cursors were never modified and
				// a drain after restart delivers pending seq 2 exactly once to
				// whichever set the config selects.
				if f.mustCheckpoint("alpha").Position != 1 {
					t.Fatalf("source alpha modified: %+v", f.mustCheckpoint("alpha"))
				}
				if !published {
					fresh, err := attempt(f.receiptDir("fresh"))
					if err != nil || fresh.Status != pluginmgr.StatusCommitted {
						t.Fatalf("fresh attempt after %s = %+v, %v", stage, fresh, err)
					}
				}
				if failures := f.drain(); len(failures) != 0 {
					t.Fatal(failures)
				}
				for _, name := range []string{"alpha", "delta"} {
					f.assertNoDuplicates(name)
					if got := f.seqs(name); fmt.Sprint(got) != "[1 2]" {
						t.Fatalf("%s after %s = %v", name, stage, got)
					}
				}
				assertTarget()
			})
		}
	}
}

func TestHandoffCrashHelperProcess(t *testing.T) {
	if os.Getenv("DOCKET_TEST_CRASH_HELPER") != "1" {
		return
	}
	stage := os.Getenv("DOCKET_TEST_CRASH_STAGE")
	pluginmgr.SetCrashHook(func(current string) {
		if current == stage {
			fmt.Println("CRASH:" + stage)
			os.Stdout.Sync()
			os.Exit(7)
		}
	})
	direction := pluginmgr.Direction(os.Getenv("DOCKET_TEST_DIRECTION"))
	var template []byte
	if direction == pluginmgr.Reverse {
		var err error
		template, err = os.ReadFile(os.Getenv("DOCKET_TEST_TEMPLATE"))
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := pluginmgr.Handoff(pluginmgr.HandoffRequest{WorkspacePath: os.Getenv("DOCKET_TEST_PROJECT"), Plugin: fixturePlugin, Direction: direction, LegacyTemplate: template, ExpectConfigHash: os.Getenv("DOCKET_TEST_CONFIG_HASH"), ReceiptDir: os.Getenv("DOCKET_TEST_RECEIPT"), EngineVersion: "dev"})
	fmt.Println("COMPLETED:", err)
}

func TestHandoffCancellationBeforeLocksChangesNothing(t *testing.T) {
	f := newFixture(t)
	f.appendMove("done")
	if failures := f.drain(); len(failures) != 0 {
		t.Fatal(failures)
	}
	ws := &workspace.Workspace{Root: f.root()}
	locked := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = store.WithLock(handlers.LockPath(ws, "alpha"), func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, err := pluginmgr.Handoff(pluginmgr.HandoffRequest{Context: ctx, WorkspacePath: f.project, Plugin: fixturePlugin, Direction: pluginmgr.Forward, ReceiptDir: f.receiptDir("cancelled"), EngineVersion: "dev"})
	close(release)
	if err == nil || result.Status == pluginmgr.StatusCommitted {
		t.Fatalf("cancelled handoff = %+v, %v", result, err)
	}
	f.assertLegacyActive()
	if _, err := os.Stat(f.receiptDir("cancelled")); err == nil {
		t.Fatal("cancelled attempt created a receipt directory")
	}
	if _, err := f.checkpoint(fixturePlugin + "/alpha"); !errors.Is(err, handlers.ErrCheckpointMissing) {
		t.Fatal("cancelled attempt prepared a destination")
	}
}

func TestEnableWithoutAdoptionSeedsUnderHandlerLocks(t *testing.T) {
	f := newFixture(t)
	// Remove legacy handlers so plain enable is valid alongside the plugin.
	if err := workspace.MutateDeclaredConfig(f.root(), func(config *workspace.Config) error {
		config.Handlers = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.appendMove("done")
	ws := &workspace.Workspace{Root: f.root()}
	locked := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = store.WithLock(handlers.LockPath(ws, fixturePlugin+"/alpha"), func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	done := make(chan error, 1)
	go func() { done <- pluginmgr.Enable(f.project, fixturePlugin, nil, false, false, "dev") }()
	select {
	case err := <-done:
		t.Fatalf("enable seeded while a plugin identity lock was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if f.mustCheckpoint(fixturePlugin+"/alpha").Position != 1 {
		t.Fatalf("seed = %+v", f.mustCheckpoint(fixturePlugin+"/alpha"))
	}
	// Repeating forward adoption against an active target is refused.
	if _, err := f.forward(f.receiptDir("again")); !errors.Is(err, pluginmgr.ErrHandoffRejected) || !strings.Contains(err.Error(), "already enabled") {
		t.Fatalf("repeat adoption = %v", err)
	}
}
