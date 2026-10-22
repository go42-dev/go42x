package fspatch

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyUsesSelectedBackupDirectory(t *testing.T) {
	for _, operation := range []string{"replace", "remove"} {
		t.Run(operation, func(t *testing.T) {
			dir, backupDir := t.TempDir(), t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte("original"), 0640); err != nil {
				t.Fatal(err)
			}
			plan := NewPlan(slog.New(slog.DiscardHandler), dir, backupDir)
			var err error
			if operation == "remove" {
				err = plan.Remove(path, KindSettings)
			} else {
				err = plan.Write(path, []byte("replacement"), KindSettings, false)
			}

			if err != nil {
				t.Fatal(err)
			}
			if err := plan.Apply(); err != nil {
				t.Fatal(err)
			}
			backups, err := filepath.Glob(filepath.Join(backupDir, string(KindSettings), "config.yaml.*.bak"))
			if err != nil || len(backups) != 1 {
				t.Fatalf("backups = %v, %v", backups, err)
			}
			data, err := os.ReadFile(backups[0])
			if err != nil || string(data) != "original" {
				t.Fatalf("backup = %q, %v", data, err)
			}
			if operation == "remove" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("removed file still exists: %v", err)
				}
				return
			}

			data, err = os.ReadFile(path)
			if err != nil || string(data) != "replacement" {
				t.Fatalf("destination = %q, %v", data, err)
			}
		})
	}
}

func TestPlanRejectsSymlinkParents(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	plan := NewPlan(slog.New(slog.DiscardHandler), root, t.TempDir())
	if err := plan.Write(filepath.Join(root, "link", "file"), []byte("escape"), KindSources, false); err == nil {
		t.Fatal("symlink parent accepted")
	}
}
