package fspatch

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

type updateLogHandler struct {
	slog.Handler
	handle func(slog.Record)
}

func (h updateLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h updateLogHandler) Handle(_ context.Context, record slog.Record) error {
	h.handle(record)
	return nil
}

func readUpdateManifest(t *testing.T, directory string) updateManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, updateManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var manifest updateManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestUpdateBacksUpEntireBatchBeforeReplacing(t *testing.T) {
	dir := t.TempDir()
	backupDir := t.TempDir()
	first, second, created := filepath.Join(dir, "first"), filepath.Join(dir, "second"), filepath.Join(dir, "new")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("original"), 0640); err != nil {
			t.Fatal(err)
		}
	}
	originalInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	backupChecked := false
	logger := slog.New(updateLogHandler{Handler: slog.NewTextHandler(os.Stderr, nil), handle: func(record slog.Record) {
		if record.Message != "Update backup completed" {
			return
		}
		var backup string
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "backup" {
				backup = attr.Value.String()
			}
			return true
		})
		for _, name := range []string{"first", "second"} {
			for _, path := range []string{filepath.Join(dir, name), filepath.Join(backup, updateBackupFiles, name)} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "original" {
					t.Fatalf("before application %s = %q, %v", path, data, err)
				}
			}
		}
		if _, err := os.Stat(created); !os.IsNotExist(err) {
			t.Fatalf("new destination exists before backup completed: %v", err)
		}
		backupChecked = true
	}})
	plan := NewPlan(logger, dir, backupDir)
	for _, destination := range []struct {
		path, content string
		kind          Kind
	}{{first, "original", Sources}, {second, "updated", Settings}, {created, "created", Instructions}} {
		if err := plan.Write(
			destination.path,
			[]byte(destination.content),
			destination.kind,
			destination.path == first,
		); err != nil {
			t.Fatal(err)
		}
	}
	result, err := plan.ApplyUpdate(t.Context(), "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if !backupChecked || result.Applied != 3 || result.Sources != 1 {
		t.Fatalf("backup checked=%v, result=%+v", backupChecked, result)
	}
	if filepath.Dir(result.BackupDir) != filepath.Join(backupDir, "updates") {
		t.Fatalf("backup outside selected directory: %s", result.BackupDir)
	}
	currentInfo, err := os.Stat(first)
	if err != nil || os.SameFile(originalInfo, currentInfo) {
		t.Fatalf("identical content was not replaced: %v", err)
	}
	if currentInfo.Mode().Perm() != originalInfo.Mode().Perm() {
		t.Fatal("original permissions were not preserved")
	}
	manifest := readUpdateManifest(t, result.BackupDir)
	if manifest.Status != updateComplete || manifest.CLIVersion != "test-version" || len(manifest.Files) != 3 {
		t.Fatalf("manifest = %+v", manifest)
	}
	for _, file := range manifest.Files {
		if file.Status != updateApplied || file.Existed != (file.Path != "new") {
			t.Errorf("incorrect recovery entry: %+v", file)
		}
	}
}

func TestUpdateFailurePreservesBackupsAndProgress(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "parent", "second")
	if err := os.MkdirAll(filepath.Dir(second), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(updateLogHandler{Handler: slog.NewTextHandler(os.Stderr, nil), handle: func(record slog.Record) {
		if record.Message != "Update backup completed" {
			return
		}
		// Simulate a destination becoming unavailable after successful backup.
		if err := os.RemoveAll(filepath.Dir(second)); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Dir(second), []byte("blocked"), 0600); err != nil {
			t.Fatal(err)
		}
	}})
	plan := NewPlan(logger, dir, filepath.Join(dir, "backups"))
	for _, path := range []string{first, second} {
		if err := plan.Write(path, []byte("updated"), Sources, false); err != nil {
			t.Fatal(err)
		}
	}
	result, err := plan.ApplyUpdate(t.Context(), "test")
	if err == nil || result == nil || result.Applied != 1 {
		t.Fatalf("ApplyUpdate() = %+v, %v", result, err)
	}
	manifest := readUpdateManifest(t, result.BackupDir)
	if manifest.Status != updateApplying || manifest.Files[0].Status != updateApplied ||
		manifest.Files[1].Status != updateApplying {
		t.Fatalf("incorrect application progress: %+v", manifest)
	}
	for _, path := range []string{"first", "parent/second"} {
		data, err := os.ReadFile(filepath.Join(result.BackupDir, updateBackupFiles, filepath.FromSlash(path)))
		if err != nil || string(data) != "original" {
			t.Fatalf("original lost after failure: %s = %q, %v", path, data, err)
		}
	}
}

func TestUpdateRejectsChangedSnapshotsAndCancellation(t *testing.T) {
	for _, scenario := range []string{"concurrent edit", "canceled", "backup blocked"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			backupDir := filepath.Join(dir, "backups")
			path := filepath.Join(dir, "source")
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			plan := NewPlan(slog.New(slog.DiscardHandler), dir, backupDir)
			if err := plan.Write(path, []byte("replacement"), Sources, false); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := "original"
			switch scenario {
			case "concurrent edit":
				want = "user edit"
				if err := os.WriteFile(path, []byte(want), 0600); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				cancel()
			case "backup blocked":
				if err := os.WriteFile(backupDir, []byte("blocked"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := plan.ApplyUpdate(ctx, "test"); err == nil {
				t.Fatal("update unexpectedly succeeded")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != want {
				t.Fatalf("destination changed before successful backup: %q, %v", data, err)
			}
		})
	}
}

func TestUpdateSkipsUnchangedAndRecordsRemovals(t *testing.T) {
	root := t.TempDir()
	keep, obsolete := filepath.Join(root, "keep"), filepath.Join(root, "obsolete")
	for _, name := range []string{keep, obsolete} {
		if err := os.WriteFile(name, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(keep)
	if err != nil {
		t.Fatal(err)
	}
	plan := NewPlan(slog.New(slog.DiscardHandler), root, filepath.Join(root, "backups"))
	if err := plan.Write(keep, []byte("original"), Sources, false); err != nil {
		t.Fatal(err)
	}
	result, err := plan.ApplyUpdate(t.Context(), "test")
	if err != nil || result.Applied != 0 || result.BackupDir != "" {
		t.Fatalf("no-op result=%+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "backups")); !os.IsNotExist(err) {
		t.Fatalf("no-op created backups: %v", err)
	}
	current, err := os.Stat(keep)
	if err != nil || !os.SameFile(info, current) {
		t.Fatal("unchanged file was replaced")
	}
	plan = NewPlan(slog.New(slog.DiscardHandler), root, filepath.Join(root, "backups"))
	if err := plan.Remove(obsolete, Sources); err != nil {
		t.Fatal(err)
	}
	result, err = plan.ApplyUpdate(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(obsolete); !os.IsNotExist(err) {
		t.Fatal("obsolete file remains")
	}
	backup, err := os.ReadFile(filepath.Join(result.BackupDir, "files", "obsolete"))
	if err != nil || string(backup) != "original" {
		t.Fatalf("lost removal backup: %q %v", backup, err)
	}
	m := readUpdateManifest(t, result.BackupDir)
	if len(m.Files) != 1 || m.Files[0].Operation != "remove" {
		t.Fatalf("missing recovery operation: %+v", m)
	}
}

func TestUpdateUsesWriteOrderAndPreservesLockOnFailure(t *testing.T) {
	root := t.TempDir()
	lock := filepath.Join(root, "lock")
	if err := os.WriteFile(lock, []byte("old lock"), 0600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(updateLogHandler{Handler: slog.NewTextHandler(os.Stderr, nil), handle: func(record slog.Record) {
		if record.Message == "Update backup completed" {
			if err := os.WriteFile(filepath.Join(root, "blocked"), []byte("block"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}})
	plan := NewPlan(logger, root, filepath.Join(root, "backups"))
	if _, _, err := plan.Read(lock); err != nil {
		t.Fatal(err)
	}
	if err := plan.Write(filepath.Join(root, "first"), []byte("new"), Sources, false); err != nil {
		t.Fatal(err)
	}
	if err := plan.Write(filepath.Join(root, "blocked", "second"), []byte("new"), Sources, false); err != nil {
		t.Fatal(err)
	}
	if err := plan.Write(lock, []byte("new lock"), Settings, false); err != nil {
		t.Fatal(err)
	}
	result, err := plan.ApplyUpdate(t.Context(), "test")
	if err == nil || result == nil || result.Applied != 1 {
		t.Fatalf("partial batch: %+v %v", result, err)
	}
	data, err := os.ReadFile(lock)
	if err != nil || string(data) != "old lock" {
		t.Fatalf("lock published before files succeeded: %q %v", data, err)
	}
}
