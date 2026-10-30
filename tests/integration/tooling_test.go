package integration_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestToolCacheFallbacks(t *testing.T) {
	data, err := os.ReadFile("../../.github/actions/setup-tools/action.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Runs struct {
			Steps []struct {
				ID  string `yaml:"id"`
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range action.Runs.Steps {
		if step.ID == "mise-config" {
			script = step.Run
		}
	}
	if len(script) == 0 {
		t.Fatal("missing tools cache identity script")
	}

	run := func(t *testing.T, changes map[string]string) (string, []string) {
		t.Helper()
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "etc"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(root, "etc", "mise.toml"),
			[]byte("min_version = \"2026.10.21\"\n"),
			0600,
		); err != nil {
			t.Fatal(err)
		}
		goVersion := "1.27.3"
		if version, exists := changes["GO_VERSION"]; exists {
			goVersion = version
			delete(changes, "GO_VERSION")
		}
		goMod := fmt.Appendf(nil, "module example.com/cache\ngo %s\n", goVersion)
		if requirement, exists := changes["GO_REQUIREMENT"]; exists {
			goMod = fmt.Appendf(goMod, "\nrequire example.com/dependency %s\n", requirement)
			delete(changes, "GO_REQUIREMENT")
		}
		if err := os.WriteFile(
			filepath.Join(root, "go.mod"),
			goMod,
			0600,
		); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{
			"PATH":                       os.Getenv("PATH"),
			"PROJECT_INSTALL_ARGS":       "",
			"PROJECT_SETUP_TASK":         "setup",
			"CACHE_PREFIX":               "project-tools-v3",
			"PROJECT_CACHE_OS":           "Linux",
			"PROJECT_CACHE_ARCHITECTURE": "X64",
			"ImageOS":                    "ubuntu24",
			"TOOLS_HASH":                 strings.Repeat("a", 64),
			"SETUP_HASH":                 strings.Repeat("c", 64),
			"GITHUB_OUTPUT":              filepath.Join(root, "outputs"),
		}
		for key, value := range changes {
			env[key] = value
		}
		cmd := exec.CommandContext(t.Context(), "bash", "-c", script)
		cmd.Dir = root
		for key, value := range env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cache identity: %v\n%s", err, output)
		}
		outputs, err := os.ReadFile(env["GITHUB_OUTPUT"])
		if err != nil {
			t.Fatal(err)
		}
		var key string
		var fallbacks []string
		for _, line := range strings.Split(string(outputs), "\n") {
			if value, found := strings.CutPrefix(line, "cache-key="); found {
				key = value
			}
			if strings.HasPrefix(line, "project-tools-v3-") {
				fallbacks = append(fallbacks, line)
			}
		}
		if len(key) == 0 || len(key) > 512 || len(fallbacks) != 2 {
			t.Fatalf("invalid cache outputs: %s", outputs)
		}
		return key, fallbacks
	}

	original, _ := run(t, nil)
	for _, tt := range []struct {
		name    string
		changes map[string]string
		match   int
		sameKey bool
	}{
		{
			name:    "setup change",
			changes: map[string]string{"SETUP_HASH": strings.Repeat("d", 64)},
			match:   0,
		},
		{
			name:    "tool change",
			changes: map[string]string{"TOOLS_HASH": strings.Repeat("d", 64)},
			match:   1,
		},
		{
			name:    "Go version boundary",
			changes: map[string]string{"GO_VERSION": "1.27.2"},
			match:   -1,
		},
		{
			name:    "setup selections share tools",
			changes: map[string]string{"PROJECT_SETUP_TASK": "setup:gh-aw-description"},
			match:   0,
			sameKey: true,
		},
		{
			name: "install selections share tools",
			changes: map[string]string{
				"PROJECT_SETUP_TASK":   "",
				"PROJECT_INSTALL_ARGS": "--locked gitleaks",
			},
			match:   0,
			sameKey: true,
		},
		{
			name:    "application dependencies do not change installed tools",
			changes: map[string]string{"GO_REQUIREMENT": "v1.2.3"},
			match:   0,
			sameKey: true,
		},
		{
			name:    "architecture boundary",
			changes: map[string]string{"PROJECT_CACHE_ARCHITECTURE": "ARM64"},
			match:   -1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			key, fallbacks := run(t, tt.changes)
			if (key == original) != tt.sameKey {
				t.Fatalf("same cache key = %t, want %t", key == original, tt.sameKey)
			}
			for i, prefix := range fallbacks {
				wantMatch := tt.match >= 0 && i >= tt.match
				if strings.HasPrefix(original, prefix) != wantMatch {
					t.Fatalf("fallback %d compatibility = %t, want %t", i, !wantMatch, wantMatch)
				}
			}
		})
	}
}

func TestSharedToolCachePublisher(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/210-build-cache.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var producer struct {
		Jobs map[string]struct {
			If    string `yaml:"if"`
			Steps []struct {
				Uses string         `yaml:"uses"`
				With map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &producer); err != nil {
		t.Fatal(err)
	}
	job := producer.Jobs["tools"]
	if job.If != "github.ref == 'refs/heads/master'" {
		t.Fatal("cache publisher is not restricted to master")
	}
	var publishes int
	for _, step := range job.Steps {
		if step.Uses != "./.github/actions/setup-tools" {
			continue
		}
		publishes++
		if step.With["install-args"] != "--locked" || step.With["setup-task"] != nil {
			t.Fatal("shared tools publisher must install all locked tools")
		}
		if fmt.Sprint(step.With["cache-save"]) != "true" {
			t.Fatal("shared tools publisher does not save its archive")
		}
	}
	if publishes != 1 {
		t.Fatalf("tools publishers = %d, want 1", publishes)
	}

	files, err := filepath.Glob("../../.github/workflows/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Base(file) == "210-build-cache.yaml" {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var workflow compiledWorkflow
		if err := yaml.Unmarshal(data, &workflow); err != nil {
			t.Fatal(err)
		}
		for _, job := range workflow.Jobs {
			for _, step := range job.Steps {
				if step.Uses == "./.github/actions/setup-tools" && fmt.Sprint(step.With["cache-save"]) == "true" {
					t.Errorf("%s: consumer workflow publishes tools caches", file)
				}
			}
		}
	}
}
