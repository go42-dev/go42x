package integration_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go42-dev/go42x/internal/kwb"
	"github.com/go42-dev/go42x/internal/vendoring"
)

func writeFixture(t *testing.T, filename string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func httpManifest(remote, destination string) string {
	return fmt.Sprintf(
		"version: \"1.0\"\ndependencies:\n  file:\n    source:\n      type: http\n      url: %s\n    files:\n      - from: file.txt\n        to: %s\n",
		remote,
		destination,
	)
}

func fixtureSettings(root, manifestName string) *vendoring.Settings {
	settings := vendoring.NewSettings()
	settings.RootPath = root
	settings.ManifestPath = manifestName
	settings.TempDir = ".scratch"
	return settings
}

func fixtureService(t *testing.T, root, manifestName string, options ...vendoring.Option) *vendoring.Service {
	t.Helper()
	return fixtureServiceWithSettings(t, fixtureSettings(root, manifestName), options...)
}

func fixtureServiceWithSettings(
	t *testing.T,
	settings *vendoring.Settings,
	options ...vendoring.Option,
) *vendoring.Service {
	t.Helper()
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := settings.Resolve(); err != nil {
		t.Fatal(err)
	}
	service, err := vendoring.NewService(
		settings,
		append(
			[]vendoring.Option{vendoring.WithLogger(slog.New(slog.DiscardHandler)), vendoring.WithVersion("test")},
			options...)...)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestHTTPBootstrapCheckSyncUpdateAndDrift(t *testing.T) {
	root := t.TempDir()
	var body atomic.Value
	body.Store("first")
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, body.Load().(string))
	}))
	defer server.Close()
	manifestPath := filepath.Join(root, ".vendor.yaml")
	writeFixture(t, manifestPath, []byte(httpManifest(server.URL+"/file.txt", "assets/file.txt")))
	s := fixtureService(t, root, ".vendor.yaml")
	r, err := s.Update(t.Context())
	if err != nil || !r.Valid || r.Applied != 2 {
		t.Fatalf("bootstrap: %+v %v", r, err)
	}
	lockPath := filepath.Join(root, ".vendor.lock.yaml")
	locked := readFixture(t, lockPath)
	before := requests.Load()
	r, err = s.Check(t.Context())
	if err != nil || !r.Valid || requests.Load() != before {
		t.Fatalf("offline check: %+v %v", r, err)
	}
	r, err = s.Update(t.Context())
	if err != nil || r.Applied != 0 || r.BackupDir != "" || !bytes.Equal(locked, readFixture(t, lockPath)) {
		t.Fatalf("no-op: %+v %v", r, err)
	}
	file := filepath.Join(root, "assets", "file.txt")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	r, err = s.Check(t.Context())
	if err != nil || r.Valid || len(r.Issues) != 1 {
		t.Fatalf("missing check: %+v %v", r, err)
	}
	r, err = s.Sync(t.Context())
	if err != nil || r.Applied != 1 || !bytes.Equal(locked, readFixture(t, lockPath)) {
		t.Fatalf("locked restore: %+v %v", r, err)
	}
	body.Store("second")
	if _, err := s.Sync(t.Context()); err == nil {
		t.Fatal("mutable HTTP archive accepted against lock")
	}
	if string(readFixture(t, file)) != "first" || !bytes.Equal(locked, readFixture(t, lockPath)) {
		t.Fatal("failed fetch changed destinations or lock")
	}
	r, err = s.Update(t.Context())
	if err != nil || r.Applied != 2 {
		t.Fatalf("upstream update: %+v %v", r, err)
	}
	if string(readFixture(t, filepath.Join(r.BackupDir, "files", "assets", "file.txt"))) != "first" {
		t.Fatal("missing original backup")
	}
	writeFixture(t, file, []byte("user edit"))
	r, err = s.Check(t.Context())
	if err != nil || r.Valid {
		t.Fatal("local drift was not detected")
	}
	if _, err := s.Update(t.Context()); err == nil {
		t.Fatal("local edit overwritten without force")
	}
	settings := fixtureSettings(root, ".vendor.yaml")
	settings.Force = true
	r, err = fixtureServiceWithSettings(t, settings).Sync(t.Context())
	if err != nil || string(readFixture(t, filepath.Join(r.BackupDir, "files", "assets", "file.txt"))) != "user edit" {
		t.Fatalf("forced restore: %+v %v", r, err)
	}
}

func TestVendoredHTMLIsLockedAndSearchable(t *testing.T) {
	root := t.TempDir()
	const original = `<h2 id="intro">Introduction</h2><p>Whole-document introduction.</p>
<h2 id="names">Names</h2><p>Use <code>bytes.Buffer</code> and <a href="/doc/naming">the guide</a>.</p>
<h3>Getters</h3><p>Avoid <code>GetOwner</code>.</p>
<pre><code class="language-go">owner := obj.Owner()
if owner != user {
    obj.SetOwner(user)
}</code></pre>
<h2 id="semicolons">Semicolons</h2><p>Whole-document conclusion.</p>`
	var body atomic.Value
	body.Store(original)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, body.Load().(string))
	}))
	defer server.Close()
	manifestPath := filepath.Join(root, ".vendor.yaml")
	manifest := fmt.Sprintf(`version: "1.0"
dependencies:
  guide:
    source:
      type: http
      url: %s/guide.html
    files:
      - from: guide.html
        to: .go42x/upstream/go/names.md
        converter: html-to-markdown
`, server.URL)
	vendorSettings := fixtureSettings(root, ".vendor.yaml")
	vendorSettings.DryRun = true
	service := fixtureServiceWithSettings(t, vendorSettings)
	writeFixture(
		t,
		manifestPath,
		[]byte(strings.Replace(manifest, "converter: html-to-markdown", "converter: unknown", 1)),
	)
	if _, err := service.Update(t.Context()); err == nil ||
		!strings.Contains(err.Error(), `unknown converter "unknown"`) {
		t.Fatalf("unknown converter was not rejected: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatal("unknown converter triggered a download")
	}
	writeFixture(t, manifestPath, []byte(manifest))
	result, err := service.Update(t.Context())
	if err != nil || !result.Valid || len(result.Changes) != 2 {
		t.Fatalf("conversion dry-run: %+v %v", result, err)
	}
	file := filepath.Join(root, ".go42x", "upstream", "go", "names.md")
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("conversion dry-run created the destination: %v", err)
	}
	vendorSettings.DryRun = false
	service = fixtureServiceWithSettings(t, vendorSettings)
	result, err = service.Update(t.Context())
	if err != nil || !result.Valid || result.Applied != 2 {
		t.Fatalf("conversion update: %+v %v", result, err)
	}
	markdown := readFixture(t, file)
	for _, want := range []string{"## Introduction", "## Names", "### Getters", "## Semicolons", "`bytes.Buffer`", "```go", "obj.SetOwner(user)", "[the guide](/doc/naming)"} {
		if !strings.Contains(string(markdown), want) {
			t.Errorf("converted Markdown omitted %q:\n%s", want, markdown)
		}
	}
	for _, unwanted := range []string{"<h2", "<pre", "<code"} {
		if strings.Contains(string(markdown), unwanted) {
			t.Errorf("converted Markdown retained %q", unwanted)
		}
	}
	lockPath := filepath.Join(root, ".vendor.lock.yaml")
	locked := readFixture(t, lockPath)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	result, err = service.Sync(t.Context())
	if err != nil || result.Applied != 1 || !bytes.Equal(markdown, readFixture(t, file)) ||
		!bytes.Equal(locked, readFixture(t, lockPath)) {
		t.Fatalf("reproducible conversion sync: %+v %v", result, err)
	}
	body.Store(strings.Replace(original, "Introduction", "Updated introduction", 1))
	if _, err := service.Sync(t.Context()); err == nil {
		t.Fatal("sync accepted changed source HTML")
	}
	if !bytes.Equal(markdown, readFixture(t, file)) || !bytes.Equal(locked, readFixture(t, lockPath)) {
		t.Fatal("failed sync changed the destination or lock")
	}

	settings := kwb.NewSettings()
	settings.RootPath = root
	settings.IndexPath = filepath.Join(root, ".go42x", "kwb", "index")
	knowledge, err := kwb.NewService(settings, kwb.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = knowledge.Close() })
	if _, err := knowledge.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	found, err := knowledge.Search(
		t.Context(),
		kwb.SearchOptions{Query: "Getters", PathPrefix: ".go42x/upstream/", Limit: 10},
	)
	if err != nil || len(found.Results) == 0 || found.Results[0].Path != ".go42x/upstream/go/names.md" ||
		!strings.Contains(found.Results[0].Snippet, "GetOwner") {
		t.Fatalf("converted naming section is not searchable: %+v %v", found, err)
	}
}

func TestDryRunUnownedConflictAndOwnedRemoval(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("upstream")) }),
	)
	defer server.Close()
	manifestPath := filepath.Join(root, ".vendor.yaml")
	writeFixture(t, manifestPath, []byte(httpManifest(server.URL+"/file.txt", "assets/file.txt")))
	file := filepath.Join(root, "assets", "file.txt")
	writeFixture(t, file, []byte("unowned"))
	s := fixtureService(t, root, ".vendor.yaml")
	if _, err := s.Update(t.Context()); err == nil {
		t.Fatal("unowned conflict accepted")
	}
	settings := fixtureSettings(root, ".vendor.yaml")
	settings.Force, settings.DryRun = true, true
	s = fixtureServiceWithSettings(t, settings)
	r, err := s.Update(t.Context())
	if err != nil || len(r.Changes) != 2 {
		t.Fatalf("dry-run: %+v %v", r, err)
	}
	if string(readFixture(t, file)) != "unowned" {
		t.Fatal("dry-run changed existing file")
	}
	for _, p := range []string{".vendor.lock.yaml", ".go42x"} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Fatalf("dry-run created %s: %v", p, err)
		}
	}
	settings.DryRun = false
	s = fixtureServiceWithSettings(t, settings)
	r, err = s.Update(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if string(readFixture(t, filepath.Join(r.BackupDir, "files", "assets", "file.txt"))) != "unowned" {
		t.Fatal("force did not back up conflict")
	}
	writeFixture(t, filepath.Join(root, "assets", "custom"), []byte("custom"))
	writeFixture(t, manifestPath, []byte("version: \"1.0\"\ndependencies: {}\n"))
	settings.Force = false
	s = fixtureServiceWithSettings(t, settings)
	if _, err := s.Sync(t.Context()); err == nil {
		t.Fatal("sync accepted changed manifest")
	}
	r, err = s.Update(t.Context())
	if err != nil || r.Applied != 2 {
		t.Fatalf("owned removal: %+v %v", r, err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("owned obsolete file remains")
	}
	if string(readFixture(t, filepath.Join(root, "assets", "custom"))) != "custom" {
		t.Fatal("custom file removed")
	}
	if string(readFixture(t, filepath.Join(r.BackupDir, "files", "assets", "file.txt"))) != "upstream" {
		t.Fatal("removed file was not backed up")
	}
	if r, err := s.Check(t.Context()); err != nil || !r.Valid {
		t.Fatalf("empty inventory check: %+v %v", r, err)
	}
}

func TestIdenticalUnownedFileIsAdoptedWithoutReplacement(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("same")) }),
	)
	defer server.Close()
	writeFixture(t, filepath.Join(root, ".vendor.yaml"), []byte(httpManifest(server.URL+"/file.txt", "file.txt")))
	file := filepath.Join(root, "file.txt")
	writeFixture(t, file, []byte("same"))
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	r, err := fixtureService(t, root, ".vendor.yaml").Update(t.Context())
	if err != nil || r.Applied != 1 {
		t.Fatalf("adoption: %+v %v", r, err)
	}
	after, err := os.Stat(file)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("identical unowned file was replaced")
	}
}

func TestNestedManifestAndOwnership(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("data")) }),
	)
	defer server.Close()
	writeFixture(
		t,
		filepath.Join(root, "third_party", ".vendor.yaml"),
		[]byte(httpManifest(server.URL+"/file.txt", "file.txt")),
	)
	s := fixtureService(t, root, "third_party/.vendor.yaml")
	if _, err := s.Update(t.Context()); err != nil {
		t.Fatal(err)
	}
	if string(readFixture(t, filepath.Join(root, "third_party", "file.txt"))) != "data" {
		t.Fatal("nested target resolved from wrong directory")
	}
	if _, err := os.Stat(filepath.Join(root, "third_party", ".vendor.lock.yaml")); err != nil {
		t.Fatal(err)
	}
	writeFixture(
		t,
		filepath.Join(root, ".vendor.yaml"),
		[]byte(httpManifest(server.URL+"/file.txt", "third_party/file.txt")),
	)
	settings := fixtureSettings(root, ".vendor.yaml")
	settings.Force = true
	if _, err := fixtureServiceWithSettings(t, settings).Update(t.Context()); err == nil {
		t.Fatal("another manifest's file was taken over")
	}
}

type hookHandler struct {
	slog.Handler
	handle func(slog.Record)
}

func (h hookHandler) Enabled(context.Context, slog.Level) bool      { return true }
func (h hookHandler) Handle(_ context.Context, r slog.Record) error { h.handle(r); return nil }

func TestApplicationFailureLeavesOldLockAndRecovery(t *testing.T) {
	root := t.TempDir()
	var body atomic.Value
	body.Store("old")
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body.Load().(string)) }),
	)
	defer server.Close()
	writeFixture(
		t,
		filepath.Join(root, ".vendor.yaml"),
		[]byte(httpManifest(server.URL+"/file.txt", "output/file.txt")),
	)
	s := fixtureService(t, root, ".vendor.yaml")
	if _, err := s.Update(t.Context()); err != nil {
		t.Fatal(err)
	}
	oldLock := readFixture(t, filepath.Join(root, ".vendor.lock.yaml"))
	body.Store("new")
	logger := slog.New(hookHandler{handle: func(r slog.Record) {
		if r.Message == "Update backup completed" {
			if err := os.RemoveAll(filepath.Join(root, "output")); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(root, "output"), []byte("blocked"))
		}
	}})
	s = fixtureService(t, root, ".vendor.yaml", vendoring.WithLogger(logger))
	r, err := s.Update(t.Context())
	if err == nil || r == nil || r.BackupDir == "" {
		t.Fatalf("failure missing recovery: %+v %v", r, err)
	}
	if !bytes.Equal(oldLock, readFixture(t, filepath.Join(root, ".vendor.lock.yaml"))) {
		t.Fatal("new lock published after destination failure")
	}
	if string(readFixture(t, filepath.Join(r.BackupDir, "files", "output", "file.txt"))) != "old" {
		t.Fatal("original lost after failure")
	}
}

func TestConcurrentInputEditAndCancellation(t *testing.T) {
	for _, scenario := range []string{"manifest edit", "cancellation"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			manifestPath := filepath.Join(root, ".vendor.yaml")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "manifest edit" {
					_ = os.WriteFile(manifestPath, []byte("version: \"1.0\"\ndependencies: {}\n"), 0600)
					_, _ = w.Write([]byte("new"))
				} else {
					cancel()
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			writeFixture(t, manifestPath, []byte(httpManifest(server.URL+"/file.txt", "file.txt")))
			if _, err := fixtureService(t, root, ".vendor.yaml").Update(ctx); err == nil {
				t.Fatal("concurrent change or cancellation was ignored")
			}
			for _, name := range []string{"file.txt", ".vendor.lock.yaml"} {
				if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
					t.Fatalf("destination changed: %s", name)
				}
			}
		})
	}
}

func archiveFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	writer := tar.NewWriter(gz)
	for name, content := range files {
		if err := writer.WriteHeader(
			&tar.Header{Name: name, Mode: 0600, Size: int64(len(content)), Typeflag: tar.TypeReg},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestNpmExactAndDistTagLockedSync(t *testing.T) {
	for _, selector := range []string{"1.2.3", "latest"} {
		t.Run(selector, func(t *testing.T) {
			root := t.TempDir()
			archive := archiveFixture(
				t,
				map[string]string{"package/dist/file.txt": "npm content", "package/LICENSE": "license"},
			)
			hash := sha512.Sum512(archive)
			integrity := "sha512-" + base64.StdEncoding.EncodeToString(hash[:])
			var metadataCalls atomic.Int64
			var corrupt atomic.Bool
			var rejectMetadata atomic.Bool
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/package.tgz" {
					if corrupt.Load() {
						_, _ = w.Write([]byte("corrupt"))
					} else {
						_, _ = w.Write(archive)
					}
					return
				}
				metadataCalls.Add(1)
				if rejectMetadata.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				m := map[string]any{
					"name":    "@fixture/assets",
					"version": "1.2.3",
					"dist":    map[string]string{"tarball": server.URL + "/package.tgz", "integrity": integrity},
				}
				if selector == "latest" {
					_ = json.NewEncoder(w).
						Encode(map[string]any{"dist-tags": map[string]string{"latest": "1.2.3"}, "versions": map[string]any{"1.2.3": m}})
				} else {
					_ = json.NewEncoder(w).Encode(m)
				}
			}))
			defer server.Close()
			manifest := fmt.Sprintf(
				"version: \"1.0\"\ndependencies:\n  npm:\n    source:\n      type: npm\n      package: '@fixture/assets'\n      version: '%s'\n      registry: %s\n    files:\n      - from: dist/file.txt\n        to: assets/file.txt\n",
				selector,
				server.URL,
			)
			writeFixture(t, filepath.Join(root, ".vendor.yaml"), []byte(manifest))
			s := fixtureService(t, root, ".vendor.yaml")
			if _, err := s.Update(t.Context()); err != nil {
				t.Fatal(err)
			}
			lockBytes := readFixture(t, filepath.Join(root, ".vendor.lock.yaml"))
			lock, err := vendoring.ParseLock(lockBytes)
			if err != nil || lock.Dependencies["npm"].Resolved.Version != "1.2.3" {
				t.Fatalf("npm lock: %+v %v", lock, err)
			}
			before := metadataCalls.Load()
			rejectMetadata.Store(true)
			if err := os.Remove(filepath.Join(root, "assets", "file.txt")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Sync(t.Context()); err != nil {
				t.Fatal(err)
			}
			if metadataCalls.Load() != before ||
				!bytes.Equal(lockBytes, readFixture(t, filepath.Join(root, ".vendor.lock.yaml"))) {
				t.Fatal("sync queried registry or rewrote lock")
			}
			corrupt.Store(true)
			if _, err := s.Sync(t.Context()); err == nil {
				t.Fatal("npm integrity mismatch accepted")
			}
		})
	}
}

func TestGitBranchUpdateAndLockedRestore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is unavailable")
	}
	repository, root := t.TempDir(), t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "absent"))
	git := func(args ...string) string {
		t.Helper()
		command := exec.CommandContext(
			t.Context(),
			"git",
			append(
				[]string{
					"-c",
					"core.hooksPath=/dev/null",
					"-c",
					"commit.gpgsign=false",
					"-c",
					"user.name=Fixture",
					"-c",
					"user.email=fixture@example.com",
				},
				args...)...)
		command.Dir = repository
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-b", "main")
	writeFixture(t, filepath.Join(repository, "nested", "file.txt"), []byte("first"))
	git("add", ".")
	git("commit", "-m", "fixture one")
	first := git("rev-parse", "HEAD")
	remote := (&url.URL{Scheme: "file", Path: repository}).String()
	manifest := fmt.Sprintf(
		"version: \"1.0\"\ndependencies:\n  git:\n    source:\n      type: git\n      url: %s\n      ref: main\n    files:\n      - from: nested/file.txt\n        to: nested/file.txt\n",
		remote,
	)
	writeFixture(t, filepath.Join(root, ".vendor.yaml"), []byte(manifest))
	s := fixtureService(t, root, ".vendor.yaml")
	if _, err := s.Update(t.Context()); err != nil {
		t.Fatal(err)
	}
	lock, err := vendoring.ParseLock(readFixture(t, filepath.Join(root, ".vendor.lock.yaml")))
	if err != nil || lock.Dependencies["git"].Resolved.Commit != first {
		t.Fatal("Git commit was not locked")
	}
	writeFixture(t, filepath.Join(repository, "nested", "file.txt"), []byte("second"))
	git("add", ".")
	git("commit", "-m", "fixture two")
	if err := os.Remove(filepath.Join(root, "nested", "file.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if string(readFixture(t, filepath.Join(root, "nested", "file.txt"))) != "first" {
		t.Fatal("sync advanced branch")
	}
	if _, err := s.Update(t.Context()); err != nil {
		t.Fatal(err)
	}
	if string(readFixture(t, filepath.Join(root, "nested", "file.txt"))) != "second" {
		t.Fatal("explicit update did not advance branch")
	}
}

func TestControlPathsAndSymlinksCannotBeOverwritten(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("data")) }),
	)
	defer server.Close()
	for _, destination := range []string{".vendor.yaml", ".VENDOR.YAML", ".vendor.lock.yaml", ".VENDOR.LOCK.YAML", ".git/file", ".go42x", ".GO42X/go42x.yaml", ".go42x/backups/file", ".scratch", ".scratch/vendoring/file"} {
		t.Run(destination, func(t *testing.T) {
			root := t.TempDir()
			original := []byte(httpManifest(server.URL+"/file.txt", destination))
			writeFixture(t, filepath.Join(root, ".vendor.yaml"), original)
			settings := fixtureSettings(root, ".vendor.yaml")
			settings.Force = true
			if _, err := fixtureServiceWithSettings(t, settings).Update(t.Context()); err == nil {
				t.Fatal("control path accepted")
			}
			if !bytes.Equal(original, readFixture(t, filepath.Join(root, ".vendor.yaml"))) {
				t.Fatal("manifest overwritten")
			}
		})
	}
	for _, scenario := range []string{"destination parent", "backup parent"} {
		t.Run(scenario, func(t *testing.T) {
			root, external := t.TempDir(), t.TempDir()
			writeFixture(
				t,
				filepath.Join(root, ".vendor.yaml"),
				[]byte(httpManifest(server.URL+"/file.txt", "assets/file.txt")),
			)
			link := filepath.Join(root, "assets")
			if scenario == "backup parent" {
				link = filepath.Join(root, ".go42x")
			}
			if err := os.Symlink(external, link); err != nil {
				t.Fatal(err)
			}
			settings := fixtureSettings(root, ".vendor.yaml")
			settings.Force = true
			if _, err := fixtureServiceWithSettings(t, settings).Update(t.Context()); err == nil {
				t.Fatal("symlink accepted")
			}
			entries, err := os.ReadDir(external)
			if err != nil || len(entries) != 0 {
				t.Fatalf("symlink destination changed: %v %v", entries, err)
			}
		})
	}
}

func TestTouchCancellationDoesNotCreateDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "missing")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := vendoring.Touch(ctx, directory); err == nil {
		t.Fatal("cancelled touch succeeded")
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("cancelled touch created a directory: %v", err)
	}
}

func TestCustomLockFilenamePreservesOwnership(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("data")) }),
	)
	defer server.Close()
	writeFixture(
		t,
		filepath.Join(root, "nested", ".vendor.yaml"),
		[]byte(httpManifest(server.URL+"/file.txt", "file.txt")),
	)
	settings := vendoring.NewSettings()
	settings.RootPath, settings.ManifestPath, settings.LockPath = root, "nested/.vendor.yaml", "locks/custom.inventory"
	first := fixtureServiceWithSettings(t, settings)
	if _, err := first.Update(t.Context()); err != nil {
		t.Fatal(err)
	}
	if result, err := first.Check(t.Context()); err != nil || !result.Valid {
		t.Fatalf("custom lock check: %+v %v", result, err)
	}
	// Removing a mapping from the authored manifest does not abandon ownership
	// until that manifest's update removes it from its previous lock.
	writeFixture(t, filepath.Join(root, "nested", ".vendor.yaml"), []byte("version: \"1.0\"\ndependencies: {}\n"))
	writeFixture(
		t,
		filepath.Join(root, ".vendor.yaml"),
		[]byte(httpManifest(server.URL+"/file.txt", "nested/file.txt")),
	)
	otherSettings := fixtureSettings(root, ".vendor.yaml")
	otherSettings.Force = true
	if _, err := fixtureServiceWithSettings(t, otherSettings).Update(t.Context()); err == nil {
		t.Fatal("custom lock ownership was ignored")
	}
}

func TestCaseOnlyDestinationChangeIsRejected(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("data")) }),
	)
	defer server.Close()
	filename := filepath.Join(root, ".vendor.yaml")
	writeFixture(t, filename, []byte(httpManifest(server.URL+"/file.txt", "file.txt")))
	s := fixtureService(t, root, ".vendor.yaml")
	if _, err := s.Update(t.Context()); err != nil {
		t.Fatal(err)
	}
	oldLock := readFixture(t, filepath.Join(root, ".vendor.lock.yaml"))
	writeFixture(t, filename, []byte(httpManifest(server.URL+"/file.txt", "FILE.txt")))
	settings := fixtureSettings(root, ".vendor.yaml")
	settings.Force = true
	s = fixtureServiceWithSettings(t, settings)
	if _, err := s.Update(t.Context()); err == nil {
		t.Fatal("case-only rename accepted")
	}
	if string(readFixture(t, filepath.Join(root, "file.txt"))) != "data" ||
		!bytes.Equal(oldLock, readFixture(t, filepath.Join(root, ".vendor.lock.yaml"))) {
		t.Fatal("case-only rename changed destinations or lock")
	}
}

func TestMissingSelectedFileDoesNotPublish(t *testing.T) {
	root := t.TempDir()
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("data")) }),
	)
	defer server.Close()
	m := strings.Replace(
		httpManifest(server.URL+"/file.txt", "assets/file.txt"),
		"from: file.txt",
		"from: missing.txt",
		1,
	)
	writeFixture(t, filepath.Join(root, ".vendor.yaml"), []byte(m))
	if _, err := fixtureService(t, root, ".vendor.yaml").Update(t.Context()); err == nil {
		t.Fatal("missing source was accepted")
	}
	for _, path := range []string{"assets", ".vendor.lock.yaml", ".go42x/backups"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("failed selection changed %s: %v", path, err)
		}
	}
}

func TestWritersSerializeAcrossTemporaryDirectories(t *testing.T) {
	root := t.TempDir()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			_, _ = w.Write([]byte("data"))
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	writeFixture(t, filepath.Join(root, ".vendor.yaml"), []byte(httpManifest(server.URL+"/file.txt", "file.txt")))
	first := fixtureService(t, root, ".vendor.yaml")
	done := make(chan error, 1)
	go func() { _, err := first.Update(t.Context()); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first writer did not start")
	}
	settings := vendoring.NewSettings()
	settings.RootPath, settings.TempDir = root, ".different-scratch"
	settings.DryRun = true
	second := fixtureServiceWithSettings(t, settings)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := second.Update(ctx); err == nil {
		t.Fatal("second writer bypassed the project lock")
	}
	if calls.Load() != 1 {
		t.Fatal("second writer fetched while the first was active")
	}
	// Release the first fetch before waiting for its completed application.
	release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first writer did not finish")
	}
}
