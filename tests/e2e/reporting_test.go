package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/internal/agentenv"
	"github.com/go42-dev/go42x/internal/fspatch"
	"github.com/go42-dev/go42x/internal/kwb"
)

func TestResultOutputDoesNotDependOnLogLevel(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"init", "generate", "build"} {
		t.Run(operation, func(t *testing.T) {
			var outputs []string
			for _, level := range []string{"info", "error"} {
				p := newProject(t)
				command := []string{"agentenv", operation}
				switch operation {
				case "generate":
					p.configure(t)
				case "build":
					p.write(t, "guide.md", "# Guide\nExample project.\n")
					command = []string{"kwb", "build"}
				}
				result := p.run(t, 0, append(command, "--log-level="+level)...)

				if strings.Count(result.stdout, "\n") != 1 {
					t.Fatalf("expected one result line: %+v", result)
				}
				if level == "error" && len(result.stderr) > 0 {
					t.Fatalf("unexpected error log on success: %s", result.stderr)
				}
				if level == "info" && operation != "build" && len(result.stderr) == 0 {
					t.Fatal("progress logs did not reach stderr")
				}
				outputs = append(outputs, result.stdout)
			}
			if outputs[0] != outputs[1] {
				t.Fatalf("log level changed result: %q vs %q", outputs[0], outputs[1])
			}
		})
	}
}

func TestAgentEnvAndBuildJSONResults(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	init := p.run(t, 0, "agentenv", "init", "--json", "--log-level=debug")
	var initialized agentenv.InitResult
	if err := json.Unmarshal([]byte(init.stdout), &initialized); err != nil || initialized.Directory != ".go42x" {
		t.Fatalf("invalid init result: %+v, %v", init, err)
	}
	p.configure(t)
	generated := p.run(t, 0, "agentenv", "generate", "--json", "--log-level=debug")
	var generation agentenv.GenerateResult
	if err := json.Unmarshal([]byte(generated.stdout), &generation); err != nil || len(generation.Changes) == 0 {
		t.Fatalf("invalid generate result: %+v, %v", generated, err)
	}
	foundInstructions := false
	for _, change := range generation.Changes {
		if change.Path == "AGENTS.md" && change.Operation == "create" {
			foundInstructions = true
		}
		if _, err := os.Stat(filepath.Join(p.root, filepath.FromSlash(change.Path))); err != nil {
			t.Fatalf("reported output was not written: %s: %v", change.Path, err)
		}
	}
	if !foundInstructions {
		t.Fatal("generate result omitted shared instructions")
	}
	unchanged := p.run(t, 0, "agentenv", "generate", "--json")
	if err := json.Unmarshal(
		[]byte(unchanged.stdout),
		&generation,
	); err != nil || generation.Changes == nil ||
		len(generation.Changes) != 0 {
		t.Fatalf("unchanged generate reported changes: %+v, %v", unchanged, err)
	}
	built := p.run(t, 0, "kwb", "build", "--json", "--log-level=debug")
	var build kwb.BuildResult
	if err := json.Unmarshal([]byte(built.stdout), &build); err != nil || build.Files == 0 || build.Indexed == 0 {
		t.Fatalf("invalid build result: %+v, %v", built, err)
	}
	updated := p.run(t, 0, "agentenv", "update", "--json", "--log-level=debug")
	var update fspatch.UpdateResult
	if err := json.Unmarshal(
		[]byte(updated.stdout),
		&update,
	); err != nil || len(update.BackupDir) == 0 || update.Sources == 0 ||
		update.Applied == 0 {
		t.Fatalf("invalid update result: %+v, %v", updated, err)
	}
	if _, err := os.Stat(update.BackupDir); err != nil {
		t.Fatalf("reported backup is unavailable: %v", err)
	}
}

func TestRuntimeErrorsFollowSelectedFormat(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"agentenv", "generate"},
		{"agentenv", "update"},
		{"kwb", "build", "--root=missing"},
		{"kwb", "search", "query"},
		{"kwb", "read", "missing.md"},
		{"kwb", "stats"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			p := newProject(t)
			result := p.run(t, 1, append(args, "--json", "--log-level=error")...)
			var failure struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal([]byte(result.stdout), &failure); err != nil || len(failure.Error) == 0 {
				t.Fatalf("invalid JSON failure: %+v, %v", result, err)
			}
			if len(result.stderr) > 0 {
				t.Fatalf("JSON failure was repeated on stderr: %s", result.stderr)
			}
			text := p.run(t, 1, append(args, "--log-level=error")...)
			if len(text.stdout) > 0 || strings.Count(text.stderr, "Error:") != 1 {
				t.Fatalf("text failure must appear once on stderr: %+v", text)
			}
		})
	}
}
