package vendoring

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxFileBytes int64 = 64 << 20

func hash(data []byte) string {
	value := sha256.Sum256(data)
	return hex.EncodeToString(value[:])
}

// safePath rejects symlinks in every existing component beneath root. Missing
// components are allowed when preparing destination files.
func safePath(root, filename string) error {
	relative, err := filepath.Rel(root, filename)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return fmt.Errorf("path must be inside %s: %s", root, filename)
	}

	current := root
	parts := strings.Split(relative, string(filepath.Separator))
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is unsupported: %s", current)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("parent must be a directory: %s", current)
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return fmt.Errorf("expected a regular file: %s", current)
		}
	}
	return nil
}

func readFile(filename string) ([]byte, error) {
	// #nosec G304 -- Callers select metadata paths or check mapped paths with safePath before reading.
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxFileBytes {
		return nil, fmt.Errorf("file exceeds 64 MiB: %s", filename)
	}

	return data, nil
}

func (s *Service) selectFile(root string, mapping FileMapping) ([]byte, error) {
	filename := filepath.Join(root, filepath.FromSlash(mapping.From))
	if err := safePath(root, filename); err != nil {
		return nil, err
	}

	data, err := readFile(filename)
	if err != nil || len(mapping.Converter) == 0 {
		return data, err
	}

	c := s.converters[mapping.Converter]
	if c == nil {
		return nil, fmt.Errorf("unknown converter %q", mapping.Converter)
	}

	converted, err := c.Convert(data)
	if err != nil {
		return nil, fmt.Errorf("converter %s: %w", mapping.Converter, err)
	}
	if int64(len(converted)) > maxFileBytes {
		return nil, fmt.Errorf("converted file exceeds 64 MiB: %s", mapping.From)
	}

	return converted, nil
}

func checkConflict(
	filename string,
	original []byte,
	exists bool,
	ownedHash string,
	replacement []byte,
	force bool,
) error {
	if !exists || force {
		return nil
	}
	if len(ownedHash) > 0 {
		if hash(original) != ownedHash {
			return fmt.Errorf("locally modified owned file %s; use --force to replace it with a backup", filename)
		}
	} else if !bytes.Equal(original, replacement) {
		return fmt.Errorf("unowned target differs: %s; use --force to replace it with a backup", filename)
	}

	return nil
}

// checkOwnership inspects authored manifests and generated YAML locks to prevent independent
// manifests from claiming overlapping paths. Lock metadata identifies its
// manifest even when --lock selects a non-default filename.
func (s *Service) checkOwnership(ctx context.Context, in *inputs) error {
	ours := []string{s.settings.ManifestPath, s.settings.LockPath}
	for _, d := range in.manifest.Dependencies {
		for _, f := range d.Files {
			ours = append(ours, s.target(f.To))
		}
	}
	for name := range in.owned {
		ours = append(ours, s.target(name))
	}
	return filepath.WalkDir(s.settings.RootPath, func(filename string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filename != s.settings.RootPath &&
				(entry.Name() == ".git" || entry.Name() == ".tools" || entry.Name() == "node_modules" || s.skipDirectory(filename)) {
				return filepath.SkipDir
			}

			return nil
		}
		if filename == s.settings.ManifestPath || filename == s.settings.LockPath || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 4<<20 {
			return nil
		}

		data, err := readFile(filename)
		if err != nil {
			return err
		}
		// --lock allows any filename. Inspect its identifying field even when
		// the filename has no YAML extension, while avoiding parsing unrelated files.
		if entry.Name() != ManifestFile && entry.Name() != LockFile &&
			!bytes.Contains(data, []byte("manifest-sha256")) {
			return nil
		}

		var node yaml.Node
		if err := yaml.Unmarshal(data, &node); err != nil {
			if entry.Name() == ManifestFile || entry.Name() == LockFile {
				return err
			}

			return nil
		}

		isLock := false
		if len(node.Content) == 1 && node.Content[0].Kind == yaml.MappingNode {
			for i := 0; i < len(node.Content[0].Content); i += 2 {
				if node.Content[0].Content[i].Value == "manifest-sha256" {
					isLock = true
				}
			}
		}
		var foreign []string
		if isLock {
			lock, err := ParseLock(data)
			if err != nil {
				return fmt.Errorf("other vendoring lock %s: %w", filename, err)
			}

			owner, err := s.lockOwner(filename, lock.Manifest)
			if err != nil {
				return err
			}
			if owner == s.settings.ManifestPath {
				return fmt.Errorf("manifest already has another lock: %s", filename)
			}

			foreign = []string{owner, filename}
			for _, d := range lock.Dependencies {
				for destination := range d.Files {
					foreign = append(foreign, filepath.Join(filepath.Dir(owner), filepath.FromSlash(destination)))
				}
			}
		} else if entry.Name() == ManifestFile {
			m, err := ParseManifest(data)
			if err != nil {
				return fmt.Errorf("other vendoring manifest %s: %w", filename, err)
			}

			foreign = []string{
				filename, filepath.Join(filepath.Dir(filename), LockFile),
			}
			for _, d := range m.Dependencies {
				for _, f := range d.Files {
					foreign = append(foreign, filepath.Join(filepath.Dir(filename), filepath.FromSlash(f.To)))
				}
			}
		}

		if len(foreign) > 0 {
			snapshot, exists, err := in.plan.Read(filename)
			if err != nil {
				return err
			}
			if !exists || !bytes.Equal(data, snapshot) {
				return fmt.Errorf("ownership metadata changed during discovery: %s; retry", filename)
			}
		}
		for _, a := range ours {
			for _, b := range foreign {
				a, b := strings.ToLower(a), strings.ToLower(b)
				if within(a, b) || within(b, a) {
					return fmt.Errorf("vendoring ownership overlaps %s: %s", filename, b)
				}
			}
		}
		return nil
	})
}
