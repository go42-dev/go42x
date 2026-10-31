package agentenv

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"testing"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/buildinfo"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/vendoring"
)

func TestPinBundledSchemaURL(t *testing.T) {
	for _, tt := range []struct {
		path   string
		schema string
	}{
		{
			config.FileProject,
			"assets/schemas/go42x.schema.yaml",
		},
		{
			vendoring.ManifestFile,
			"assets/schemas/vendor.schema.yaml",
		},
	} {
		t.Run(tt.path, func(t *testing.T) {
			for _, ref := range []string{"master", "v0.52.0", "aa1602e76e0a373536eb51643d153d985a86afc9"} {
				t.Run(ref, func(t *testing.T) {
					data := []byte(
						"# yaml-language-server: $schema=https://raw.githubusercontent.com/go42-dev/go42x/" +
							ref + "/" + tt.schema + "\nversion: \"1.0\"\n",
					)
					original := bytes.Clone(data)
					pinned := pinBundledSchemaURL(tt.path, data)
					want := "# yaml-language-server: $schema=" + buildinfo.SourceURL(tt.schema) + "\nversion: \"1.0\"\n"

					if string(pinned) != want || !bytes.Equal(data, original) {
						t.Fatalf("pinned schema = %q, original = %q", pinned, data)
					}
					if got := pinBundledSchemaURL("upstream/example.md", data); !bytes.Equal(got, original) {
						t.Fatalf("rewrote a schema URL in an upstream document: %q", got)
					}
				})
			}
		})
	}
}

func TestPinBundledSchemaURLPreservesCustomSchemas(t *testing.T) {
	for _, data := range []string{
		"# yaml-language-server: $schema=https://example.com/schema.yaml\n",
		"# yaml-language-server: $schema=https://raw.githubusercontent.com/other/project/main/assets/schemas/go42x.schema.yaml\n",
		"# yaml-language-server: $schema=https://raw.githubusercontent.com/go42-dev/go42x/main/assets/schemas/custom.schema.yaml\n",
		"# Example: https://raw.githubusercontent.com/go42-dev/go42x/master/assets/schemas/go42x.schema.yaml\n",
		"version: \"1.0\"\n",
	} {
		if got := pinBundledSchemaURL(config.FileProject, []byte(data)); string(got) != data {
			t.Errorf("changed custom schema or unrelated content: %q", got)
		}
	}
}

func TestBundledUpstreamInventory(t *testing.T) {
	bundle, err := assets.AgentEnvTemplates()
	if err != nil {
		t.Fatal(err)
	}
	manifestData, err := fs.ReadFile(bundle, vendoring.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := vendoring.ParseManifest(pinBundledSchemaURL(vendoring.ManifestFile, manifestData))
	if err != nil {
		t.Fatal(err)
	}
	lockData, err := fs.ReadFile(bundle, vendoring.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := vendoring.ParseLock(lockData)
	if err != nil {
		t.Fatal(err)
	}
	if lock.Manifest != vendoring.ManifestFile {
		t.Fatalf("bundled lock points to %q", lock.Manifest)
	}
	if err := lock.Matches(manifest); err != nil {
		t.Fatal(err)
	}
	for _, dependency := range lock.Dependencies {
		for path, want := range dependency.Files {
			data, err := fs.ReadFile(bundle, path)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != want {
				t.Errorf("bundled upstream checksum differs for %s: got %s, want %s", path, got, want)
			}
		}
	}
}
