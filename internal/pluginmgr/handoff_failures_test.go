package pluginmgr_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tvdavies/docket/internal/pluginmgr"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/store"
)

func TestHandoffPublicationAndReceiptFailuresRemainClassified(t *testing.T) {
	for _, kind := range []string{"prepared", "destination", "config-rename", "directory-sync", "committed"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			f.appendMove("done")
			if failures := f.drain(); len(failures) != 0 {
				t.Fatal(failures)
			}
			receipt := f.receiptDir("failure")
			original := f.configHash()
			moved := filepath.Join(f.project, "moved-workspace")
			pluginmgr.SetCrashHook(func(stage string) {
				var err error
				switch {
				case kind == "prepared" && stage == "before_prepared":
					err = os.Mkdir(filepath.Join(receipt, "prepared.json"), 0o700)
				case kind == "destination" && stage == "before_destination:stub/beta":
					err = os.Mkdir(filepath.Join(f.open().HandlerStateDir(), "stub", "beta.cursor"), 0o700)
				case kind == "config-rename" && stage == "config:before_rename":
					files, e := filepath.Glob(filepath.Join(f.root(), ".tmp-config.yaml-*"))
					if e != nil {
						t.Fatal(e)
					}
					for _, file := range files {
						if e := os.Remove(file); e != nil {
							t.Fatal(e)
						}
					}
				case kind == "directory-sync" && stage == "config:before_directory_sync":
					err = os.Rename(f.root(), moved)
				case kind == "committed" && stage == "before_committed":
					err = os.WriteFile(filepath.Join(receipt, "committed.json"), []byte("incomplete"), 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
			})
			defer pluginmgr.SetCrashHook(nil)
			result, err := f.forward(receipt)
			if err == nil {
				t.Fatalf("%s failure was hidden: %+v", kind, result)
			}
			switch kind {
			case "prepared":
				if result.Status != pluginmgr.StatusRejected {
					t.Fatal(result)
				}
			case "destination", "config-rename":
				if result.Status != pluginmgr.StatusSourceActive {
					t.Fatal(result)
				}
			case "directory-sync":
				if result.Status != pluginmgr.StatusNeedsInspection || result.PowerLossDurable || result.Publication == nil || !result.Publication.Report.Renamed {
					t.Fatal(result)
				}
				if _, err := os.Stat(filepath.Join(moved, "config.yaml")); err != nil {
					t.Fatal(err)
				}
				return
			case "committed":
				if result.Status != pluginmgr.StatusTargetActive {
					t.Fatal(result)
				}
				f.assertPluginActive()
				return
			}
			if f.configHash() != original {
				t.Fatal("pre-publication failure changed config")
			}
			f.assertLegacyActive()
		})
	}
}

func TestHandoffRegistryReadLockSerializesInstanceMutation(t *testing.T) {
	f := newFixture(t)
	f.appendMove("done")
	if failures := f.drain(); len(failures) != 0 {
		t.Fatal(failures)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	pluginmgr.SetCrashHook(func(stage string) {
		if stage == "before_publish" {
			close(entered)
			<-release
		}
	})
	defer pluginmgr.SetCrashHook(nil)
	done := make(chan error, 1)
	go func() { _, err := f.forward(f.receiptDir("registry")); done <- err }()
	<-entered
	path, err := registry.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := store.TryWithLock(path+".lock", func() error { return nil })
	if err != nil || acquired {
		t.Fatalf("exclusive registry writer bypassed handoff's shared lock: %v %v", acquired, err)
	}
	mutation := make(chan error, 1)
	go func() {
		mutation <- registry.Update(func(c *registry.Config) error {
			c.Plugins[0].Config = map[string]any{"concurrent": "preserved"}
			return nil
		})
	}()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-mutation; err != nil {
		t.Fatal(err)
	}
	current, err := registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	if current.Plugins[0].Config["concurrent"] != "preserved" {
		t.Fatal("instance mutation lost")
	}
}
