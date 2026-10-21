// Package fspatch prepares and applies file writes and removals
// with backups and atomic replacement of individual files.
package fspatch

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	OperationCreate = "create"
	OperationUpdate = "update"
	OperationRemove = "remove"

	Instructions Kind = "instructions"
	Settings     Kind = "settings"
	Sources      Kind = "sources"
)

// Kind groups files into backup subdirectories.
type Kind string

type file struct {
	root     string
	path     string
	previous []byte
	mode     os.FileMode
	exists   bool
	content  []byte
	kind     Kind
	write    bool
	remove   bool
	force    bool
}

// Plan retains originals while file changes are prepared.
// Apply and ApplyUpdate write to disk. A plan is intended for a single operation.
type Plan struct {
	logger *slog.Logger

	outputDir string
	backupDir string

	files      map[string]*file
	ordered    []*file
	operations []*file
}

// NewPlan limits destinations to outputDir and stores originals under backupDir.
// Both directories may be absolute or relative to the current working directory.
func NewPlan(logger *slog.Logger, outputDir, backupDir string) *Plan {
	return &Plan{
		logger:    logger,
		outputDir: outputDir,
		backupDir: backupDir,
		files:     make(map[string]*file),
	}
}

// Read snapshots an output once. Later reads return the same original content.
// Missing files are valid destinations; symlinks and special files are rejected.
func (p *Plan) Read(path string) ([]byte, bool, error) {
	f, err := p.read(path)
	if err != nil {
		return nil, false, err
	}

	return bytes.Clone(f.previous), f.exists, nil
}

// Write schedules a replacement. Force also replaces unchanged content.
func (p *Plan) Write(path string, content []byte, kind Kind, force bool) error {
	f, err := p.read(path)
	if err != nil {
		return err
	}
	if f.write || f.remove {
		return fmt.Errorf("output %s is scheduled more than once", path)
	}

	f.content = bytes.Clone(content)
	f.kind = kind
	f.write = true
	f.force = force
	p.operations = append(p.operations, f)
	return nil
}

// Remove schedules removal of a file whose ownership the caller has checked.
func (p *Plan) Remove(path string, kind Kind) error {
	f, err := p.read(path)
	if err != nil {
		return err
	}
	if f.write || f.remove {
		return fmt.Errorf("output %s is scheduled more than once", path)
	}

	f.kind = kind
	f.remove = true
	p.operations = append(p.operations, f)
	return nil
}

func (p *Plan) read(path string) (*file, error) {
	root, err := filepath.Abs(p.outputDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve output directory: %w", err)
	}

	path, err = filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve output path: %w", err)
	}

	relative, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return nil, fmt.Errorf("output must be inside %s: %s", root, path)
	}

	if f, ok := p.files[path]; ok {
		return f, nil
	}

	if err := checkParents(root, path); err != nil {
		return nil, err
	}

	f, err := readFile(path)
	if err != nil {
		return nil, err
	}

	f.root = root
	p.files[path] = f
	p.ordered = append(p.ordered, f)
	return f, nil
}

func readFile(path string) (*file, error) {
	f := &file{
		path: path,
		mode: 0644,
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("output %s must be a regular file", path)
	}
	// #nosec G304 -- The caller selected this output; file operations assume a trusted directory tree.
	f.previous, err = os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	f.mode = info.Mode().Perm()
	f.exists = true
	return f, nil
}

func (f *file) checkUnchanged() error {
	if err := checkParents(f.root, f.path); err != nil {
		return err
	}

	current, err := readFile(f.path)
	if err != nil {
		return err
	}
	if current.exists != f.exists || current.mode != f.mode || !bytes.Equal(current.previous, f.previous) {
		return fmt.Errorf("output %s changed while preparing file changes; retry", f.path)
	}

	return nil
}

// Validate verifies every captured input and destination without writing files.
func (p *Plan) Validate() error {
	for _, f := range p.ordered {
		if err := f.checkUnchanged(); err != nil {
			return err
		}
	}
	return nil
}

// Apply checks every snapshot before changing any output, then checks each file
// again before replacing or removing it. Replacement is atomic per file; an I/O
// failure during application can still leave some files updated.
func (p *Plan) Apply() error {
	if err := p.Validate(); err != nil {
		return err
	}

	for _, f := range p.operations {
		switch {
		case f.remove && f.exists:
			if err := p.backup(f); err != nil {
				return err
			}
			if err := f.checkUnchanged(); err != nil {
				return err
			}
			if err := os.Remove(f.path); err != nil {
				return fmt.Errorf("failed to remove output %s: %w", f.path, err)
			}
			p.logger.Info("Removed file", slog.String("file", f.path))
		case f.write && (f.force || !f.exists || !bytes.Equal(f.previous, f.content)):
			if err := p.replace(f, true); err != nil {
				return err
			}
			p.logger.Info("Wrote file", slog.String("file", f.path))
		}
	}
	return nil
}

func (p *Plan) replace(f *file, backupOriginal bool) error {
	if err := f.checkUnchanged(); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(f.path), 0700); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(f.path), "."+filepath.Base(f.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary output: %w", err)
	}
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporary.Name())
	}()

	if _, err := temporary.Write(f.content); err != nil {
		return fmt.Errorf("failed to write temporary output: %w", err)
	}

	if err := temporary.Chmod(f.mode); err != nil {
		return fmt.Errorf("failed to preserve output permissions: %w", err)
	}

	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("failed to sync temporary output: %w", err)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("failed to close temporary output: %w", err)
	}
	if backupOriginal && f.exists {
		if err := p.backup(f); err != nil {
			return err
		}
	}
	if err := f.checkUnchanged(); err != nil {
		return err
	}

	if err := os.Rename(temporary.Name(), f.path); err != nil {
		return fmt.Errorf("failed to replace output %s: %w", f.path, err)
	}

	return nil
}

func checkParents(root, filename string) error {
	rel, err := filepath.Rel(root, filepath.Dir(filename))
	if err != nil {
		return err
	}

	current := root
	if rel == "." {
		return nil
	}

	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("output parent must be a real directory: %s", current)
		}
	}
	return nil
}

func (p *Plan) backup(f *file) error {
	root, err := filepath.Abs(p.outputDir)
	if err != nil {
		return fmt.Errorf("failed to resolve output directory: %w", err)
	}

	relative, err := filepath.Rel(root, f.path)
	if err != nil {
		return fmt.Errorf("failed to resolve backup path: %w", err)
	}
	// Content-addressed backups avoid duplicate versions on repeated operations.
	backup := filepath.Join(p.backupDir, string(f.kind),
		fmt.Sprintf("%s.%x.bak", relative, sha256.Sum256(f.previous)))
	if err := os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	if err := os.WriteFile(backup, f.previous, 0600); err != nil {
		return fmt.Errorf("failed to back up %s: %w", f.path, err)
	}

	p.logger.Info("Backed up output", slog.String("file", f.path), slog.String("backup", backup))
	return nil
}

// Change describes a pending operation without exposing file contents or secrets.
type Change struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Kind      Kind   `json:"kind"`
}

// Changes returns only outputs that Apply would change, in application order.
func (p *Plan) Changes() []Change {
	changes := []Change{}
	for _, f := range p.operations {
		operation := ""
		switch {
		case f.remove && f.exists:
			operation = OperationRemove
		case f.write && !f.exists:
			operation = OperationCreate
		case f.write && (f.force || !bytes.Equal(f.previous, f.content)):
			operation = OperationUpdate
		}
		if len(operation) > 0 {
			root, _ := filepath.Abs(p.outputDir)
			relative, _ := filepath.Rel(root, f.path)
			changes = append(changes, Change{
				filepath.ToSlash(relative),
				operation,
				f.kind,
			})
		}
	}
	return changes
}
