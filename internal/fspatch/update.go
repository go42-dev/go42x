package fspatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const (
	updateManifestFile = "manifest.json"
	updateBackupFiles  = "files"
)

const (
	updateStatusBackingUp = "backing_up"
	updateStatusApplying  = "applying"
	updateStatusComplete  = "complete"
	updateStatusPending   = "pending"
	updateStatusApplied   = "applied"
)

// UpdateResult describes a batch, including progress when application fails.
type UpdateResult struct {
	BackupDir string `json:"backup_dir"`
	Sources   int    `json:"sources"`
	Applied   int    `json:"applied"`
}

type updateManifest struct {
	Version    int                `json:"version"`
	CLIVersion string             `json:"cli_version"`
	Status     string             `json:"status"`
	Files      []updateBackupFile `json:"files"`
}

type updateBackupFile struct {
	Operation string      `json:"operation"`
	Path      string      `json:"path"`
	Kind      Kind        `json:"kind"`
	Existed   bool        `json:"existed"`
	Mode      os.FileMode `json:"mode"`
	Status    string      `json:"status"`
}

// ApplyUpdate backs up all originals before applying writes and removals in
// scheduling order. Unchanged writes are skipped unless forced. Snapshots include
// read-only inputs. Each replacement is atomic; the batch is not a transaction.
func (p *Plan) ApplyUpdate(ctx context.Context, cliVersion string) (*UpdateResult, error) {
	var files []*file
	result := &UpdateResult{}
	manifest := updateManifest{
		Version:    1,
		CLIVersion: cliVersion,
		Status:     updateStatusBackingUp,
	}
	for _, f := range p.ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if err := f.checkUnchanged(); err != nil {
			return nil, err
		}
	}
	for _, f := range p.operations {
		if (!f.remove || !f.exists) && (!f.write || (f.exists && !f.force && bytes.Equal(f.previous, f.content))) {
			continue
		}

		operation := OperationUpdate
		if f.remove {
			operation = OperationRemove
		} else if !f.exists {
			operation = OperationCreate
		}

		root, err := filepath.Abs(p.outputDir)
		if err != nil {
			return nil, err
		}

		path, err := filepath.Rel(root, f.path)
		if err != nil {
			return nil, err
		}

		files = append(files, f)
		manifest.Files = append(manifest.Files, updateBackupFile{
			Path: filepath.ToSlash(
				path,
			),
			Kind:      f.kind,
			Existed:   f.exists,
			Mode:      f.mode,
			Status:    updateStatusPending,
			Operation: operation,
		})
		if f.kind == KindSources {
			result.Sources++
		}
	}
	if len(files) == 0 {
		return result, nil
	}

	backupRoot := filepath.Join(p.backupDir, "updates")
	if err := os.MkdirAll(backupRoot, 0700); err != nil {
		return nil, fmt.Errorf("create update backup directory: %w", err)
	}

	backupDir, err := os.MkdirTemp(backupRoot, time.Now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return nil, fmt.Errorf("create update backup: %w", err)
	}

	result.BackupDir = backupDir
	if err := manifest.save(backupDir); err != nil {
		return result, err
	}

	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !f.exists {
			continue
		}

		path := filepath.Join(backupDir, updateBackupFiles, filepath.FromSlash(manifest.Files[i].Path))
		if err := writeUpdateFile(path, f.previous); err != nil {
			return result, fmt.Errorf("back up %s: %w", f.path, err)
		}
	}
	// Revalidate the entire batch after backup, before any destination is changed.
	for _, f := range p.ordered {
		if err := f.checkUnchanged(); err != nil {
			return result, err
		}
	}
	manifest.Status = updateStatusApplying
	if err := manifest.save(backupDir); err != nil {
		return result, err
	}

	p.logger.InfoContext(ctx, "Update backup completed", slog.String("backup", backupDir))
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		// A crash between replacement and the next manifest write leaves an
		// explicit applying status instead of claiming the file is untouched.
		for _, input := range p.ordered {
			if !input.write && !input.remove {
				if err := input.checkUnchanged(); err != nil {
					return result, err
				}
			}
		}
		manifest.Files[i].Status = updateStatusApplying
		if err := manifest.save(backupDir); err != nil {
			return result, err
		}

		var err error
		if f.remove {
			err = f.checkUnchanged()
			if err == nil {
				err = os.Remove(f.path)
			}
		} else {
			err = p.replace(f, false)
		}

		if err != nil {
			return result, err
		}

		result.Applied++
		manifest.Files[i].Status = updateStatusApplied
		if err := manifest.save(backupDir); err != nil {
			return result, err
		}

		p.logger.InfoContext(
			ctx,
			"Applied file change",
			slog.String("file", f.path),
			slog.String("operation", manifest.Files[i].Operation),
		)
	}
	manifest.Status = updateStatusComplete
	return result, manifest.save(backupDir)
}

func (m *updateManifest) save(directory string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	if err := writeUpdateFile(filepath.Join(directory, updateManifestFile), append(data, '\n')); err != nil {
		return fmt.Errorf("write update recovery manifest: %w", err)
	}

	return nil
}

// writeUpdateFile keeps both originals and recovery metadata private and flushes
// each file before publishing it. The manifest is replaced atomically on updates.
func writeUpdateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".backup-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporary.Name())
	}()

	if _, err := temporary.Write(data); err != nil {
		return err
	}

	if err := temporary.Sync(); err != nil {
		return err
	}

	if err := temporary.Close(); err != nil {
		return err
	}

	return os.Rename(temporary.Name(), path)
}
