package output

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	updateManifestFile = "manifest.json"
	updateBackupFiles  = "files"
	updateBackingUp    = "backing_up"
	updateApplying     = "applying"
	updateComplete     = "complete"
	updatePending      = "pending"
	updateApplied      = "applied"
)

// UpdateResult describes a batch, including progress when application fails.
type UpdateResult struct {
	BackupDir string
	Sources   int
	Outputs   int
	Applied   int
}

type updateManifest struct {
	Version    int                `json:"version"`
	CLIVersion string             `json:"cli_version"`
	Status     string             `json:"status"`
	Files      []updateBackupFile `json:"files"`
}

type updateBackupFile struct {
	Path    string      `json:"path"`
	Kind    Kind        `json:"kind"`
	Existed bool        `json:"existed"`
	Mode    os.FileMode `json:"mode"`
	Status  string      `json:"status"`
}

// Merge appends another prepared plan for the same project. Duplicate paths are
// rejected before either plan is changed, including read-only dependencies.
func (p *Plan) Merge(other *Plan) error {
	root, err := filepath.Abs(p.outputDir)
	if err != nil {
		return err
	}
	otherRoot, err := filepath.Abs(other.outputDir)
	if err != nil {
		return err
	}
	if root != otherRoot {
		return fmt.Errorf("cannot merge plans for different output directories")
	}
	for _, f := range other.ordered {
		if _, exists := p.files[f.path]; exists {
			return fmt.Errorf("output %s is prepared more than once", f.path)
		}
	}
	for _, f := range other.ordered {
		p.files[f.path] = f
		p.ordered = append(p.ordered, f)
	}
	return nil
}

// ApplyUpdate rewrites every scheduled file, even when its content is unchanged.
// All originals are backed up before the first replacement. Snapshot checks only
// detect concurrent edits; they never select which files to update. Each write
// is atomic, but an I/O failure can leave a partially applied batch.
func (p *Plan) ApplyUpdate(ctx context.Context, cliVersion string) (*UpdateResult, error) {
	var files []*file
	result := &UpdateResult{}
	manifest := updateManifest{Version: 1, CLIVersion: cliVersion, Status: updateBackingUp}
	for _, f := range p.ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := f.checkUnchanged(); err != nil {
			return nil, err
		}
		if f.remove {
			return nil, fmt.Errorf("update does not remove files: %s", f.path)
		}
		if !f.write {
			continue
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
			Path: filepath.ToSlash(path), Kind: f.kind, Existed: f.exists, Mode: f.mode, Status: updatePending,
		})
		if f.kind == Sources {
			result.Sources++
		} else {
			result.Outputs++
		}
	}
	backupRoot := filepath.Join(p.outputDir, ".go42x", "backups", "updates")
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
	manifest.Status = updateApplying
	if err := manifest.save(backupDir); err != nil {
		return result, err
	}
	p.logger.Info("Update backup completed", "backup", backupDir)
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		// A crash between replacement and the next manifest write leaves an
		// explicit applying status instead of claiming the file is untouched.
		manifest.Files[i].Status = updateApplying
		if err := manifest.save(backupDir); err != nil {
			return result, err
		}
		if err := p.replace(f, false); err != nil {
			return result, err
		}
		result.Applied++
		manifest.Files[i].Status = updateApplied
		if err := manifest.save(backupDir); err != nil {
			return result, err
		}
		p.logger.Info("Updated file", "file", f.path)
	}
	manifest.Status = updateComplete
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
