package source

import (
	"strings"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	for _, s := range []Config{
		{
			Type:     "npm",
			Registry: "https://registry.npmjs.org",
			Package:  "pkg",
			Version:  "^1.0.0",
		},
		{
			Type:     "npm",
			Registry: "https://registry.npmjs.org?query=value",
			Package:  "pkg",
			Version:  "1.0.0",
		},
		{
			Type: "http",
			URL:  "https://example.com/file?archive=zip",
		},
		{
			Type: "http",
			URL:  "https://example.com/archive.zip//subdir",
		},
		{
			Type: "http",
			URL:  "https://example.com/archive.zip%2f%2fsubdir",
		},
		{
			Type: "git",
			URL:  "https://example.com/repo",
			Ref:  "--help",
		},
		{
			Type: "git",
			URL:  "https:///repo",
			Ref:  "main",
		},
		{
			Type: "git",
			URL:  "https://example.com/repo//subdir",
			Ref:  "main",
		},
	} {
		if err := s.Validate(); err == nil {
			t.Fatalf("invalid source accepted: %+v", s)
		}
	}
	for _, s := range []Config{
		{
			Type:    "npm",
			Package: "@fixture/assets",
			Version: "latest",
		},
		{
			Type:   "http",
			URL:    "https://example.com/file",
			SHA256: strings.Repeat("AB", 32),
		},
		{
			Type: "git",
			URL:  "ssh://git@example.com/repo",
			Ref:  "main",
		},
	} {
		if err := s.WithDefaults().Validate(); err != nil {
			t.Fatalf("valid source rejected: %+v: %v", s, err)
		}
	}
}
func TestConfigDefaults(t *testing.T) {
	original := Config{
		Type:    "npm",
		Package: "package",
		Version: "latest",
	}
	normalized := original.WithDefaults()

	if normalized.Registry != "https://registry.npmjs.org" || len(original.Registry) > 0 {
		t.Fatal("invalid registry default or input mutation")
	}
	if got := (Config{
		Type:   "http",
		SHA256: strings.Repeat("AB", 32),
	}).WithDefaults().SHA256; got != strings.Repeat(
		"ab",
		32,
	) {
		t.Fatal("SHA-256 was not normalized")
	}
}
func TestPortablePathValidation(t *testing.T) {
	for _, p := range []string{"", ".", "..", "../file", "dir/../file", "dir/./file", "/file", "dir//file", "dir/", `dir\file`, "C:/file", "file\x00"} {
		if err := ValidatePath(p); err == nil {
			t.Fatalf("invalid path accepted: %q", p)
		}
	}
	for _, p := range []string{"file", ".hidden", "dir/file name"} {
		if err := ValidatePath(p); err != nil {
			t.Fatal(err)
		}
	}
}
