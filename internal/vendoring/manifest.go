package vendoring

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/buildinfo"
	"github.com/go42-dev/go42x/internal/fspatch"
	"github.com/go42-dev/go42x/internal/vendoring/source"
)

const (
	SchemaVersion = "1.0"
	ManifestFile  = ".vendor.yaml"
)

type Manifest struct {
	Version      string                `yaml:"version"`
	Dependencies map[string]Dependency `yaml:"dependencies"`
}

type Dependency struct {
	Source source.Config `yaml:"source"`
	Files  []FileMapping `yaml:"files"`
}

type FileMapping struct {
	From      string `yaml:"from"`
	To        string `yaml:"to"`
	Converter string `yaml:"converter,omitempty"`
}

func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := decodeMetadata(data, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}

	for id, d := range m.Dependencies {
		d.Source = d.Source.WithDefaults()
		d.Files = slices.Clone(d.Files)
		slices.SortFunc(d.Files, func(a, b FileMapping) int {
			return strings.Compare(a.To, b.To)
		})
		m.Dependencies[id] = d
	}
	return &m, m.Validate()
}

// parseMetadata checks YAML syntax before decoding or applying defaults.
// Manifest and lock scalar values are exclusively strings.
func parseMetadata(data []byte) (*yaml.Node, error) {
	if len(data) > 4<<20 {
		return nil, fmt.Errorf("vendoring metadata exceeds 4 MiB")
	}

	var node yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&node); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected a YAML mapping")
	}

	if err := validateNode(&node); err != nil {
		return nil, err
	}

	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("expected exactly one YAML document")
	}

	return &node, nil
}

// decodeMetadata additionally rejects unknown fields in manifests and locks.
func decodeMetadata(data []byte, target any) error {
	if _, err := parseMetadata(data); err != nil {
		return err
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode YAML: %w", err)
	}

	return nil
}

func validateNode(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode || (n.Kind == yaml.ScalarNode && n.Tag != "!!str") {
		return fmt.Errorf("line %d: vendoring values must be strings; aliases are unsupported", n.Line)
	}
	if n.Kind == yaml.MappingNode {
		keys := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || keys[key.Value] {
				return fmt.Errorf("line %d: invalid or duplicate key %q", key.Line, key.Value)
			}

			keys[key.Value] = true
		}
	}
	for _, c := range n.Content {
		if err := validateNode(c); err != nil {
			return err
		}
	}
	return nil
}

// Digest hashes normalized YAML, independent of comments, formatting and order.
func (m *Manifest) Digest() (string, error) {
	canonical := *m
	canonical.Dependencies = make(map[string]Dependency, len(m.Dependencies))
	for id, d := range m.Dependencies {
		d.Files = slices.Clone(d.Files)
		slices.SortFunc(d.Files, func(a, b FileMapping) int {
			return strings.Compare(a.To, b.To)
		})
		canonical.Dependencies[id] = d
	}
	data, err := yaml.Marshal(canonical)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validDependencyID(id string) bool {
	return idPattern.MatchString(id)
}

func (m *Manifest) Validate() error {
	if m.Version != SchemaVersion {
		return fmt.Errorf("unsupported manifest version %q; expected %q", m.Version, SchemaVersion)
	}
	if m.Dependencies == nil {
		return fmt.Errorf("dependencies mapping is required (it may be empty)")
	}

	var targets []string
	for id, d := range m.Dependencies {
		if !validDependencyID(id) {
			return fmt.Errorf("invalid dependency ID %q", id)
		}

		if err := d.Source.Validate(); err != nil {
			return fmt.Errorf("dependency %s: %w", id, err)
		}
		if len(d.Files) == 0 {
			return fmt.Errorf("dependency %s requires file mappings", id)
		}

		for _, f := range d.Files {
			if len(f.Converter) > 0 && !validDependencyID(f.Converter) {
				return fmt.Errorf("dependency %s has an invalid converter name: %q", id, f.Converter)
			}

			if err := source.ValidatePath(f.From); err != nil {
				return fmt.Errorf("dependency %s from: %w", id, err)
			}

			if err := source.ValidatePath(f.To); err != nil {
				return fmt.Errorf("dependency %s to: %w", id, err)
			}

			targets = append(targets, f.To)
		}
	}
	return validateCollisions(targets)
}

// validateCollisions also rejects file/directory and case-folded conflicts.
func validateCollisions(paths []string) error {
	seen := map[string]bool{}
	for _, p := range paths {
		key := strings.ToLower(p)
		if seen[key] {
			return fmt.Errorf("destination collision: %s", p)
		}

		seen[key] = true
	}
	for p := range seen {
		for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return fmt.Errorf("file/directory destination collision: %s", p)
			}
		}
	}
	return nil
}

// Touch atomically creates a missing manifest
// Existing manifests are preserved.
func Touch(ctx context.Context, directory string) (*Result, error) {
	result := newResult(OperationTouch)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(strings.TrimSpace(directory)) == 0 {
		return result, fmt.Errorf("directory is required")
	}

	directory, err := filepath.Abs(directory)
	if err != nil {
		return result, err
	}

	if err := os.MkdirAll(directory, 0700); err != nil {
		return result, fmt.Errorf("create vendoring directory: %w", err)
	}

	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return result, err
	}

	manifestPath := filepath.Join(directory, ManifestFile)
	if err := safePath(directory, manifestPath); err != nil {
		return result, err
	}

	info, err := os.Lstat(manifestPath)
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	if err == nil {
		if !info.Mode().IsRegular() {
			return result, fmt.Errorf("expected a regular file: %s", manifestPath)
		}

		result.Valid = true
		return result, nil
	}

	manifest := strings.NewReplacer(
		"{{.SchemaURL}}", buildinfo.SourceURL("assets/schemas/vendor.schema.yaml"),
		"{{.SchemaVersion}}", SchemaVersion,
	).Replace(assets.VendorManifestTemplate())
	changed, err := writeTouchFile(ctx, directory, manifestPath, []byte(manifest))
	if err != nil {
		return result, fmt.Errorf("write %s: %w", manifestPath, err)
	}
	if changed {
		result.Applied = 1
		result.Changes = append(result.Changes,
			fspatch.Change{
				Path:      manifestPath,
				Operation: "create",
				Kind:      fspatch.Sources,
			})
	}
	result.Valid = true
	return result, nil
}

func writeTouchFile(
	ctx context.Context,
	directory, filename string,
	content []byte,
) (bool, error) {
	temporary, err := os.CreateTemp(directory, ".vendor-touch-*")
	if err != nil {
		return false, err
	}
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporary.Name())
	}()

	if _, err := temporary.Write(content); err != nil {
		return false, err
	}

	if err := temporary.Chmod(0644); err != nil {
		return false, err
	}

	if err := temporary.Sync(); err != nil {
		return false, err
	}

	if err := temporary.Close(); err != nil {
		return false, err
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}

	if err := safePath(directory, filename); err != nil {
		return false, err
	}
	// Publish without replacing a manifest created by another process.
	if err := os.Link(temporary.Name(), filename); err != nil {
		if os.IsExist(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}
