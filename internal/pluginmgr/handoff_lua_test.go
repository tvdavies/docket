package pluginmgr_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/luahook"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestLuaPartialBatchRemainsAtLeastOnceAcrossHandoff(t *testing.T) {
	f := newFixture(t)
	manifestPath := filepath.Join(f.pluginRoot, plugin.ManifestFile)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "run: hooks/gamma", "lua: hooks/gamma.lua", 1))
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := workspace.MutateDeclaredConfig(f.root(), func(c *workspace.Config) error {
		h := c.Handlers["gamma"]
		h.Run = ""
		h.Lua = "hooks/gamma.lua"
		c.Handlers["gamma"] = h
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(f.counters, "gamma")
	flag := filepath.Join(f.home, "fail")
	body := fmt.Sprintf(`function handle(event, docket)
 local f = assert(io.open(%q, "a"))
 f:write(os.getenv("DOCKET_HANDLER") .. "\t" .. event.seq .. "\n")
 f:close()
 local fail = io.open(%q, "r")
 if fail then fail:close(); if event.seq == 2 then error("fixture partial batch") end end
end
`, counter, flag)
	for _, root := range []string{f.project, f.pluginRoot} {
		if err := os.WriteFile(filepath.Join(root, "hooks", "gamma.lua"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(flag, []byte("fail"), 0o600); err != nil {
		t.Fatal(err)
	}
	// An explicit zero is established before delivery (fixture setup, not a
	// recovery edit in response to a failed batch).
	if err := handlers.ResetCursor(f.open(), "gamma"); err != nil {
		t.Fatal(err)
	}
	f.appendMove("done")
	f.appendMove("done")
	t.Setenv("DOCKET_TEST_HANDOFF_LUA", "1")
	options := handlers.Options{Scope: handlers.ScopeAll, RefreshConfig: true, LuaCommand: []string{os.Args[0], "-test.run=^TestHandoffLuaHelper$", "--"}}
	failures := handlers.DrainAll(f.open(), options)
	if len(failures) != 1 || f.mustCheckpoint("gamma").Position != 0 {
		t.Fatalf("partial batch: %v", failures)
	}
	if _, err := f.forward(f.receiptDir("lua")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	if failures := handlers.DrainAll(f.open(), options); len(failures) != 0 {
		t.Fatal(failures)
	}
	if got := f.seqs("gamma"); fmt.Sprint(got) != "[1 1 2 2]" {
		t.Fatalf("expected ordinary at-least-once duplicates, got %v", got)
	}
	for _, name := range []string{"alpha", "beta", "delta"} {
		f.assertNoDuplicates(name)
	}
}

func TestHandoffLuaHelper(t *testing.T) {
	if os.Getenv("DOCKET_TEST_HANDOFF_LUA") != "1" {
		return
	}
	ws, err := workspace.Open()
	if err != nil {
		t.Fatal(err)
	}
	script := os.Args[len(os.Args)-1]
	if !strings.HasPrefix(script, os.Getenv("HOME")+string(filepath.Separator)) {
		t.Fatal("Lua script escaped fixture")
	}
	if err := luahook.Run(luahook.Options{Workspace: ws, Script: script, Input: os.Stdin, Output: os.Stdout, Error: os.Stderr}); err != nil {
		t.Fatal(err)
	}
}
