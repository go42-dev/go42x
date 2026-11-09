package agentenv

import (
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/buildinfo"
	"github.com/go42-dev/go42x/internal/config"
)

// gitRepository creates a repository whose first commit holds files and returns its file
// URL and a function that runs Git in it.
func gitRepository(t *testing.T, files map[string]string) (string, func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("Git is required for bundle fetch tests: %v", err)
	}

	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{
			"-C", dir, "-c", "user.name=go42x", "-c", "user.email=go42x@example.com", "-c", "commit.gpgsign=false",
		}, args...)...)
		// User and system settings, such as signed tags, must not change the fixture.
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "--quiet")
	for name, content := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}
	git("add", "--all")
	git("commit", "--quiet", "--message", "bundle")

	return "file://" + path.Clean("/"+filepath.ToSlash(dir)), git
}

// bundleRepository commits this binary's bundle, changed by edits, as a previous release.
func bundleRepository(t *testing.T, edits map[string]string) *buildinfo.Source {
	t.Helper()
	bundle, err := assets.AgentEnvTemplates()
	if err != nil {
		t.Fatal(err)
	}
	files, err := readBundle(t.Context(), bundle)
	if err != nil {
		t.Fatal(err)
	}

	tree := map[string]string{}
	for name, data := range files {
		tree[bundleSourceDir+"/"+name] = string(data)
	}
	for name, content := range edits {
		tree[bundleSourceDir+"/"+name] = content
	}
	repository, git := gitRepository(t, tree)

	return &buildinfo.Source{
		Repository: repository,
		Ref:        git("rev-parse", "HEAD"),
	}
}

func TestBundleSourceRecord(t *testing.T) {
	source := &buildinfo.Source{
		Repository: "https://github.com/go42-dev/go42x.git",
		Ref:        "0123456789abcdef0123456789abcdef01234567",
	}
	want := "---\nrepository: https://github.com/go42-dev/go42x.git\n" +
		"ref: 0123456789abcdef0123456789abcdef01234567\n"

	data, err := marshalBundleSource(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Errorf("record = %q, want %q", data, want)
	}
	if parsed, err := parseBundleSource(data); err != nil || !reflect.DeepEqual(parsed, source) {
		t.Fatalf("parseBundleSource() = %+v, %v; want %+v", parsed, err, source)
	}

	for _, data := range []string{
		"",
		"repository: https://example.com/go42x.git\nref: v1.0.0\nextra: true\n",
		"repository: ext::sh -c touch% /tmp/pwned\nref: v1.0.0\n",
		"repository: git@example.com:go42x.git\nref: v1.0.0\n",
		"repository: https://example.com/go42x.git\n",
		"repository: https://example.com/go42x.git\nref: --upload-pack=touch\n",
		"repository: https://example.com/go42x.git\nref: 'v1.0.0 main'\n",
	} {
		if source, err := parseBundleSource([]byte(data)); err == nil {
			t.Errorf("parseBundleSource(%q) = %+v; want an error", data, source)
		}
	}
}

func TestFetchBundle(t *testing.T) {
	repository, git := gitRepository(t, map[string]string{
		"README.md":                              "Outside the bundle\n",
		bundleSourceDir + "/go42x.yaml":          "version: previous\n",
		bundleSourceDir + "/chunks/100.tpl.md":   "Previous chunk\n",
		bundleSourceDir + "/.gitignore.tpl":      "# go42x -- START\n",
		"assets/schemas/go42x.schema.yaml":       "{}\n",
		bundleSourceDir + "/skills/x/SKILL.md":   "Previous skill\n",
		bundleSourceDir + "/upstream/go/doc.md":  "Previous reference\n",
		bundleSourceDir + "/resources/style.md":  "Previous resource\n",
		bundleSourceDir + "/agents.tpl.md":       "Previous template\n",
		bundleSourceDir + "/.vendor.lock.yaml":   "Previous lock\n",
		bundleSourceDir + "/.vendor.yaml":        "Previous manifest\n",
		bundleSourceDir + "/chunks/200.tpl.md":   "Second chunk\n",
		bundleSourceDir + "/chunks/nested/x.md":  "Nested\n",
		bundleSourceDir + "/resources/other.md":  "Other resource\n",
		bundleSourceDir + "/upstream/google.md":  "Google\n",
		bundleSourceDir + "/skills/y/SKILL.md":   "Other skill\n",
		bundleSourceDir + "/skills/y/ref/doc.md": "Skill reference\n",
	})
	previous := git("rev-parse", "HEAD")
	git("tag", "v1.0.0")
	writeFile(
		t,
		filepath.Join(strings.TrimPrefix(repository, "file://"), bundleSourceDir, "go42x.yaml"),
		"version: next\n",
	)
	git("commit", "--quiet", "--all", "--message", "next")

	for name, ref := range map[string]string{
		"commit":             previous,
		"tag":                "v1.0.0",
		"abbreviated commit": previous[:12],
	} {
		t.Run(name, func(t *testing.T) {
			bundle, err := fetchBundle(t.Context(), &buildinfo.Source{
				Repository: repository,
				Ref:        ref,
			}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if got := string(bundle["go42x.yaml"]); got != "version: previous\n" {
				t.Errorf("go42x.yaml = %q, want the previous revision", got)
			}
			if got := string(bundle["skills/y/ref/doc.md"]); got != "Skill reference\n" {
				t.Errorf("nested file = %q", got)
			}
			for name := range bundle {
				if strings.HasPrefix(name, "../") || name == "README.md" || strings.HasPrefix(name, "assets/") {
					t.Errorf("fetched a file outside the bundle: %s", name)
				}
			}
			if len(bundle) != 15 {
				t.Errorf("fetched %d files, want 15: %v", len(bundle), slices.Sorted(maps.Keys(bundle)))
			}
		})
	}

	for name, ref := range map[string]string{
		"unknown tag":     "v9.9.9",
		"unknown commit":  "0123456789ab",
		"unknown hash":    strings.Repeat("a", 40),
		"bundle-less ref": "",
	} {
		t.Run(name, func(t *testing.T) {
			source := &buildinfo.Source{
				Repository: repository,
				Ref:        ref,
			}
			if len(ref) == 0 {
				git("rm", "--quiet", "-r", bundleSourceDir)
				git("commit", "--quiet", "--message", "remove bundle")
				source.Ref = git("rev-parse", "HEAD")
			}
			if bundle, err := fetchBundle(t.Context(), source, t.TempDir()); err == nil {
				t.Fatalf("fetchBundle(%s) = %v; want an error", ref, slices.Sorted(maps.Keys(bundle)))
			}
		})
	}
}

func TestFetchBundleRejectsSymlinks(t *testing.T) {
	repository, git := gitRepository(t, map[string]string{
		bundleSourceDir + "/go42x.yaml": "version: previous\n",
	})
	dir := strings.TrimPrefix(repository, "file://")
	if err := os.Symlink("go42x.yaml", filepath.Join(dir, bundleSourceDir, "linked.yaml")); err != nil {
		t.Fatal(err)
	}
	git("add", "--all")
	git("commit", "--quiet", "--message", "link")

	source := &buildinfo.Source{
		Repository: repository,
		Ref:        git("rev-parse", "HEAD"),
	}
	if _, err := fetchBundle(t.Context(), source, t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "regular file") {
		t.Fatalf("fetchBundle() = %v; want a symlink rejection", err)
	}
}

func TestInitRecordsOnlyCompleteInstallations(t *testing.T) {
	dir := t.TempDir()
	service := testService(t, dir, false)
	service.source = &buildinfo.Source{
		Repository: "https://example.com/go42x.git",
		Ref:        "v1.2.3",
	}
	record := filepath.Join(dir, ".go42x", bundleSourceFile)
	if _, err := service.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if recorded, err := parseBundleSource(
		readFile(t, record),
	); err != nil ||
		!reflect.DeepEqual(recorded, service.source) {
		t.Fatalf("bundle record = %+v, %v", recorded, err)
	}

	// Kept files may come from another release, so a partial installation keeps the record.
	if err := os.Remove(filepath.Join(dir, ".go42x/chunks/100-operation.tpl.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, record, "custom record\n")
	if _, err := service.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := string(readFile(t, record)); got != "custom record\n" {
		t.Fatalf("partial installation replaced the record: %q", got)
	}

	// A build without a published revision cannot be fetched later, so it records nothing.
	other := t.TempDir()
	service = testService(t, other, false)
	service.source = nil
	writeFile(t, filepath.Join(other, ".go42x", bundleSourceFile), "stale record\n")
	if _, err := service.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(other, ".go42x", bundleSourceFile)); !os.IsNotExist(err) {
		t.Fatalf("development build kept a stale record: %v", err)
	}
}

func TestUpdateMergesWithFetchedBundle(t *testing.T) {
	for name, next := range map[string]*buildinfo.Source{
		"release": {
			Repository: "https://example.com/go42x.git",
			Ref:        "v1.2.3",
		},
		"development build": nil,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			service := testService(t, dir, false)
			if _, err := service.Init(t.Context()); err != nil {
				t.Fatal(err)
			}
			previous := bundleRepository(t, map[string]string{
				"chunks/100-operation.tpl.md": "Previous operation chunk\n",
				"chunks/090-retired.tpl.md":   "Retired chunk\n",
			})
			data, err := marshalBundleSource(previous)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dir, ".go42x", bundleSourceFile), string(data))
			writeFile(t, filepath.Join(dir, ".go42x/chunks/090-retired.tpl.md"), "Retired chunk\n")
			writeFile(t, filepath.Join(dir, ".go42x/chunks/300-codestyle.tpl.md"), "Customized style\n")
			service.source = next

			result, err := service.Update(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if result.Applied == 0 {
				t.Fatalf("Update() = %+v; want applied changes", result)
			}
			if got := string(
				readFile(t, filepath.Join(dir, ".go42x/chunks/300-codestyle.tpl.md")),
			); got != "Customized style\n" {
				t.Errorf("update lost a project change: %q", got)
			}
			if _, err := os.Stat(filepath.Join(dir, ".go42x/chunks/090-retired.tpl.md")); !os.IsNotExist(err) {
				t.Errorf("update kept a file that the release retired: %v", err)
			}
			record := filepath.Join(dir, ".go42x", bundleSourceFile)
			if next == nil {
				if _, err := os.Stat(record); !os.IsNotExist(err) {
					t.Errorf("development build kept the previous record: %v", err)
				}
			} else if recorded, err := parseBundleSource(readFile(t, record)); err != nil ||
				!reflect.DeepEqual(recorded, next) {
				t.Errorf("bundle record = %+v, %v; want %+v", recorded, err, next)
			}
			workspaces, err := filepath.Glob(filepath.Join(dir, ".build/agentenv-update-*"))
			if err != nil || len(workspaces) != 0 {
				t.Errorf("merge workspaces remain: %v, %v", workspaces, err)
			}
		})
	}
}

func TestUpdateRequiresUsablePreviousBundle(t *testing.T) {
	for name, record := range map[string]string{
		"missing record":   "",
		"invalid record":   "repository: ext::sh\nref: v1.0.0\n",
		"unknown ref":      "",
		"no configuration": "",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			service := testService(t, dir, false)
			if _, err := service.Init(t.Context()); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "unknown ref":
				previous := bundleRepository(t, nil)
				previous.Ref = "v9.9.9"
				data, err := marshalBundleSource(previous)
				if err != nil {
					t.Fatal(err)
				}
				record = string(data)
			case "no configuration":
				repository, git := gitRepository(t, map[string]string{
					bundleSourceDir + "/agents.tpl.md": "Previous template\n",
				})
				data, err := marshalBundleSource(&buildinfo.Source{
					Repository: repository,
					Ref:        git("rev-parse", "HEAD"),
				})
				if err != nil {
					t.Fatal(err)
				}
				record = string(data)
			}
			if len(record) > 0 {
				writeFile(t, filepath.Join(dir, ".go42x", bundleSourceFile), record)
			}
			before := snapshotTree(t, dir)

			result, err := service.Update(t.Context())
			if err == nil || result == nil || result.Applied != 0 {
				t.Fatalf("Update() = %+v, %v; want a failure before application", result, err)
			}
			if name == "missing record" && !strings.Contains(err.Error(), "agentenv init --force") {
				t.Errorf("missing record error does not name the recovery: %v", err)
			}
			if !reflect.DeepEqual(before, snapshotTree(t, dir)) {
				t.Fatal("failed update changed the project")
			}
			if _, err := os.Stat(filepath.Join(dir, ".go42x", config.FileProject)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
