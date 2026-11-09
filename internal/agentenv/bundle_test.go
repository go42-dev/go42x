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

func TestReplaceBundleFile(t *testing.T) {
	file := func(data string) bundleFile {
		return bundleFile{
			data:   []byte(data),
			exists: true,
		}
	}
	for _, tt := range []struct {
		name        string
		base        bundleFile
		current     bundleFile
		incoming    bundleFile
		want        bundleFile
		conflict    bool
		overwritten bool
	}{
		{
			name:     "unchanged",
			base:     file("old"),
			current:  file("old"),
			incoming: file("new"),
			want:     file("new"),
		},
		{
			name:     "missing locally",
			base:     file("old"),
			incoming: file("new"),
			want:     file("new"),
		},
		{
			name:     "new in the release",
			incoming: file("new"),
			want:     file("new"),
		},
		{
			name:     "local copy already current",
			base:     file("old"),
			current:  file("new"),
			incoming: file("new"),
			want:     file("new"),
		},
		{
			name:        "local changes",
			base:        file("old"),
			current:     file("custom"),
			incoming:    file("new"),
			want:        file("new"),
			overwritten: true,
		},
		{
			name:    "retired",
			base:    file("old"),
			current: file("old"),
		},
		{
			name:        "retired with local changes",
			base:        file("old"),
			current:     file("custom"),
			overwritten: true,
		},
		{
			name:     "project file at a newly bundled path",
			current:  file("custom"),
			incoming: file("new"),
			want:     file("new"),
			conflict: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := replaceBundleFile(tt.base, tt.current, tt.incoming)
			if got.file.exists != tt.want.exists || !bytes.Equal(got.file.data, tt.want.data) ||
				got.conflict != tt.conflict || got.overwritten != tt.overwritten {
				t.Fatalf("replaceBundleFile() = %+v, want file %q, conflict %t, overwritten %t",
					got, tt.want.data, tt.conflict, tt.overwritten)
			}
		})
	}
}

func TestMergeBundleFileReplacesReleaseOwnedDirectories(t *testing.T) {
	base := bundleFile{
		data:   []byte("bundled\n"),
		exists: true,
	}
	current := bundleFile{
		data:   []byte("customized\n"),
		exists: true,
	}
	for path, wantReplaced := range map[string]bool{
		"resources/go-codestyle.md":       true,
		"skills/go42x-skills/SKILL.md":    true,
		"upstream/go/package-names.md":    true,
		"chunks/100-operation.tpl.md":     false,
		"agents.tpl.md":                   false,
		"resources-custom/example.md":     false,
		"chunks/resources/example.tpl.md": false,
	} {
		t.Run(path, func(t *testing.T) {
			got, err := mergeBundleFile(t.Context(), t.TempDir(), path, base, current, base)
			if err != nil {
				t.Fatal(err)
			}

			want := current
			if wantReplaced {
				want = base
			}
			if !bytes.Equal(got.file.data, want.data) || got.overwritten != wantReplaced || got.conflict {
				t.Fatalf("mergeBundleFile() = %+v, want %q", got, want.data)
			}
		})
	}
}
