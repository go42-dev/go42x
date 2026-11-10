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

func TestApplyRemovesDirectoriesLeftEmpty(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"Apply", "ApplyUpdate"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			obsolete := filepath.Join(root, "skills", "old", "references", "guide.md")
			kept := filepath.Join(root, "skills", "kept", "SKILL.md")
			replaced := filepath.Join(root, "reused", "old.md")
			for _, path := range []string{obsolete, kept, replaced} {
				if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			plan := NewPlan(slog.New(slog.DiscardHandler), root, filepath.Join(root, "backups"))
			for _, path := range []string{obsolete, replaced} {
				if err := plan.Remove(path, KindSkills); err != nil {
					t.Fatal(err)
				}
			}
			if err := plan.Write(
				filepath.Join(root, "reused", "new.md"),
				[]byte("new"),
				KindSkills,
				false,
			); err != nil {
				t.Fatal(err)
			}

			var err error
			if method == "Apply" {
				err = plan.Apply()
			} else {
				_, err = plan.ApplyUpdate(t.Context(), "test")
			}
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]bool{
				"skills/old":           false,
				"skills/kept/SKILL.md": true,
				"reused/new.md":        true,
			} {
				_, err := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if got := err == nil; got != want {
					t.Errorf("%s exists = %t, want %t", name, got, want)
				}
			}
			info, err := os.Stat(filepath.Join(root, "reused"))
			if err != nil || info.Mode().Perm() != 0750 {
				t.Fatalf("reused directory = %v, %v; want it kept with mode 0750", info, err)
			}
		})
	}
}

func TestApplyKeepsEmptyOutputDirectory(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "output")
	path := filepath.Join(root, "only", "file")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := NewPlan(slog.New(slog.DiscardHandler), root, t.TempDir())
	if err := plan.Remove(path, KindSources); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("output directory = %v, %v; want it kept and empty", entries, err)
	}
}
