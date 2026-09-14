package agentenv

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/go42-dev/go42x/assets"
)

func extractTemplate(targetDir string) error {
	templateFS, err := assets.AgentEnvTemplates()
	if err != nil {
		return fmt.Errorf("failed to open embedded templates: %w", err)
	}

	return fs.WalkDir(templateFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		targetPath := filepath.Join(targetDir, filepath.FromSlash(path))

		if d.IsDir() {
			// #nosec G301 -- Embedded templates are public project source files with intentionally traversable directories.
			return os.MkdirAll(targetPath, 0755)
		}

		data, err := fs.ReadFile(templateFS, path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		// #nosec G301 -- This directory contains public embedded templates, not user credentials.
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", targetPath, err)
		}

		// #nosec G304 G302 -- Embedded names install public templates in the trusted project tree; existing files are kept.
		file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if os.IsExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", targetPath, err)
		}
		_, writeErr := file.Write(data)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return fmt.Errorf("failed to write file %s: %w", targetPath, err)
		}

		return nil
	})
}
