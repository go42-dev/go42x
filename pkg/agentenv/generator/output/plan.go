// Package output prepares generated file changes without modifying the filesystem.
package output

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// Kind selects the backup directory for a generated file.
type Kind string

const (
	Instructions Kind = "instructions"
	Settings     Kind = "settings"
	Sources      Kind = "sources"
)

type file struct {
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

// Plan retains originals while sources, instructions, and settings are prepared.
// Apply and ApplyUpdate write to disk. A plan is intended for a single operation.
type Plan struct {
	logger    *slog.Logger
	outputDir string
	files     map[string]*file
	ordered   []*file
}

func NewPlan(logger *slog.Logger, outputDir string) *Plan {
	return &Plan{
		logger:    logger,
		outputDir: outputDir,
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

// Write schedules a replacement. Force also replaces unchanged content, for --clean.
func (p *Plan) Write(path string, content []byte, kind Kind, force bool) error {
	f, err := p.read(path)
	if err != nil {
		return err
	}
	if f.write || f.remove {
		return fmt.Errorf("output %s is generated more than once", path)
	}
	f.content = bytes.Clone(content)
	f.kind = kind
	f.write = true
	f.force = force
	return nil
}

// Remove schedules removal of a file whose ownership the caller has checked.
func (p *Plan) Remove(path string, kind Kind) error {
	f, err := p.read(path)
	if err != nil {
		return err
	}
	if f.write || f.remove {
		return fmt.Errorf("output %s is generated more than once", path)
	}
	f.kind = kind
	f.remove = true
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
	f, err := readFile(path)
	if err != nil {
		return nil, err
	}
	p.files[path] = f
	p.ordered = append(p.ordered, f)
	return f, nil
}

func readFile(path string) (*file, error) {
	f := &file{path: path, mode: 0644}
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
	// #nosec G304 -- The caller selected this project output; generation assumes a trusted project tree.
	f.previous, err = os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	f.mode = info.Mode().Perm()
	f.exists = true
	return f, nil
}

func (f *file) checkUnchanged() error {
	current, err := readFile(f.path)
	if err != nil {
		return err
	}
	if current.exists != f.exists || current.mode != f.mode || !bytes.Equal(current.previous, f.previous) {
		return fmt.Errorf("output %s changed during generation; retry", f.path)
	}
	return nil
}

// Apply checks every snapshot before changing any output, then checks each file
// again before replacing or removing it. Replacement is atomic per file; an I/O
// failure during application can still leave some files updated.
func (p *Plan) Apply() error {
	for _, f := range p.ordered {
		if err := f.checkUnchanged(); err != nil {
			return err
		}
	}
	for _, f := range p.ordered {
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
			p.logger.Info("Removed generated output", "file", f.path)
		case f.write && (f.force || !f.exists || !bytes.Equal(f.previous, f.content)):
			if err := p.replace(f, true); err != nil {
				return err
			}
			p.logger.Info("Generated output", "file", f.path)
		}
	}
	return nil
}

func (p *Plan) replace(f *file, backupOriginal bool) error {
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

func (p *Plan) backup(f *file) error {
	root, err := filepath.Abs(p.outputDir)
	if err != nil {
		return fmt.Errorf("failed to resolve output directory: %w", err)
	}
	relative, err := filepath.Rel(root, f.path)
	if err != nil {
		return fmt.Errorf("failed to resolve backup path: %w", err)
	}
	// Content-addressed backups avoid duplicate versions on repeated generation.
	backup := filepath.Join(root, ".go42x", "backups", string(f.kind),
		fmt.Sprintf("%s.%x.bak", relative, sha256.Sum256(f.previous)))
	if err := os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}
	if err := os.WriteFile(backup, f.previous, 0600); err != nil {
		return fmt.Errorf("failed to back up %s: %w", f.path, err)
	}
	p.logger.Info("Backed up output", "file", f.path, "backup", backup)
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
	for _, f := range p.ordered {
		operation := ""
		switch {
		case f.remove && f.exists:
			operation = "remove"
		case f.write && !f.exists:
			operation = "create"
		case f.write && (f.force || !bytes.Equal(f.previous, f.content)):
			operation = "update"
		}
		if operation != "" {
			root, _ := filepath.Abs(p.outputDir)
			relative, _ := filepath.Rel(root, f.path)
			changes = append(changes, Change{filepath.ToSlash(relative), operation, f.kind})
		}
	}
	return changes
}
