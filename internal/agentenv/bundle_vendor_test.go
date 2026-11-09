package agentenv

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/internal/vendoring"
	"github.com/go42-dev/go42x/internal/vendoring/source"
)

// vendorFixture builds a manifest with one HTTP dependency per entry, mapping each ID
// to its source URL, and a lock that matches it.
func vendorFixture(t *testing.T, dependencies map[string]string) vendorBundle {
	t.Helper()
	var manifest strings.Builder
	manifest.WriteString("version: \"1.0\"\ndependencies:")
	if len(dependencies) == 0 {
		manifest.WriteString(" {}")
	}
	manifest.WriteString("\n")
	for _, id := range slices.Sorted(maps.Keys(dependencies)) {
		fmt.Fprintf(&manifest, "  %s:\n    source:\n      type: http\n      url: %s\n"+
			"    files:\n      - from: file\n        to: upstream/%s.md\n", id, dependencies[id], id)
	}

	parsed, err := vendoring.ParseManifest([]byte(manifest.String()))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := parsed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	lock := &vendoring.Lock{
		Manifest:       vendoring.ManifestFile,
		ManifestSHA256: digest,
		Dependencies:   map[string]vendoring.LockedDependency{},
	}
	for id, url := range dependencies {
		sum := sha256.Sum256([]byte(url))
		checksum := hex.EncodeToString(sum[:])
		lock.Dependencies[id] = vendoring.LockedDependency{
			Resolved: source.Resolved{SHA256: checksum},
			Files:    map[string]string{"upstream/" + id + ".md": checksum},
		}
	}
	data, err := lock.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return vendorBundle{
		manifest: bundleFile{
			data:   []byte(manifest.String()),
			exists: true,
		},
		lock: bundleFile{
			data:   append([]byte("---\n"), data...),
			exists: true,
		},
	}
}

func TestMergeVendorLockCombinesBundledAndProjectDependencies(t *testing.T) {
	bundled := map[string]string{"go-wiki": "https://example.com/go-wiki"}
	combined := map[string]string{
		"go-wiki": "https://example.com/go-wiki",
		"team":    "https://example.com/team",
	}
	incoming := vendorFixture(t, bundled)
	project := vendorFixture(t, combined)
	merged := project.manifest

	got, err := mergeVendorLock(merged, project, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.data) != string(project.lock.data) {
		t.Fatalf("rewrote an equivalent project lock:\n%s", got.data)
	}

	// The bundle moved go-wiki to a new revision; the project keeps its own dependency.
	updated := vendorFixture(t, map[string]string{"go-wiki": "https://example.com/go-wiki-v2"})
	merged = vendorFixture(t, map[string]string{
		"go-wiki": "https://example.com/go-wiki-v2",
		"team":    "https://example.com/team",
	}).manifest
	got, err = mergeVendorLock(merged, project, updated)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := vendoring.ParseLock(got.data)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := vendoring.ParseManifest(merged.data)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Matches(manifest); err != nil {
		t.Fatalf("rebuilt lock does not match the merged manifest: %v", err)
	}
	updatedLock, err := vendoring.ParseLock(updated.lock.data)
	if err != nil {
		t.Fatal(err)
	}
	projectLock, err := vendoring.ParseLock(project.lock.data)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(lock.Dependencies["go-wiki"].Files, updatedLock.Dependencies["go-wiki"].Files) {
		t.Error("bundled dependency did not take the incoming locked files")
	}
	if !maps.Equal(lock.Dependencies["team"].Files, projectLock.Dependencies["team"].Files) {
		t.Error("project dependency did not keep its locked files")
	}
}

func TestMergeVendorLockKeepsBundledBytes(t *testing.T) {
	incoming := vendorFixture(t, map[string]string{"go-wiki": "https://example.com/go-wiki"})
	current := vendorBundle{
		manifest: incoming.manifest,
		lock: bundleFile{
			data:   []byte("customized lock\n"),
			exists: true,
		},
	}

	got, err := mergeVendorLock(incoming.manifest, current, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if !got.exists || string(got.data) != string(incoming.lock.data) {
		t.Fatalf("lock = %q, want the bundled lock", got.data)
	}

	got, err = mergeVendorLock(bundleFile{}, current, incoming)
	if err != nil || got.exists {
		t.Fatalf("lock without a merged manifest = %+v, %v; want removal", got, err)
	}
}

func TestMergeVendorLockRejectsInconsistentDependencies(t *testing.T) {
	bundled := map[string]string{"go-wiki": "https://example.com/go-wiki"}
	incoming := vendorFixture(t, bundled)
	combined := vendorFixture(t, map[string]string{
		"go-wiki": "https://example.com/go-wiki",
		"team":    "https://example.com/team",
	})
	for _, tt := range []struct {
		name     string
		manifest bundleFile
		current  vendorBundle
		want     string
	}{
		{
			name:     "edited bundled dependency",
			manifest: vendorFixture(t, map[string]string{"go-wiki": "https://example.com/fork"}).manifest,
			current:  incoming,
			want:     "dependency go-wiki differs from the bundled definition",
		},
		{
			name:     "unlocked project dependency",
			manifest: combined.manifest,
			current: vendorBundle{
				manifest: combined.manifest,
				lock:     incoming.lock,
			},
			want: "go42x vendor update --manifest .go42x/.vendor.yaml",
		},
		{
			name:     "missing project lock",
			manifest: combined.manifest,
			current:  vendorBundle{manifest: combined.manifest},
			want:     ".vendor.lock.yaml is missing",
		},
		{
			name:     "dependency missing from the project manifest",
			manifest: combined.manifest,
			current:  incoming,
			want:     "project dependency team is not locked",
		},
		{
			name: "invalid merged manifest",
			manifest: bundleFile{
				data:   []byte("version: \"2.0\"\ndependencies: {}\n"),
				exists: true,
			},
			current: incoming,
			want:    "merged .vendor.yaml",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeVendorLock(tt.manifest, tt.current, incoming)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("mergeVendorLock() = %+v, %v; want error containing %q", got, err, tt.want)
			}
		})
	}
}
