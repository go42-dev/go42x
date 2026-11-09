package agentenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/buildinfo"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/fspatch"
	"github.com/go42-dev/go42x/internal/vendoring"
)

const (
	bundledSchemaURLBase = "https://raw.githubusercontent.com/go42-dev/go42x/"
	schemaPathProject    = "assets/schemas/go42x.schema.yaml"
	schemaPathVendor     = "assets/schemas/vendor.schema.yaml"
)

const bundleMergeTimeout = 30 * time.Second

// bundleReplacedDirs hold release-owned sources. A merge update replaces their bundled
// files instead of merging project changes; files that projects add beside them are kept.
var bundleReplacedDirs = []string{"resources/", "skills/", "upstream/"}

// bundleFile distinguishes a missing source from an empty file.
type bundleFile struct {
	data   []byte
	exists bool
}

// bundleMerge retains a candidate even when Git or a deletion reports a conflict.
// Overwritten reports that the candidate replaces local changes to a bundled file.
type bundleMerge struct {
	file        bundleFile
	conflict    bool
	overwritten bool
}

// pinBundledSchemaURL pins an official editor schema to this binary's source revision.
// Custom schema directives and unrelated content are preserved.
func pinBundledSchemaURL(path string, data []byte) []byte {
	var schemaPath string
	switch path {
	case config.FileProject:
		schemaPath = schemaPathProject
	case vendoring.ManifestFile:
		schemaPath = schemaPathVendor
	default:
		return data
	}
	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines {
		url, found := bytes.CutPrefix(line, []byte("# yaml-language-server: $schema="))
		if !found {
			continue
		}
		url = bytes.TrimSpace(url)
		ref, found := strings.CutPrefix(string(url), bundledSchemaURLBase)
		if !found {
			continue
		}
		ref, found = strings.CutSuffix(ref, "/"+schemaPath)
		if !found || len(ref) == 0 {
			continue
		}
		lines[i] = bytes.Replace(line, url, []byte(buildinfo.SourceURL(schemaPath)), 1)
		return bytes.Join(lines, []byte("\n"))
	}
	return data
}

// extractBundle installs bundled files missing from targetDir and keeps existing files.
// It reports whether it installed every bundled file.
func extractBundle(ctx context.Context, targetDir string) (bool, error) {
	templateFS, err := assets.AgentEnvTemplates()
	if err != nil {
		return false, fmt.Errorf("failed to open embedded templates: %w", err)
	}

	complete := true
	err = fs.WalkDir(templateFS, ".", func(path string, d fs.DirEntry, err error) error {
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
			complete = false
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
	return complete && err == nil, err
}

// prepareMergedBundle schedules a three-way merge of the project sources with the
// incoming bundle. The base is the previous bundle, fetched from the revision that the
// project's bundle record names; next replaces that record. It returns the merged
// configuration and the project-relative paths of bundled files whose local changes the
// update replaces.
func prepareMergedBundle(
	ctx context.Context,
	root string,
	plan *fspatch.Plan,
	bundle fs.FS,
	next *buildinfo.Source,
) ([]byte, []string, error) {
	dir := filepath.Join(root, config.Directory)
	previous, err := readBundleSource(plan, dir)
	if err != nil {
		return nil, nil, err
	}

	incoming, err := readBundle(ctx, bundle)
	if err != nil {
		return nil, nil, fmt.Errorf("read incoming bundle: %w", err)
	}

	installed := map[string]bool{}
	for path, data := range incoming {
		incoming[path] = pinBundledSchemaURL(path, data)
		name, _, _ := strings.Cut(path, "/")
		installed[name] = true
	}

	if err := os.MkdirAll(filepath.Join(root, ".build"), 0700); err != nil {
		return nil, nil, fmt.Errorf("create merge parent: %w", err)
	}

	workspace, err := os.MkdirTemp(filepath.Join(root, ".build"), "agentenv-update-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create merge workspace: %w", err)
	}

	base, err := fetchBundle(ctx, previous, workspace)
	if err = errors.Join(err, os.RemoveAll(filepath.Join(workspace, "source"))); err != nil {
		return nil, nil, errors.Join(
			fmt.Errorf("fetch previous bundle %s at %s: %w", previous.Repository, previous.Ref, err),
			os.RemoveAll(workspace),
		)
	}
	// The bundle directory also holds templates that go42x does not install into .go42x.
	maps.DeleteFunc(base, func(path string, _ []byte) bool {
		name, _, _ := strings.Cut(path, "/")
		return !installed[name]
	})
	if _, exists := base[config.FileProject]; !exists {
		return nil, nil, errors.Join(
			fmt.Errorf("previous bundle at %s must contain %s", previous.Ref, config.FileProject),
			os.RemoveAll(workspace),
		)
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
	var vendorManifest bundleFile
	overwritten := []string{}
	var conflicts []string
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		// The lock is rebuilt from the merged manifest below.
		if path == vendoring.LockFile {
			continue
		}

		target := filepath.Join(root, config.Directory, filepath.FromSlash(path))
		currentData, currentExists, err := plan.Read(target)
		if err != nil {
			return nil, nil, err
		}

		baseData, baseExists := base[path]
		incomingData, incomingExists := incoming[path]
		current := bundleFile{
			data:   currentData,
			exists: currentExists,
		}
		previous := bundleFile{
			data:   baseData,
			exists: baseExists,
		}
		next := bundleFile{
			data:   incomingData,
			exists: incomingExists,
		}
		merged, err := mergeBundleFile(ctx, workspace, path, previous, current, next)
		if err != nil {
			return nil, nil, fmt.Errorf("merge %s (workspace %s): %w", path, workspace, err)
		}
		if merged.file.exists {
			if err := writeBundleFile(workspace, filepath.Join("merged", path), merged.file.data); err != nil {
				return nil, nil, err
			}
		}
		if merged.conflict {
			conflicts = append(conflicts, path)
			continue
		}
		if merged.overwritten {
			overwritten = append(overwritten, filepath.ToSlash(filepath.Join(config.Directory, path)))
		}

		switch path {
		case config.FileProject:
			updatedConfig = merged.file.data
		case vendoring.ManifestFile:
			vendorManifest = merged.file
		}
		if merged.file.exists {
			err = plan.Write(target, merged.file.data, fspatch.KindSources, false)
		} else {
			err = plan.Remove(target, fspatch.KindSources)
		}

		if err != nil {
			return nil, nil, err
		}
	}
	if len(conflicts) > 0 {
		return nil, nil, fmt.Errorf(
			"conflicts in %s; candidates: %s",
			strings.Join(conflicts, ", "),
			filepath.Join(workspace, "merged"),
		)
	}
	if updatedConfig == nil {
		return nil, nil, fmt.Errorf("merged bundle must retain %s", config.FileProject)
	}
	if slices.Contains(paths, vendoring.LockFile) {
		if err := prepareVendorLock(plan, dir, vendorManifest, incoming); err != nil {
			return nil, nil, err
		}
	}
	if err := prepareBundleSource(plan, dir, next); err != nil {
		return nil, nil, err
	}

	if err := os.RemoveAll(workspace); err != nil {
		return nil, nil, fmt.Errorf("remove merge workspace %s: %w", workspace, err)
	}

	return updatedConfig, overwritten, nil
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
	if path == config.FileProject || path == vendoring.ManifestFile {
		// Official schema refs follow the incoming release, rather than participating in content conflicts.
		base.data = pinBundledSchemaURL(path, base.data)
		current.data = pinBundledSchemaURL(path, current.data)
		incoming.data = pinBundledSchemaURL(path, incoming.data)
	}
	if slices.ContainsFunc(bundleReplacedDirs, func(dir string) bool {
		return strings.HasPrefix(path, dir)
	}) {
		return replaceBundleFile(base, current, incoming), nil
	}
	if !incoming.exists {
		if current.exists && !bytes.Equal(current.data, base.data) {
			return bundleMerge{
				file:     current,
				conflict: true,
			}, nil
		}

		return bundleMerge{}, nil
	}
	if !current.exists {
		if !base.exists {
			return bundleMerge{file: incoming}, nil
		}
		if !bytes.Equal(base.data, incoming.data) {
			return bundleMerge{
				file:     incoming,
				conflict: true,
			}, nil
		}

		return bundleMerge{}, nil
	}
	if (base.exists && bytes.Equal(base.data, current.data)) || bytes.Equal(current.data, incoming.data) {
		return bundleMerge{file: incoming}, nil
	}
	if base.exists && bytes.Equal(base.data, incoming.data) {
		return bundleMerge{file: current}, nil
	}

	if err := writeBundleFile(workspace, filepath.Join("base", path), base.data); err != nil {
		return bundleMerge{}, err
	}
	if err := writeBundleFile(workspace, filepath.Join("current", path), current.data); err != nil {
		return bundleMerge{}, err
	}
	if err := writeBundleFile(workspace, filepath.Join("incoming", path), incoming.data); err != nil {
		return bundleMerge{}, err
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
		file: bundleFile{
			data:   output.Bytes(),
			exists: true,
		},
		conflict: err != nil,
	}, nil
}

// replaceBundleFile installs the incoming version of a release-owned file, restoring
// it when missing and removing it when the release retires it. Replacing local changes
// to a previously bundled file is reported. A project file at a path that the previous
// bundle did not contain is a conflict, so the release never replaces a project's own file.
func replaceBundleFile(base, current, incoming bundleFile) bundleMerge {
	if !current.exists || (incoming.exists && bytes.Equal(current.data, incoming.data)) {
		return bundleMerge{file: incoming}
	}
	if !base.exists {
		return bundleMerge{
			file:     incoming,
			conflict: true,
		}
	}

	return bundleMerge{
		file:        incoming,
		overwritten: !bytes.Equal(current.data, base.data),
	}
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
