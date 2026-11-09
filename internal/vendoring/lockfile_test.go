package vendoring

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/internal/vendoring/source"
)

func TestLockRoundTripAndConsistency(t *testing.T) {
	m, err := ParseManifest(
		[]byte(
			"version: \"1.0\"\ndependencies:\n  file:\n    source:\n      type: http\n      url: https://example.com/file\n    files:\n      - from: file\n        to: file\n",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := m.Digest()
	if err != nil {
		t.Fatal(err)
	}
	hash := hash([]byte("content"))
	lock := &Lock{
		Manifest:       ManifestFile,
		ManifestSHA256: digest,
		Dependencies: map[string]LockedDependency{
			"file": {
				Resolved: source.Resolved{SHA256: hash},
				Files:    map[string]string{"file": hash},
			},
		},
	}
	data, err := lock.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseLock(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := parsed.Matches(m); err != nil {
		t.Fatal(err)
	}
	data2, err := parsed.Marshal()
	if err != nil || string(data) != string(data2) {
		t.Fatal("lock serialization is not stable")
	}
	parsed.Dependencies["file"] = LockedDependency{
		Resolved: source.Resolved{SHA256: hash},
		Files:    map[string]string{"other": hash},
	}
	if err := parsed.Matches(m); err == nil {
		t.Fatal("tampered file inventory accepted")
	}
	if _, err := ParseLock(append([]byte("version: '1.0'\n"), data...)); err == nil {
		t.Fatal("unknown lock field accepted")
	}
	parsed.Dependencies["file"] = LockedDependency{
		Resolved: source.Resolved{Commit: strings.Repeat("ab", 20)},
		Files:    map[string]string{"file": hash},
	}
	if err := parsed.Matches(m); err == nil {
		t.Fatal("Git identity accepted for an HTTP manifest source")
	}
}

func TestLockResolvedIdentityValidation(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))
	for _, tc := range []struct {
		name     string
		identity string
		valid    bool
	}{
		{
			"Git",
			"{commit: '" + strings.Repeat("ab", 20) + "'}",
			true,
		},
		{
			"Git SHA-256",
			"{commit: '" + hash + "'}",
			true,
		},
		{
			"short commit",
			"{commit: short}",
			false,
		},
		{
			"mixed Git and HTTP",
			"{commit: '" + strings.Repeat("ab", 20) + "', sha256: '" + hash + "'}",
			false,
		},
		{
			"HTTP",
			"{sha256: '" + hash + "'}",
			true,
		},
		{
			"empty identity",
			"{}",
			false,
		},
		{
			"short HTTP hash",
			"{sha256: short}",
			false,
		},
		{
			"HTTP duplicated URL",
			"{url: https://example.com/file, sha256: '" + hash + "'}",
			false,
		},
		{
			"npm",
			"{url: https://example.com/file.tgz, version: '1.2.3', integrity: '" + integrity + "', sha256: '" + hash + "'}",
			true,
		},
		{
			"npm missing integrity",
			"{url: https://example.com/file.tgz, version: '1.2.3', sha256: '" + hash + "'}",
			false,
		},
		{
			"npm nonexact version",
			"{url: https://example.com/file.tgz, version: latest, integrity: '" + integrity + "', sha256: '" + hash + "'}",
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := fmt.Sprintf(
				"manifest: "+ManifestFile+"\nmanifest-sha256: '%s'\ndependencies:\n  file:\n    resolved: %s\n    files:\n      assets/file: '%s'\n",
				hash,
				tc.identity,
				hash,
			)
			if _, err := ParseLock([]byte(data)); (err == nil) != tc.valid {
				t.Errorf("lock validation = %v, want valid %v", err, tc.valid)
			}
		})
	}
}

func TestLockMatchesDirectoryInventory(t *testing.T) {
	m, err := ParseManifest(
		[]byte(validDirectories + "    files:\n      - from: README.md\n        to: skills/README.md\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := m.Digest()
	if err != nil {
		t.Fatal(err)
	}
	hash := hash([]byte("content"))
	locked := func(files ...string) *Lock {
		inventory := map[string]string{}
		for _, file := range files {
			inventory[file] = hash
		}
		return &Lock{
			Manifest:       ManifestFile,
			ManifestSHA256: digest,
			Dependencies: map[string]LockedDependency{
				"skills": {
					Resolved: source.Resolved{Commit: strings.Repeat("ab", 20)},
					Files:    inventory,
				},
			},
		}
	}
	complete := []string{
		"skills/README.md",
		"skills/notes/SKILL.md",
		"skills/review/SKILL.md",
		"skills/review/scripts/check.sh",
	}
	if err := locked(complete...).Matches(m); err != nil {
		t.Fatalf("complete inventory rejected: %v", err)
	}
	for name, files := range map[string][]string{
		"missing mapped file":     complete[1:],
		"empty directory":         {"skills/README.md", "skills/review/SKILL.md"},
		"file outside a mapping":  append([]string{"skills/other/SKILL.md"}, complete...),
		"directory name prefix":   append([]string{"skills/reviewer/SKILL.md"}, complete...),
		"directory itself locked": append([]string{"skills/notes"}, complete[0], complete[2], complete[3]),
	} {
		t.Run(name, func(t *testing.T) {
			if err := locked(files...).Matches(m); err == nil {
				t.Fatalf("inventory %v accepted", files)
			}
		})
	}
}
