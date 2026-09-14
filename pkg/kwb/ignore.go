package kwb

import (
	"context"
	"errors"
	"fmt"
	"os"
)

const sourceIgnoreHeader = "# go42x knowledgebase will ignore the following files and directories\n\n"

// ensureSourceIgnore creates the editable project exclusions on the first build.
// Read operations never call it, and existing paths are left untouched.
func ensureSourceIgnore(ctx context.Context, rootPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close() //nolint:errcheck
	if _, err := root.Lstat(sourceIgnorePath); !os.IsNotExist(err) {
		return err
	}
	if err := root.MkdirAll(".go42x", 0755); err != nil {
		return err
	}
	file, err := root.OpenFile(sourceIgnorePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("creating %s: %w", sourceIgnorePath, err)
	}
	_, writeErr := file.WriteString(sourceIgnoreHeader)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("writing %s: %w", sourceIgnorePath, err)
	}
	return nil
}
