package vendoring

import (
	"encoding/hex"
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/internal/vendoring/source"
)

// LockFile is the default filename for vendoring lockfiles.
const LockFile = ".vendor.lock.yaml"

type Lock struct {
	Manifest       string                      `yaml:"manifest"`
	ManifestSHA256 string                      `yaml:"manifest-sha256"`
	Dependencies   map[string]LockedDependency `yaml:"dependencies"`
}

type LockedDependency struct {
	Resolved source.Resolved   `yaml:"resolved"`
	Files    map[string]string `yaml:"files"`
}

func ParseLock(data []byte) (*Lock, error) {
	var lock Lock
	if err := decodeMetadata(data, &lock); err != nil {
		return nil, err
	}

	return &lock, lock.Validate()
}

func (l *Lock) Validate() error {
	if !source.ValidSHA256(l.ManifestSHA256) || len(l.Manifest) == 0 ||
		l.Manifest == "." ||
		path.IsAbs(l.Manifest) ||
		path.Clean(l.Manifest) != l.Manifest ||
		strings.ContainsAny(l.Manifest, "\\:\x00") ||
		l.Dependencies == nil {
		return fmt.Errorf("invalid lock manifest identity or dependency inventory")
	}

	var targets []string
	for id, d := range l.Dependencies {
		if !validDependencyID(id) {
			return fmt.Errorf("invalid locked dependency ID %q", id)
		}

		if _, err := resolvedType(d.Resolved); err != nil {
			return fmt.Errorf("locked %s: %w", id, err)
		}
		if len(d.Files) == 0 {
			return fmt.Errorf("locked dependency %s has no files", id)
		}

		for destination, checksum := range d.Files {
			if err := source.ValidatePath(destination); err != nil {
				return err
			}
			if !source.ValidSHA256(checksum) {
				return fmt.Errorf("invalid file checksum: %s", destination)
			}

			targets = append(targets, destination)
		}
	}
	return validateCollisions(targets)
}

// resolvedType validates the identity without requiring a copy of the manifest.
// Foreign locks can still describe old destinations after their manifest changes.
func resolvedType(r source.Resolved) (string, error) {
	if len(r.Commit) > 0 {
		commit, err := hex.DecodeString(r.Commit)
		if err != nil || (len(commit) != 20 && len(commit) != 32) || r.Commit != strings.ToLower(r.Commit) ||
			len(r.URL) > 0 || len(r.Version) > 0 || len(r.Integrity) > 0 || len(r.SHA256) > 0 {
			return "", fmt.Errorf("invalid locked Git identity")
		}

		return source.TypeGit, nil
	}
	if len(r.URL) > 0 || len(r.Version) > 0 || len(r.Integrity) > 0 {
		if !source.ExactVersion(r.Version) || !source.ValidSHA256(r.SHA256) {
			return "", fmt.Errorf("invalid locked npm identity")
		}

		if _, err := source.IntegrityChecksum(r.Integrity); err != nil {
			return "", err
		}

		if err := source.ValidateHTTPURL(r.URL); err != nil {
			return "", err
		}

		return source.TypeNPM, nil
	}
	if !source.ValidSHA256(r.SHA256) {
		return "", fmt.Errorf("invalid locked HTTP identity")
	}

	return source.TypeHTTP, nil
}

func (l *Lock) Matches(m *Manifest) error {
	digest, err := m.Digest()
	if err != nil {
		return err
	}
	if digest != l.ManifestSHA256 || len(m.Dependencies) != len(l.Dependencies) {
		return fmt.Errorf("manifest differs from lock; run vendor update")
	}

	for id, d := range m.Dependencies {
		locked, ok := l.Dependencies[id]
		if !ok || len(d.Files) != len(locked.Files) {
			return fmt.Errorf("inconsistent lock inventory for %s", id)
		}

		kind, err := resolvedType(locked.Resolved)
		if err != nil {
			return fmt.Errorf("locked %s: %w", id, err)
		}
		if kind != d.Source.Type {
			return fmt.Errorf("locked source type differs for %s", id)
		}
		if kind == source.TypeHTTP && len(d.Source.SHA256) > 0 && d.Source.SHA256 != locked.Resolved.SHA256 {
			return fmt.Errorf("locked HTTP checksum differs from manifest: %s", id)
		}
		if kind == source.TypeNPM && source.ExactVersion(d.Source.Version) &&
			d.Source.Version != locked.Resolved.Version {
			return fmt.Errorf("locked npm version differs from exact selector: %s", id)
		}

		for _, f := range d.Files {
			if _, ok := locked.Files[f.To]; !ok {
				return fmt.Errorf("inconsistent locked mapping for %s", id)
			}
		}
	}
	return nil
}

func (l *Lock) Marshal() ([]byte, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}

	data, err := yaml.Marshal(l)
	if err != nil {
		return nil, err
	}
	if len(data) > 4<<20 {
		return nil, fmt.Errorf("vendoring lock exceeds 4 MiB")
	}

	return data, nil
}
