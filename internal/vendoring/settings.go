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
	// TmpDir is go42x's temporary directory, which holds vendoring workspaces.
	TmpDir string
	// JournalDir is the agent journal directory, which vendoring never writes.
	JournalDir string
	Timeout    time.Duration
	DryRun     bool
	Force      bool
	JSON       bool
}

func NewSettings() *Settings {
	return &Settings{
		RootPath:     ".",
		ManifestPath: ManifestFile,
		TmpDir:       config.Context{}.TmpPath(),
		JournalDir:   config.DefaultJournalDir,
		Timeout:      2 * time.Minute,
	}
}

func (s *Settings) Validate() error {
	if s == nil {
		return fmt.Errorf("settings are required")
	}
	if len(strings.TrimSpace(s.RootPath)) == 0 || len(strings.TrimSpace(s.ManifestPath)) == 0 ||
		len(strings.TrimSpace(s.TmpDir)) == 0 || len(strings.TrimSpace(s.JournalDir)) == 0 ||
		s.Timeout <= 0 {
		return fmt.Errorf("root, manifest, temporary directory, journal directory and positive timeout are required")
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

	s.TmpDir = cfg.Context.TmpPath()
	s.JournalDir = cfg.Journal.Dir()
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
	if !filepath.IsAbs(s.TmpDir) {
		s.TmpDir = filepath.Join(root, filepath.FromSlash(s.TmpDir))
	}
	if !filepath.IsAbs(s.JournalDir) {
		s.JournalDir = filepath.Join(root, filepath.FromSlash(s.JournalDir))
	}
	s.ManifestPath = filepath.Clean(s.ManifestPath)
	s.LockPath = filepath.Clean(s.LockPath)
	s.TmpDir = filepath.Clean(s.TmpDir)
	s.JournalDir = filepath.Clean(s.JournalDir)
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
