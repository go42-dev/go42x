package cmd_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmd"
	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func executeCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	factory := cmdutil.NewFactory(t.Context())
	command := cmd.NewGo42Command(t.Context(), factory)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(args)
	err := command.Execute()
	return output.String(), err
}

func TestKnowledgeBaseDefaultsToHelp(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	// Build settings must not trigger building or validation while listing commands.
	t.Setenv("GO42X_REBUILD", "true")
	t.Setenv("GO42X_BATCH_SIZE", "0")

	help, err := executeCommand(t, "kwb", "--help")
	if err != nil {
		t.Fatal(err)
	}
	output, err := executeCommand(t, "kwb")
	if err != nil {
		t.Fatalf("kwb: %v\n%s", err, output)
	}
	if output != help {
		t.Fatalf("kwb output differs from --help:\n%s", output)
	}
	for _, command := range []string{"build", "read", "search", "stats", "check"} {
		if !strings.Contains(output, "\n  "+command+" ") {
			t.Errorf("help does not list %s:\n%s", command, output)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("help created files: %v", entries)
	}
}

func TestKnowledgeBaseBuildCommand(t *testing.T) {
	for _, mode := range []string{"defaults", "root before subcommand", "explicit index", "environment"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			index := filepath.Join(root, kwb.NewSettings().IndexPath)
			t.Chdir(t.TempDir())
			args := []string{"kwb", "build"}
			switch mode {
			case "defaults":
				t.Chdir(root)
			case "root before subcommand":
				args = []string{"kwb", "--root", root, "build"}
			case "explicit index":
				index = filepath.Join(t.TempDir(), "index")
				args = append(args, "--root", root, "--index", index)
			case "environment":
				t.Setenv("GO42X_ROOT", root)
			}
			args = append(args, "--include-ext=.xyz", "--batch-size=1")
			if err := os.WriteFile(
				filepath.Join(root, "source.xyz"),
				[]byte("orchard nectarines\n"),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			if output, err := executeCommand(t, args...); err != nil {
				t.Fatalf("build: %v\n%s", err, output)
			}
			ignorePath := filepath.Join(root, ".go42x/kwb.ignore")
			ignore, err := os.ReadFile(ignorePath)
			if err != nil ||
				string(ignore) != "# go42x knowledgebase will ignore the following files and directories\n\n" {
				t.Fatalf("default project ignore file: %q %v", ignore, err)
			}
			current := filepath.Join(index, "CURRENT")
			generation, err := os.ReadFile(current)
			if err != nil {
				t.Fatal(err)
			}
			checkOutput, err := executeCommand(t, "kwb", "check", "--index", index, "--json")
			var freshness kwb.FreshnessResult
			if err != nil || json.Unmarshal([]byte(checkOutput), &freshness) != nil || freshness.Status != "fresh" {
				t.Fatalf("check did not retain custom build settings: %s %v", checkOutput, err)
			}

			output, err := executeCommand(t, "kwb", "search", "nectarines", "--index", index, "--json")
			if err != nil {
				t.Fatalf("search: %v\n%s", err, output)
			}
			var result kwb.SearchResponse
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatal(err)
			}
			if result.Total != 1 || len(result.Results) != 1 || result.Results[0].Path != "source.xyz" {
				t.Fatalf("search did not find the indexed custom extension: %+v", result)
			}

			if output, err := executeCommand(t, args...); err != nil {
				t.Fatalf("incremental build: %v\n%s", err, output)
			}
			unchanged, err := os.ReadFile(current)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(generation, unchanged) {
				t.Fatal("unchanged build replaced the index generation")
			}

			if output, err := executeCommand(t, append(args, "--rebuild")...); err != nil {
				t.Fatalf("full rebuild: %v\n%s", err, output)
			}
			rebuilt, err := os.ReadFile(current)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(generation, rebuilt) {
				t.Fatal("--rebuild did not replace the index generation")
			}
			for _, custom := range []string{"# Local exclusions\n*.generated.js\n", ""} {
				if err := os.WriteFile(ignorePath, []byte(custom), 0600); err != nil {
					t.Fatal(err)
				}
				for _, buildArgs := range [][]string{args, append(args, "--rebuild")} {
					if output, err := executeCommand(t, buildArgs...); err != nil {
						t.Fatalf("build with custom ignore: %v\n%s", err, output)
					}
					preserved, err := os.ReadFile(ignorePath)
					if err != nil || string(preserved) != custom {
						t.Fatalf("build overwrote custom exclusions: %q %v", preserved, err)
					}
				}
			}
		})
	}
}

func TestKnowledgeBaseRejectsInvalidBuildUsage(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, args := range [][]string{
		{"kwb", "--rebuild"},
		{"kwb", "build", "unexpected"},
		{"kwb", "build", "--batch-size=0"},
	} {
		if output, err := executeCommand(t, args...); cmdutil.ExitCode(err) != 2 {
			t.Errorf("%v: expected usage error, got %v\n%s", args, err, output)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".go42x")); !os.IsNotExist(err) {
		t.Fatalf("invalid usage created index storage: %v", err)
	}
}
