package integration_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/ghaw"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/fspatch"
)

type compiledWorkflow struct {
	Jobs map[string]compiledJob `yaml:"jobs"`
}

type compiledJob struct {
	Permissions map[string]string `yaml:"permissions"`
	Steps       []compiledStep    `yaml:"steps"`
}

type compiledStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	With map[string]any    `yaml:"with"`
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
}

func TestCompiledWorkflowBoundaries(t *testing.T) {
	for _, name := range []string{"920-aw-agent", "910-aw-description"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("../..", ".github/workflows", name+".lock.yml"))
			if err != nil {
				t.Fatal(err)
			}
			var workflow compiledWorkflow
			if err := yaml.Unmarshal(data, &workflow); err != nil {
				t.Fatal(err)
			}
			agent, exists := workflow.Jobs["agent"]

			if !exists {
				t.Fatal("missing agent job")
			}
			for permission, level := range agent.Permissions {
				if level == "write" {
					t.Errorf("agent has write permission %s", permission)
				}
			}
			var order []string
			for _, step := range agent.Steps {
				if step.Name == "Checkout PR branch" {
					t.Fatal("untrusted PR checkout precedes runtime preparation")
				}
				if strings.Contains(step.Run, "AI_APP_PRIVATE_KEY") ||
					strings.Contains(step.Run, "AI_APP_CLIENT_ID") ||
					strings.Contains(fmt.Sprint(step.With, step.Env), "AI_APP_PRIVATE_KEY") {
					t.Fatal("App credentials exposed to the agent job")
				}
				if step.Uses == "./.github/actions/ai-prepare-env" {
					if fmt.Sprint(step.With["pull-mcp-image"]) != "true" {
						t.Fatalf("agent preparation does not enable MCP image pulling: %v", step.With)
					}
					if setup, exists := step.With["setup-task"]; exists && setup != "setup" {
						t.Fatalf("agent preparation overrides the full development setup: %v", setup)
					}
				}
				switch step.Name {
				case "Prepare agent environment", "Start MCP Gateway":
					order = append(order, step.Name)
				}
			}
			if !reflect.DeepEqual(
				order,
				[]string{"Prepare agent environment", "Start MCP Gateway"},
			) {
				t.Fatalf("runtime order = %v", order)
			}
			foundApp := false
			for _, job := range workflow.Jobs {
				for _, step := range job.Steps {
					if !strings.HasPrefix(step.Uses, "actions/create-github-app-token@") {
						continue
					}

					foundApp = true

					if step.With["client-id"] != "${{ vars.AI_APP_CLIENT_ID }}" ||
						step.With["private-key"] != "${{ secrets.AI_APP_PRIVATE_KEY }}" ||
						step.With["repositories"] != "${{ github.event.repository.name }}" {
						t.Fatalf("App token is not configured and scoped to this repository: %v", step.With)
					}
					if _, exists := step.With["permission-administration"]; exists {
						t.Fatal("App token requests an unconfigured administration permission")
					}
				}
			}
			if !foundApp {
				t.Fatal("safe outputs have no App token")
			}
			for _, command := range []string{"go42x", "gopls", "mise"} {
				if !strings.Contains(string(data), `"entrypoint": "`+command+`"`) {
					t.Errorf("missing native container entrypoint %s", command)
				}
			}
			if strings.Contains(string(data), "8765/mcp") || strings.Contains(string(data), "supergateway") {
				t.Fatal("compiled workflow still uses a stdio bridge")
			}

		})
	}
}

func TestMCPImageUsesLatestRelease(t *testing.T) {
	data, err := os.ReadFile("../../.github/actions/ai-prepare-env/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Inputs map[string]struct {
			Default string `yaml:"default"`
		} `yaml:"inputs"`
		Runs struct {
			Steps []compiledStep `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}

	repository := action.Inputs["mcp-image-repository"].Default

	if repository != "ghcr.io/go42-dev/go42x" {
		t.Fatalf("default MCP image repository = %q, want ghcr.io/go42-dev/go42x", repository)
	}

	var script string
	for _, step := range action.Runs.Steps {
		if step.Name == "Pull MCP tools image" {
			script = step.Run

			if step.Env["IMAGE_REPOSITORY"] != "${{ inputs.mcp-image-repository }}" {
				t.Fatalf("image repository does not come from the action input: %v", step.Env)
			}
			if step.Env["GH_TOKEN"] != "${{ github.token }}" {
				t.Fatalf("release discovery does not use the workflow token: %v", step.Env)
			}
		}
	}
	if len(script) == 0 {
		t.Fatal("missing MCP image preparation script")
	}

	for _, tt := range []struct {
		name    string
		tag     string
		wantTag string
		changes map[string]string
	}{
		{
			name:    "latest release",
			tag:     "v1.2.3",
			wantTag: "v1.2.3",
		},
		{
			name:    "custom repository",
			tag:     "v1.2.3",
			wantTag: "v1.2.3",
			changes: map[string]string{
				"IMAGE_REPOSITORY": "ghcr.io/example/tools",
			},
		},
		{
			name:    "release discovery failure",
			changes: map[string]string{"MOCK_GH_STATUS": "1"},
		},
		{
			name:    "image unavailable",
			tag:     "v1.2.3",
			changes: map[string]string{"MOCK_PULL_STATUS": "1"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, stub := range map[string]string{
				"gh": `#!/bin/bash
set -euo pipefail
[[ "$*" == 'api repos/go42-dev/go42x/releases/latest --jq .tag_name' ]] || exit 2
printf '%s\n' "$MOCK_RELEASE_TAG"
exit "$MOCK_GH_STATUS"
`,
				"docker": `#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> "$MOCK_DOCKER_LOG"
case "$*" in
  'pull '*) exit "$MOCK_PULL_STATUS" ;;
  *) exit 2 ;;
esac
`,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(stub), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			environment := map[string]string{
				"PATH":             dir,
				"IMAGE_REPOSITORY": repository,
				"MOCK_RELEASE_TAG": tt.tag,
				"MOCK_GH_STATUS":   "0",
				"MOCK_PULL_STATUS": "0",
				"MOCK_DOCKER_LOG":  filepath.Join(dir, "docker.log"),
				"GITHUB_ENV":       filepath.Join(dir, "github.env"),
			}
			maps.Copy(environment, tt.changes)
			command := exec.CommandContext(t.Context(), "bash", "-c", script)
			for key, value := range environment {
				command.Env = append(command.Env, key+"="+value)
			}

			output, err := command.CombinedOutput()

			if len(tt.wantTag) == 0 {
				if err == nil {
					t.Fatalf("image preparation unexpectedly succeeded: %s", output)
				}
				if _, err := os.ReadFile(environment["GITHUB_ENV"]); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed image preparation published an environment value: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("image preparation: %v\n%s", err, output)
			}
			published, err := os.ReadFile(environment["GITHUB_ENV"])
			if err != nil {
				t.Fatal(err)
			}
			want := "GO42X_MCP_IMAGE_VERSION=" + tt.wantTag + "\n"

			if string(published) != want {
				t.Fatalf("published environment = %q, want %q", published, want)
			}
			calls, err := os.ReadFile(environment["MOCK_DOCKER_LOG"])
			if err != nil {
				t.Fatal(err)
			}
			imageReference := environment["IMAGE_REPOSITORY"] + ":" + tt.wantTag
			wantCalls := "pull " + imageReference + "\n"

			if string(calls) != wantCalls {
				t.Fatalf("Docker calls = %q, want %q", calls, wantCalls)
			}
		})
	}
}

// TestRepositoryMCPFragment checks the current implementation against the committed shared declaration.
func TestRepositoryMCPFragment(t *testing.T) {
	root := filepath.Join("..", "..")
	cfg, err := config.LoadConfig(filepath.Join(root, ".go42x/go42x.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	logger := slog.New(slog.DiscardHandler)
	plan := fspatch.NewPlan(logger, output, filepath.Join(output, ".build/backups"))
	generator := ghaw.New(
		logger,
		nil,
		ghaw.Settings{
			OutputDir:  output,
			MCPServers: cfg.MCP,
			Bindings:   cfg.Providers[ghaw.Name].Bindings,
		},
		"",
	)
	if err := generator.Prepare(plan, nil); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(output, ghaw.MCPFile))
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(filepath.Join(root, ghaw.MCPFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, committed) {
		t.Fatal("shared MCP fragment is stale; regenerate it with the current implementation")
	}
}
