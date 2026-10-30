package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGoCacheIdentity(t *testing.T) {
	data, err := os.ReadFile("../../.github/actions/setup-go/action.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Runs struct {
			Steps []struct {
				ID  string
				Run string
			}
		}
	}
	if err := yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range action.Runs.Steps {
		if step.ID == "metadata" {
			script = step.Run
		}
	}
	if len(script) == 0 {
		t.Fatal("missing Go cache metadata script")
	}

	root := filepath.Join(t.TempDir(), "checkout with spaces")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		args = append([]string{
			"-c", "core.hooksPath=/dev/null",
			"-c", "user.name=Cache Test",
			"-c", "user.email=cache@example.invalid",
			"-c", "commit.gpgsign=false",
		}, args...)
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture: %v\n%s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "--quiet")
	git("commit", "--quiet", "--allow-empty", "-m", "initial source")

	run := func(t *testing.T, changes map[string]string, wantError bool) map[string]string {
		t.Helper()
		outputPath := filepath.Join(t.TempDir(), "output")
		env := map[string]string{
			"GOTOOLCHAIN":               "local",
			"GOCACHE":                   filepath.Join(root, "build cache"),
			"GOMODCACHE":                filepath.Join(root, "module cache"),
			"GITHUB_OUTPUT":             outputPath,
			"GITHUB_SHA":                strings.Repeat("f", 40),
			"PROJECT_CACHE_ENABLED":     "true",
			"PROJECT_CACHE_SCOPE":       "tests",
			"PROJECT_CACHE_OS":          "Linux",
			"PROJECT_CACHE_ARCH":        "X64",
			"PROJECT_GO_VERSION":        "1.27.3",
			"PROJECT_DEPENDENCIES_HASH": "dependencies-v1",
			"ImageOS":                   "ubuntu24",
		}
		for name, value := range changes {
			env[name] = value
		}
		cmd := exec.CommandContext(t.Context(), "bash", "-euo", "pipefail", "-c", script)
		cmd.Dir = root
		cmd.Env = os.Environ()
		for name, value := range env {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
		output, err := cmd.CombinedOutput()
		if wantError {
			if err == nil {
				t.Fatal("invalid cache scope unexpectedly succeeded")
			}
			return nil
		}
		if err != nil {
			t.Fatalf("read Go cache metadata: %v\n%s", err, output)
		}
		content, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatal(err)
		}
		values := make(map[string]string)
		lines := strings.Split(string(content), "\n")
		for i := 0; i < len(lines); i++ {
			if name, delimiter, ok := strings.Cut(lines[i], "<<"); ok {
				var value []string
				for i++; i < len(lines) && lines[i] != delimiter; i++ {
					value = append(value, lines[i])
				}
				values[name] = strings.Join(value, "\n")
			} else if name, value, ok := strings.Cut(lines[i], "="); ok {
				values[name] = value
			}
		}
		return values
	}

	original := run(t, nil, false)
	if !strings.HasSuffix(original["build-key"], "-"+git("rev-parse", "HEAD")) {
		t.Fatal("build cache does not identify the checked-out commit")
	}
	if original["go-build"] != filepath.Join(root, "build cache") ||
		original["go-mod"] != filepath.Join(root, "module cache") {
		t.Fatal("cache paths with spaces were not preserved")
	}

	for _, tt := range []struct {
		name        string
		changes     map[string]string
		sameModules bool
		reuseBuild  bool
	}{
		{name: "workload scope", changes: map[string]string{"PROJECT_CACHE_SCOPE": "nilaway"}, sameModules: true},
		{name: "dependencies", changes: map[string]string{"PROJECT_DEPENDENCIES_HASH": "dependencies-v2"}, reuseBuild: true},
		{name: "Go version", changes: map[string]string{"PROJECT_GO_VERSION": "1.28.0"}},
		{name: "OS", changes: map[string]string{"PROJECT_CACHE_OS": "macOS"}},
		{name: "architecture", changes: map[string]string{"PROJECT_CACHE_ARCH": "ARM64"}},
		{name: "runner image", changes: map[string]string{"ImageOS": "ubuntu26"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changed := run(t, tt.changes, false)
			if same := changed["modules-key"] == original["modules-key"]; same != tt.sameModules {
				t.Fatalf("shared module cache compatibility = %t, want %t", same, tt.sameModules)
			}
			if changed["build-key"] == original["build-key"] {
				t.Fatal("changed build requirements reused the exact compilation cache")
			}
			reusable := false
			for _, prefix := range strings.Split(changed["build-restore"], "\n") {
				reusable = reusable || strings.HasPrefix(original["build-key"], prefix)
			}
			if reusable != tt.reuseBuild {
				t.Fatalf("build fallback compatibility = %t, want %t", reusable, tt.reuseBuild)
			}
		})
	}

	git("commit", "--quiet", "--allow-empty", "-m", "changed source")
	changed := run(t, nil, false)
	if changed["modules-key"] != original["modules-key"] || changed["build-key"] == original["build-key"] {
		t.Fatal("source change did not refresh only the compilation cache")
	}
	if !strings.HasPrefix(original["modules-key"], changed["modules-restore"]) {
		t.Fatal("module fallback does not match the previous archive")
	}
	if !strings.HasPrefix(original["build-key"], strings.Split(changed["build-restore"], "\n")[0]) {
		t.Fatal("source change cannot reuse the previous compilation cache")
	}
	run(t, map[string]string{"PROJECT_CACHE_SCOPE": "invalid/scope"}, true)
	disabled := run(
		t,
		map[string]string{"PROJECT_CACHE_ENABLED": "false", "PROJECT_CACHE_SCOPE": "invalid/scope"},
		false,
	)
	if len(disabled["modules-key"]) > 0 || len(disabled["build-key"]) > 0 || len(disabled["go-build"]) == 0 {
		t.Fatal("disabled caching must expose paths without preparing cache keys")
	}
}
