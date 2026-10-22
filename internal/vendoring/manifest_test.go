package vendoring

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

const valid = `version: "1.0"
dependencies:
  assets:
    source:
      type: http
      url: https://example.com/file
    files:
      - from: file
        to: assets/file
`

func TestStrictManifest(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{
			"numeric version",
			strings.Replace(valid, `"1.0"`, `1.0`, 1),
		},
		{
			"boolean path",
			strings.Replace(valid, "from: file", "from: true", 1),
		},
		{
			"unknown version",
			strings.Replace(valid, `"1.0"`, `"2.0"`, 1),
		},
		{
			"unknown field",
			valid + "extra: value\n",
		},
		{
			"unknown source field",
			strings.Replace(valid, "      type: http\n", "      type: http\n      extra: value\n", 1),
		},
		{
			"unknown mapping field",
			valid + "        extra: value\n",
		},
		{
			"removed HTML flag",
			valid + "        html-to-markdown: 'true'\n",
		},
		{
			"duplicate key",
			valid + "version: \"1.0\"\n",
		},
		{
			"multiple documents",
			valid + "---\nversion: \"1.0\"\n",
		},
		{
			"traversal",
			strings.Replace(valid, "assets/file", "../file", 1),
		},
		{
			"absolute",
			strings.Replace(valid, "assets/file", "/file", 1),
		},
		{
			"alias",
			valid + "other: &value text\ncopy: *value\n",
		},
		{
			"bad source",
			strings.Replace(valid, "type: http", "type: ftp", 1),
		},
		{
			"unrelated fields",
			strings.Replace(valid, "type: http", "type: http\n      ref: main", 1),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(test.data)); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	m, err := ParseManifest([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Digest()
	if err != nil {
		t.Fatal(err)
	}
	m, err = ParseManifest([]byte("# comment\n" + valid))
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Digest()
	if err != nil || first != second {
		t.Fatal("comments changed normalized digest")
	}
}

func TestDestinationCollisions(t *testing.T) {
	for _, paths := range [][]string{{"file", "FILE"}, {"dir", "dir/file"}, {"dir/file", "dir"}} {
		if err := validateCollisions(paths); err == nil {
			t.Fatalf("collision accepted: %v", paths)
		}
	}
	if err := validateCollisions([]string{"dir/file", "dir/other"}); err != nil {
		t.Fatal(err)
	}
}

func TestManifestDigestNormalization(t *testing.T) {
	m, err := ParseManifest([]byte(`version: "1.0"
dependencies:
  http:
    source: {type: http, url: https://example.com/file}
    files:
      - {from: first, to: first}
      - {from: second, to: second}
  npm:
    source: {type: npm, package: docs, version: latest}
    files: [{from: README.md, to: docs/README.md}]
`))
	if err != nil {
		t.Fatal(err)
	}
	want, err := m.Digest()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("equivalent YAML", func(t *testing.T) {
		// Change dependency, field and file order, formatting, comments, and
		// explicit optional/default values without changing the manifest.
		equivalent, err := ParseManifest([]byte(`# Different presentation of the same manifest.
dependencies:
  npm:
    files: [{to: docs/README.md, from: README.md}]
    source: {registry: https://registry.npmjs.org, version: latest, package: docs, type: npm}
  http:
    files: [{to: second, from: second}, {to: first, from: first, converter: ''}]
    source: {url: https://example.com/file, type: http, ref: ''}
version: '1.0'
`))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := equivalent.Digest(); err != nil || got != want {
			t.Fatalf("equivalent manifest digest = %s, %v; want %s", got, err, want)
		}
	})
	t.Run("unordered mappings in memory", func(t *testing.T) {
		d := m.Dependencies["http"]
		slices.Reverse(d.Files)
		if got, err := m.Digest(); err != nil || got != want {
			t.Fatalf("reordered mappings digest = %s, %v; want %s", got, err, want)
		}
	})
	t.Run("changed source", func(t *testing.T) {
		d := m.Dependencies["http"]
		d.Source.URL = "https://example.com/changed"
		m.Dependencies["http"] = d
		if got, err := m.Digest(); err != nil || got == want {
			t.Fatalf("changed source digest = %s, %v; want a different digest", got, err)
		}
	})
}

func TestManifestRequiredValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{
			"missing version",
			strings.TrimPrefix(valid, "version: \"1.0\"\n"),
		},
		{
			"missing dependencies",
			"version: '1.0'\n",
		},
		{
			"invalid ID",
			strings.Replace(valid, "assets:", "bad/id:", 1),
		},
		{
			"removed category",
			strings.Replace(valid, "    source:", "    category: documentation\n    source:", 1),
		},
		{
			"removed license",
			strings.Replace(valid, "    source:", "    license: MIT\n    source:", 1),
		},
		{
			"removed attribution",
			strings.Replace(valid, "    source:", "    attribution: example\n    source:", 1),
		},
		{
			"empty files",
			"version: '1.0'\ndependencies: {a: {source: {type: http, url: https://example.com/file}, files: []}}",
		},
		{
			"missing source type",
			strings.Replace(valid, "      type: http\n", "", 1),
		},
		{
			"missing mapping field",
			strings.Replace(valid, "        to: assets/file\n", "", 1),
		},
		{
			"unknown field",
			valid + "unknown: value\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(tc.data)); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestManifestNormalization(t *testing.T) {
	// Empty optional fields are treated as unset, as with the main configuration.
	empty := strings.Replace(valid, "      type: http\n", "      type: http\n      ref: ''\n", 1)
	if _, err := ParseManifest([]byte(empty)); err != nil {
		t.Fatalf("empty optional field: %v", err)
	}
	npm := strings.Replace(valid, "      type: http\n      url: https://example.com/file\n",
		"      type: npm\n      package: assets\n      version: latest\n", 1)
	m, err := ParseManifest([]byte(npm))
	if err != nil || m.Dependencies["assets"].Source.Registry != "https://registry.npmjs.org" {
		t.Fatalf("registry default: %+v %v", m, err)
	}
	http := strings.Replace(
		valid,
		"      type: http\n",
		"      type: http\n      sha256: '"+strings.Repeat("AB", 32)+"'\n",
		1,
	)
	m, err = ParseManifest([]byte(http))
	if err != nil || m.Dependencies["assets"].Source.SHA256 != strings.Repeat("ab", 32) {
		t.Fatalf("SHA-256 normalization: %+v %v", m, err)
	}
}

func TestManifestCollisionValidation(t *testing.T) {
	data := valid + "      - from: another\n        to: assets/FILE\n"
	_, err := ParseManifest([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "destination collision") {
		t.Fatalf("expected collision validation, got %v", err)
	}
}

func TestManifestEmptyInventory(t *testing.T) {
	if _, err := ParseManifest([]byte("version: '1.0'\ndependencies: {}\n")); err != nil {
		t.Fatalf("empty inventory rejected: %v", err)
	}
}

func TestManifestSourceVariants(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		valid  bool
	}{
		{
			"HTTP",
			"{type: http, url: https://example.com/file}",
			true,
		},
		{
			"HTTP query",
			"{type: http, url: 'https://example.com/file?token=value'}",
			true,
		},
		{
			"HTTP hash",
			"{type: http, url: https://example.com/file, sha256: '" + strings.Repeat("AB", 32) + "'}",
			true,
		},
		{
			"invalid hash",
			"{type: http, url: https://example.com/file, sha256: short}",
			false,
		},
		{
			"HTTP credentials",
			"{type: http, url: 'https://user:password@example.com/file'}",
			false,
		},
		{
			"HTTP fragment",
			"{type: http, url: 'https://example.com/file#part'}",
			false,
		},
		{
			"HTTP Git field",
			"{type: http, url: https://example.com/file, ref: main}",
			false,
		},
		{
			"Git",
			"{type: git, url: https://example.com/repository.git, ref: main}",
			true,
		},
		{
			"Git SSH",
			"{type: git, url: ssh://git@example.com/repository.git, ref: main}",
			true,
		},
		{
			"Git file",
			"{type: git, url: file:///tmp/repository, ref: main}",
			true,
		},
		{
			"Git missing ref",
			"{type: git, url: https://example.com/repository.git}",
			false,
		},
		{
			"Git option ref",
			"{type: git, url: https://example.com/repository.git, ref: '--help'}",
			false,
		},
		{
			"Git fragment",
			"{type: git, url: 'https://example.com/repository.git#main', ref: main}",
			false,
		},
		{
			"npm exact",
			"{type: npm, package: assets, version: '1.2.3'}",
			true,
		},
		{
			"npm dist-tag",
			"{type: npm, package: '@fixture/assets', version: latest}",
			true,
		},
		{
			"npm prerelease",
			"{type: npm, package: assets, version: '1.2.3-beta.1+build.2'}",
			true,
		},
		{
			"npm custom registry",
			"{type: npm, package: assets, version: latest, registry: https://example.com/npm}",
			true,
		},
		{
			"npm empty registry",
			"{type: npm, package: assets, version: latest, registry: ''}",
			true,
		},
		{
			"npm range",
			"{type: npm, package: assets, version: '^1.2.3'}",
			false,
		},
		{
			"npm missing version",
			"{type: npm, package: assets}",
			false,
		},
		{
			"npm bad package",
			"{type: npm, package: '../assets', version: latest}",
			false,
		},
		{
			"npm registry query",
			"{type: npm, package: assets, version: latest, registry: 'https://example.com/npm?key=value'}",
			false,
		},
		{
			"unsupported source",
			"{type: ftp, url: https://example.com/file}",
			false,
		},
		{
			"unknown source field",
			"{type: http, url: https://example.com/file, unknown: field}",
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := fmt.Sprintf(
				"version: '1.0'\ndependencies:\n  assets:\n    source: %s\n    files: [{from: file, to: file}]\n",
				tc.source,
			)
			if _, err := ParseManifest([]byte(data)); (err == nil) != tc.valid {
				t.Errorf("ParseManifest() = %v, want valid %v", err, tc.valid)
			}
		})
	}
}

func TestManifestPortablePaths(t *testing.T) {
	for _, tc := range []struct {
		path  string
		valid bool
	}{
		{
			"file",
			true,
		}, {
			".hidden",
			true,
		}, {
			"dir/file name",
			true,
		},
		{
			"",
			false,
		}, {
			".",
			false,
		}, {
			"..",
			false,
		}, {
			"../file",
			false,
		},
		{
			"dir/../file",
			false,
		}, {
			"dir/./file",
			false,
		}, {
			"/file",
			false,
		},
		{
			"dir//file",
			false,
		}, {
			"dir/",
			false,
		}, {
			`dir\file`,
			false,
		}, {
			"C:/file",
			false,
		}, {
			"file\x00",
			false,
		},
	} {
		t.Run(tc.path, func(t *testing.T) {
			for _, field := range []string{"from", "to"} {
				from, to := "file", "file"

				if field == "from" {
					from = tc.path
				} else {
					to = tc.path
				}

				data := fmt.Sprintf(
					"version: '1.0'\ndependencies: {assets: {source: {type: http, url: https://example.com/file}, files: [{from: %q, to: %q}]}}",
					from,
					to,
				)
				if _, err := ParseManifest([]byte(data)); (err == nil) != tc.valid {
					t.Errorf("ParseManifest() = %v, want valid %v", err, tc.valid)
				}
			}
		})
	}
}

func TestManifestConverter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options string
		valid   bool
	}{
		{
			"HTML to Markdown",
			"html-to-markdown",
			true,
		},
		{
			"copy without conversion",
			"''",
			true,
		},
		{
			"boolean name",
			"true",
			false,
		},
		{
			"numeric name",
			"1",
			false,
		},
		{
			"null name",
			"null",
			false,
		},
		{
			"invalid name",
			"'html to markdown'",
			false,
		},
		{
			"options object",
			"{section: names}",
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := valid + "        converter: " + tc.options + "\n"
			if _, err := ParseManifest([]byte(data)); (err == nil) != tc.valid {
				t.Errorf("ParseManifest() = %v, want valid %v", err, tc.valid)
			}
		})
	}

	m, err := ParseManifest([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := m.Digest()
	if err != nil {
		t.Fatal(err)
	}
	d := m.Dependencies["assets"]
	d.Files[0].Converter = "html-to-markdown"
	m.Dependencies["assets"] = d
	converted, err := m.Digest()
	if err != nil || converted == plain {
		t.Fatalf("converter was omitted from the manifest digest: %s %v", converted, err)
	}
}
