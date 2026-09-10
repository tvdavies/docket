package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tvdavies/docket/internal/store"
)

func TestAtomicReportExposesPostRenameDirectoryFailure(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "target")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "moved")
	var stages []string
	report, err := store.WriteAtomicReport(filepath.Join(dir, "config"), []byte("new"), 0o600, func(stage string) {
		stages = append(stages, stage)
		if stage == "before_directory_sync" {
			if err := os.Rename(dir, moved); err != nil {
				t.Fatal(err)
			}
		}
	})
	if err != nil || !report.Renamed || report.DirSynced || report.DirSyncErr == "" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	data, err := os.ReadFile(filepath.Join(moved, "config"))
	if err != nil || string(data) != "new" {
		t.Fatalf("publication was lost: %q %v", data, err)
	}
	if len(stages) != 4 {
		t.Fatalf("boundaries=%v", stages)
	}
}

func TestAtomicReportPreRenameFailureLeavesOldBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Remove only the fixture's temp file immediately before rename.
	report, err := store.WriteAtomicReport(path, []byte("new"), 0o600, func(stage string) {
		if stage == "before_rename" {
			matches, _ := filepath.Glob(filepath.Join(dir, ".tmp-config-*"))
			for _, file := range matches {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
	if err == nil || report.Renamed {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old" {
		t.Fatalf("old config changed: %q %v", data, err)
	}
}
