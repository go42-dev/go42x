package vendoring

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go42-dev/go42x/internal/config"
)

type Settings struct {
	RootPath     string
	ManifestPath string
	LockPath     string
	TempDir      string
	Timeout      time.Duration
	DryRun       bool
	Force        bool
	JSON         bool
}

func NewSettings() *Settings {
	return &Settings{
		RootPath:     ".",
		ManifestPath: ManifestFile,
		TempDir:      config.DefaultTempDir,
		Timeout:      2 * time.Minute,
	}
}

func (s *Settings) Validate() error {
	if s == nil {
		return fmt.Errorf("settings are required")
	}
	if len(strings.TrimSpace(s.RootPath)) == 0 || len(strings.TrimSpace(s.ManifestPath)) == 0 ||
		len(strings.TrimSpace(s.TempDir)) == 0 ||
		s.Timeout <= 0 {
		return fmt.Errorf("root, manifest, temp directory and positive timeout are required")
	}

	return nil
}

// LoadProject applies optional project configuration. Call after Validate and before Resolve.
func (s *Settings) LoadProject() error {
	root, err := filepath.Abs(s.RootPath)
	if err != nil {
		return err
	}

	s.RootPath = root
	cfg, err := config.LoadProjectConfig(filepath.Join(root, config.Directory, config.FileProject), nil)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	s.TempDir = cfg.Context.WithDefaults().TempDir
	return nil
}

// Resolve makes configured paths absolute and canonical. Call after Validate.
func (s *Settings) Resolve() error {
	root, err := filepath.Abs(s.RootPath)
	if err != nil {
		return err
	}

	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}

	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("project root must be a directory")
	}

	s.RootPath = root
	if !filepath.IsAbs(s.ManifestPath) {
		s.ManifestPath = filepath.Join(root, s.ManifestPath)
	}
	if len(s.LockPath) == 0 {
		s.LockPath = filepath.Join(filepath.Dir(s.ManifestPath), LockFile)
	}
	if !filepath.IsAbs(s.LockPath) {
		s.LockPath = filepath.Join(root, s.LockPath)
	}
	if !filepath.IsAbs(s.TempDir) {
		s.TempDir = filepath.Join(root, s.TempDir)
	}
	s.ManifestPath = filepath.Clean(s.ManifestPath)
	s.LockPath = filepath.Clean(s.LockPath)
	s.TempDir = filepath.Clean(s.TempDir)
	if strings.EqualFold(s.ManifestPath, s.LockPath) {
		return fmt.Errorf("manifest and lock paths must differ")
	}

	for _, filename := range []string{s.ManifestPath, s.LockPath} {
		rel, err := filepath.Rel(root, filename)
		if err != nil || !filepath.IsLocal(rel) || rel == "." {
			return fmt.Errorf("metadata must remain inside project root")
		}
	}
	return nil
}
