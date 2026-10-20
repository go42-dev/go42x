package agentenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/buildinfo"
	"github.com/go42-dev/go42x/internal/fspatch"
)

const (
	templateSchemaURLBase = "https://raw.githubusercontent.com/go42-dev/go42x/master/"
	bundleMergeTimeout    = 30 * time.Second
)

// bundleFile distinguishes a missing source from an empty file.
type bundleFile struct {
	data   []byte
	exists bool
}

// bundleMerge retains a candidate even when Git or a deletion reports a conflict.
type bundleMerge struct {
	file     bundleFile
	conflict bool
}

// pinBundledSchemaURL pins a bundled configuration's editor schema to this binary's source revision.
func pinBundledSchemaURL(path string, data []byte) []byte {
	var schemaPath string
	switch path {
	case configFile:
		schemaPath = "assets/schemas/go42x.schema.yaml"
	case ".vendor.yaml":
		schemaPath = "assets/schemas/vendor.schema.yaml"
	default:
		return data
	}
	return bytes.Replace(
		data,
		[]byte(templateSchemaURLBase+schemaPath),
		[]byte(buildinfo.SourceURL(schemaPath)),
		1,
	)
}

func extractBundle(ctx context.Context, targetDir string) error {
	templateFS, err := assets.AgentEnvTemplates()
	if err != nil {
		return fmt.Errorf("failed to open embedded templates: %w", err)
	}

	return fs.WalkDir(templateFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
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
		data = pinBundledSchemaURL(path, data)

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

func (s *Service) prepareMergedBundle(
	ctx context.Context,
	root string,
	plan *fspatch.Plan,
	bundle fs.FS,
) ([]byte, error) {
	info, err := os.Lstat(s.settings.BaseDir)
	if err != nil {
		return nil, fmt.Errorf("read base directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("base must be a directory: %s", s.settings.BaseDir)
	}
	base, err := readBundle(ctx, os.DirFS(s.settings.BaseDir))
	if err != nil {
		return nil, fmt.Errorf("read base bundle: %w", err)
	}
	if _, exists := base[configFile]; !exists {
		return nil, fmt.Errorf("base bundle must contain %s", configFile)
	}
	incoming, err := readBundle(ctx, bundle)
	if err != nil {
		return nil, fmt.Errorf("read incoming bundle: %w", err)
	}
	for path, data := range incoming {
		incoming[path] = pinBundledSchemaURL(path, data)
	}

	if err := os.MkdirAll(filepath.Join(root, ".build"), 0700); err != nil {
		return nil, fmt.Errorf("create merge parent: %w", err)
	}
	workspace, err := os.MkdirTemp(filepath.Join(root, ".build"), "agentenv-update-*")
	if err != nil {
		return nil, fmt.Errorf("create merge workspace: %w", err)
	}

	paths := make([]string, 0, len(base)+len(incoming))
	for path := range base {
		paths = append(paths, path)
	}
	for path := range incoming {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)

	var updatedConfig []byte
	var conflicts []string
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		target := filepath.Join(root, agentEnvDir, filepath.FromSlash(path))
		currentData, currentExists, err := plan.Read(target)
		if err != nil {
			return nil, err
		}
		baseData, baseExists := base[path]
		incomingData, incomingExists := incoming[path]
		current := bundleFile{data: currentData, exists: currentExists}
		previous := bundleFile{data: baseData, exists: baseExists}
		next := bundleFile{data: incomingData, exists: incomingExists}
		merged, err := mergeBundleFile(ctx, workspace, path, previous, current, next)
		if err != nil {
			return nil, fmt.Errorf("merge %s (workspace %s): %w", path, workspace, err)
		}
		if merged.file.exists {
			if err := writeBundleFile(workspace, filepath.Join("merged", path), merged.file.data); err != nil {
				return nil, err
			}
		}
		if merged.conflict {
			conflicts = append(conflicts, path)
			continue
		}

		if path == configFile {
			updatedConfig = merged.file.data
		}
		if merged.file.exists {
			err = plan.Write(target, merged.file.data, fspatch.Sources, false)
		} else {
			err = plan.Remove(target, fspatch.Sources)
		}
		if err != nil {
			return nil, err
		}
	}
	if len(conflicts) > 0 {
		return nil, fmt.Errorf(
			"conflicts in %s; candidates: %s",
			strings.Join(conflicts, ", "),
			filepath.Join(workspace, "merged"),
		)
	}
	if updatedConfig == nil {
		return nil, fmt.Errorf("merged bundle must retain %s", configFile)
	}
	if err := os.RemoveAll(workspace); err != nil {
		return nil, fmt.Errorf("remove merge workspace %s: %w", workspace, err)
	}
	return updatedConfig, nil
}

func readBundle(ctx context.Context, source fs.FS) (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := fs.WalkDir(source, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("bundle source must be a regular file: %s", path)
		}
		data, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}
		files[path] = data
		return nil
	})
	return files, err
}

func mergeBundleFile(
	ctx context.Context,
	workspace, path string,
	base, current, incoming bundleFile,
) (bundleMerge, error) {
	if strings.HasPrefix(path, "upstream/") || path == ".vendor.yaml" || path == ".vendor.lock.yaml" {
		return bundleMerge{file: incoming}, nil
	}
	if !incoming.exists {
		if current.exists && !bytes.Equal(current.data, base.data) {
			return bundleMerge{file: current, conflict: true}, nil
		}
		return bundleMerge{}, nil
	}
	if !current.exists {
		if !base.exists {
			return bundleMerge{file: incoming}, nil
		}
		if !bytes.Equal(base.data, incoming.data) {
			return bundleMerge{file: incoming, conflict: true}, nil
		}
		return bundleMerge{}, nil
	}
	if (base.exists && bytes.Equal(base.data, current.data)) || bytes.Equal(current.data, incoming.data) {
		return bundleMerge{file: incoming}, nil
	}
	if base.exists && bytes.Equal(base.data, incoming.data) {
		return bundleMerge{file: current}, nil
	}

	for name, source := range map[string]bundleFile{
		"base":     base,
		"current":  current,
		"incoming": incoming,
	} {
		if err := writeBundleFile(workspace, filepath.Join(name, path), source.data); err != nil {
			return bundleMerge{}, err
		}
	}

	ctx, cancel := context.WithTimeout(ctx, bundleMergeTimeout)
	defer cancel()

	// #nosec G204 -- Git and its options are fixed; labels are literal arguments, paths follow --, and no shell is used.
	cmd := exec.CommandContext(
		ctx,
		"git",
		"merge-file",
		"-p",
		"--diff3",
		"-L",
		"project: "+path,
		"-L",
		"previous bundle: "+path,
		"-L",
		"new bundle: "+path,
		"--",
		filepath.Join(
			workspace,
			"current",
			path,
		),
		filepath.Join(workspace, "base", path),
		filepath.Join(workspace, "incoming", path),
	)
	cmd.WaitDelay = time.Second
	var output, diagnostics bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &diagnostics
	err := cmd.Run()
	if ctx.Err() != nil {
		return bundleMerge{}, ctx.Err()
	}
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() < 1 || exit.ExitCode() > 127) {
		return bundleMerge{}, fmt.Errorf("git merge-file: %w: %s", err, strings.TrimSpace(diagnostics.String()))
	}
	return bundleMerge{
		file:     bundleFile{data: output.Bytes(), exists: true},
		conflict: err != nil,
	}, nil
}

func writeBundleFile(root, path string, data []byte) error {
	target := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return fmt.Errorf("create merge directory: %w", err)
	}
	if err := os.WriteFile(target, data, 0600); err != nil {
		return fmt.Errorf("write merge candidate: %w", err)
	}
	return nil
}
