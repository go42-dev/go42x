package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go42-dev/go42x/internal/vendoring"
)

func vendorManifest(remote string) string {
	return fmt.Sprintf(`version: "1.0"
dependencies:
  file:
    source:
      type: http
      url: %s/file.txt
    files:
      - from: file.txt
        to: assets/file.txt
`, remote)
}

func vendorReport(t *testing.T, result commandResult) vendoring.Result {
	t.Helper()
	var report vendoring.Result
	if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
		t.Fatalf("vendor stdout is not one JSON report: %v\n%s", err, result.stdout)
	}
	if report.Changes == nil || report.Issues == nil || strings.Contains(result.stderr, "Error:") {
		t.Fatalf("inconsistent JSON report or duplicate error: %s %s", result.stdout, result.stderr)
	}
	return report
}

func TestVendorTouch(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	// Initialization does not require valid agentenv configuration.
	p.write(t, ".go42x/go42x.yaml", "invalid: [")
	r := vendorReport(t, p.run(t, 0, "vendor", "touch", "third_party", "--json"))

	if !r.Valid || r.Operation != "touch" || r.Applied != 1 {
		t.Fatalf("touch: %+v", r)
	}
	manifestPath, schemaPath := "third_party/"+vendoring.ManifestFile, "third_party/.vendor.schema.yaml"
	data := p.read(t, manifestPath)
	m, err := vendoring.ParseManifest([]byte(data))
	if err != nil || len(m.Dependencies) != 0 ||
		!strings.HasPrefix(data, "# yaml-language-server: $schema=https://raw.githubusercontent.com/go42-dev/go42x/") ||
		!strings.Contains(data, "/assets/schemas/vendor.schema.yaml\n") {
		t.Fatalf("initial manifest: %s %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(p.root, schemaPath)); !os.IsNotExist(err) {
		t.Fatalf("touch installed a sibling schema: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(p.root, "third_party"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("touch created additional files: %v %v", entries, err)
	}
	before := p.snapshot(t)
	r = vendorReport(t, p.run(t, 0, "vendor", "touch", "third_party", "--json"))

	if r.Applied != 0 || len(r.Changes) != 0 {
		t.Fatalf("unchanged touch: %+v", r)
	}
	p.assertUnchanged(t, before)
	// Preserve user edits, permissions, and any pre-existing editor schema.
	p.write(t, manifestPath, "# User-authored manifest\nversion: '1.0'\ndependencies: {}\n")
	p.write(t, schemaPath, "stale schema\n")
	if err := os.Chmod(filepath.Join(p.root, manifestPath), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(p.root, schemaPath), 0600); err != nil {
		t.Fatal(err)
	}
	original := p.snapshot(t)[manifestPath]
	r = vendorReport(t, p.run(t, 0, "vendor", "touch", "third_party", "--json"))

	if r.Applied != 0 || p.snapshot(t)[manifestPath] != original || p.read(t, schemaPath) != "stale schema\n" {
		t.Fatalf("touch changed an existing manifest or editor schema: %+v", r)
	}
	info, err := os.Stat(filepath.Join(p.root, schemaPath))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("schema permissions: %v %v", info, err)
	}
	before = p.snapshot(t)
	p.run(t, 2, "vendor", "touch", "first", "second")
	p.run(t, 2, "vendor", "schema")
	p.assertUnchanged(t, before)
}

func TestVendorTouchDirectorySelection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		dir  string
	}{
		{
			"default",
			nil,
			".",
		},
		{
			"nested spaces",
			[]string{"vendor assets/nested"},
			"vendor assets/nested",
		},
		{
			"absolute",
			[]string{"absolute"},
			"absolute",
		},
		{
			"selected root",
			[]string{"selected-root"},
			"selected-root",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newProject(t)
			args := append([]string{"vendor", "touch"}, tc.args...)
			switch tc.name {
			case "absolute":
				args[2] = filepath.Join(p.root, tc.dir)
			case "selected root":
				args = []string{"vendor", "touch", "--root", filepath.Join(p.root, tc.dir)}
			}
			p.run(t, 0, args...)
			if _, err := os.Stat(filepath.Join(p.root, tc.dir, ".vendor.schema.yaml")); !os.IsNotExist(err) {
				t.Fatalf("touch installed a sibling schema: %v", err)
			}
			if _, err := vendoring.ParseManifest(
				[]byte(p.read(t, filepath.Join(tc.dir, vendoring.ManifestFile))),
			); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVendorTouchRejectsInvalidDestinations(t *testing.T) {
	t.Parallel()
	for _, name := range []string{vendoring.ManifestFile} {
		for _, kind := range []string{"directory", "symlink"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				p, external := newProject(t), newProject(t)

				if kind == "directory" {
					p.write(t, "third_party/"+name+"/sentinel", "preserve")
				} else {
					p.write(t, "third_party/.keep", "preserve")
					external.write(t, "target", "preserve")
					if err := os.Symlink(
						filepath.Join(external.root, "target"),
						filepath.Join(p.root, "third_party", name),
					); err != nil {
						t.Fatal(err)
					}
				}

				before, outside := p.snapshot(t), external.snapshot(t)
				r := vendorReport(t, p.run(t, 1, "vendor", "touch", "third_party", "--json"))

				if r.Valid || r.Applied != 0 {
					t.Fatalf("invalid touch destination was accepted: %+v", r)
				}
				p.assertUnchanged(t, before)
				external.assertUnchanged(t, outside)
			})
		}
	}
}

func TestVendorValidatesBeforeFetching(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"check", "sync", "update"} {
		t.Run(operation, func(t *testing.T) {
			p := newProject(t)
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = w.Write([]byte("{}"))
			}))
			defer server.Close()

			data := "# yaml-language-server: $schema=" + server.URL + "/schema.yaml\n" + vendorManifest(server.URL)
			data = strings.Replace(data, "      type: http\n", "      type: http\n      ref: main\n", 1)
			p.write(t, vendoring.ManifestFile, data)
			p.write(t, ".vendor.schema.yaml", "{}")
			before := p.snapshot(t)
			r := vendorReport(t, p.run(t, 1, "vendor", operation, "--json"))

			if r.Valid || len(r.Issues) == 0 || !strings.Contains(r.Issues[0].Reason, "unrelated fields") ||
				requests.Load() != 0 {
				t.Fatalf("Go validation was not enforced before fetching: %+v; requests=%d", r, requests.Load())
			}
			p.assertUnchanged(t, before)
		})
	}
}

func TestVendorIgnoresEditorSchemas(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	var schemaRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/schema.yaml" {
			schemaRequests.Add(1)
		}
		_, _ = w.Write([]byte("upstream"))
	}))
	defer server.Close()

	data := "# yaml-language-server: $schema=" + server.URL + "/schema.yaml\n" + vendorManifest(server.URL)
	data = strings.Replace(data, "      type: http\n", "      type: http\n      ref: ''\n", 1)
	p.write(t, vendoring.ManifestFile, data)
	p.write(t, ".vendor.schema.yaml", "invalid: [YAML\n")
	for _, operation := range []string{"update", "check", "sync"} {
		r := vendorReport(t, p.run(t, 0, "vendor", operation, "--json"))

		if !r.Valid || schemaRequests.Load() != 0 || p.read(t, ".vendor.schema.yaml") != "invalid: [YAML\n" {
			t.Fatalf("%s relied on an editor schema: %+v; requests=%d", operation, r, schemaRequests.Load())
		}
	}
}

func TestVendorLifecycleAndJSON(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	var body atomic.Value
	body.Store("upstream")
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, body.Load().(string))
	}))
	defer server.Close()

	p.write(t, vendoring.ManifestFile, vendorManifest(server.URL))
	before := p.snapshot(t)
	r := vendorReport(t, p.run(t, 1, "vendor", "check", "--json"))

	if r.Valid || requests.Load() != 0 {
		t.Fatalf("missing-lock check: %+v", r)
	}
	p.assertUnchanged(t, before)
	r = vendorReport(t, p.run(t, 0, "vendor", "update", "--dry-run", "--json"))

	if !r.Valid || !r.DryRun || r.Applied != 0 || len(r.Changes) != 2 {
		t.Fatalf("dry-run: %+v", r)
	}
	for _, name := range []string{"assets", vendoring.LockFile, ".go42x/backups"} {
		if _, err := os.Stat(filepath.Join(p.root, name)); !os.IsNotExist(err) {
			t.Fatalf("dry-run created %s: %v", name, err)
		}
	}
	r = vendorReport(t, p.run(t, 0, "vendor", "update", "--json", "--log-level=debug"))

	if !r.Valid || r.Applied != 2 || p.read(t, "assets/file.txt") != "upstream" {
		t.Fatalf("bootstrap: %+v", r)
	}
	locked := p.read(t, vendoring.LockFile)
	before = p.snapshot(t)
	r = vendorReport(t, p.run(t, 0, "vendor", "update", "--json"))

	if r.Applied != 0 || len(r.BackupDir) > 0 {
		t.Fatalf("no-op: %+v", r)
	}
	p.assertUnchanged(t, before)
	p.write(t, "assets/file.txt", "local edit")
	before = p.snapshot(t)
	r = vendorReport(t, p.run(t, 1, "vendor", "sync", "--json"))

	if r.Valid || len(r.Issues) == 0 {
		t.Fatalf("conflict: %+v", r)
	}
	p.assertUnchanged(t, before)
	r = vendorReport(t, p.run(t, 0, "vendor", "sync", "--force", "--json"))

	if r.Applied != 1 || p.read(t, vendoring.LockFile) != locked {
		t.Fatalf("forced sync: %+v", r)
	}
	backup, err := os.ReadFile(filepath.Join(r.BackupDir, "files", "assets", "file.txt"))
	if err != nil || string(backup) != "local edit" {
		t.Fatalf("conflict backup: %s %v", backup, err)
	}
	body.Store("new upstream")
	before = p.snapshot(t)
	vendorReport(t, p.run(t, 1, "vendor", "sync", "--json"))
	p.assertUnchanged(t, before)
	r = vendorReport(t, p.run(t, 0, "vendor", "update", "--json"))

	if r.Applied != 2 || p.read(t, "assets/file.txt") != "new upstream" {
		t.Fatalf("update: %+v", r)
	}
	server.Close()
	count := requests.Load()
	before = p.snapshot(t)
	r = vendorReport(t, p.run(t, 0, "vendor", "check", "--json"))

	if !r.Valid || requests.Load() != count {
		t.Fatalf("offline check: %+v", r)
	}
	p.assertUnchanged(t, before)
}

func TestVendorNestedManifestAndTempConfiguration(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("data"))
		}),
	)
	defer server.Close()

	for _, tc := range []struct {
		name   string
		temp   string
		local  string
		want   string
		config bool
	}{
		{
			name: "missing configuration",
			want: ".build",
		},
		{
			name:   "omitted temp",
			config: true,
			want:   ".build",
		},
		{
			name:   "empty temp",
			temp:   `""`,
			config: true,
			want:   ".build",
		},
		{
			name:   "relative temp",
			temp:   ".scratch",
			config: true,
			want:   ".scratch",
		},
		{
			name:   "local override",
			temp:   ".base",
			local:  ".local-scratch",
			config: true,
			want:   ".local-scratch",
		},
		{
			name:   "absolute temp",
			temp:   "absolute",
			config: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newProject(t)
			temp, want := tc.temp, filepath.Join(p.root, tc.want)

			if temp == "absolute" {
				want = filepath.Dir(p.root)
				temp = fmt.Sprintf("%q", want)
			}
			if tc.config {
				cfg := "version: \"1.0\"\nproject:\n  name: example\nproviders:\n  codex: {}\ncontext:\n  template: agents.tpl.md\n"

				if len(temp) > 0 {
					cfg += "  temp-dir: " + temp + "\n"
				}
				p.write(t, ".go42x/go42x.yaml", cfg)
			}
			if len(tc.local) > 0 {
				p.write(t, ".go42x/go42x.local.yaml", "context:\n  temp-dir: "+tc.local+"\n")
			}
			p.write(t, "third_party/"+vendoring.ManifestFile, vendorManifest(server.URL))
			caller := newProject(t)
			caller.env = append(
				caller.env,
				"GO42X_ROOT="+p.root,
				"GO42X_MANIFEST=third_party/"+vendoring.ManifestFile,
				"GO42X_JSON=true",
			)
			r := vendorReport(t, caller.run(t, 0, "vendor", "update"))

			if !r.Valid || p.read(t, "third_party/assets/file.txt") != "data" {
				t.Fatalf("nested update: %+v", r)
			}
			if !strings.Contains(p.read(t, "third_party/"+vendoring.LockFile), "manifest: "+vendoring.ManifestFile) {
				t.Fatal("lock is not beside the selected manifest")
			}
			entries, err := os.ReadDir(filepath.Join(want, "vendoring"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary workspace not used or not cleaned: %v %v", entries, err)
			}
			if tc.want != ".build" {
				if _, err := os.Stat(filepath.Join(p.root, ".build")); !os.IsNotExist(err) {
					t.Fatal("ignored configured temp directory")
				}
			}
			before := p.snapshot(t)
			vendorReport(t, caller.run(t, 0, "vendor", "check"))
			p.assertUnchanged(t, before)
		})
	}
}

func TestVendorInvalidConfigurationAndUsage(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.write(t, vendoring.ManifestFile, "version: \"1.0\"\ndependencies: {}\n")
	p.write(t, ".go42x/go42x.yaml", "version: [\n")
	before := p.snapshot(t)
	for _, operation := range []string{"check", "sync", "update"} {
		r := vendorReport(t, p.run(t, 1, "vendor", operation, "--json"))

		if r.Valid || r.Operation != operation || len(r.Issues) != 1 {
			t.Fatalf("%s accepted invalid config: %+v", operation, r)
		}
		p.assertUnchanged(t, before)
	}
	for _, args := range [][]string{
		{"vendor", "check", "--force"}, {"vendor", "check", "--dry-run"},
		{"vendor", "sync", "extra"}, {"vendor", "update", "--manifest="},
	} {
		p.run(t, 2, args...)
	}
	p.assertUnchanged(t, before)
}

func TestVendorRejectsInvalidPathsBeforeFetching(t *testing.T) {
	t.Parallel()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("data"))
	}))
	defer server.Close()

	p := newProject(t)
	p.write(t, vendoring.ManifestFile, vendorManifest(server.URL))
	p.write(t, "root-file", "not a directory\n")
	before := p.snapshot(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{
			"missing root",
			[]string{"--root", filepath.Join(p.root, "missing")},
		},
		{
			"root file",
			[]string{"--root", filepath.Join(p.root, "root-file")},
		},
		{
			"manifest outside root",
			[]string{"--manifest", "../outside.yaml"},
		},
		{
			"lock outside root",
			[]string{"--lock", "../outside.lock.yaml"},
		},
		{
			"colliding metadata",
			[]string{"--lock", vendoring.ManifestFile},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, operation := range []string{"check", "sync", "update"} {
				args := append([]string{"vendor", operation, "--json"}, tc.args...)
				r := vendorReport(t, p.run(t, 1, args...))

				if r.Valid || r.Operation != operation || len(r.Issues) != 1 {
					t.Fatalf("%s accepted invalid paths: %+v", operation, r)
				}
				if requests.Load() != 0 {
					t.Fatal("fetched a source before validating paths")
				}
				p.assertUnchanged(t, before)
			}
		})
	}
}
