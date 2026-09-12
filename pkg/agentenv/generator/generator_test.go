package generator

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/mocks"
)

func TestGeneratorContextAndProviderErrors(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	t.Setenv("AGENTENV_TEST_CONTEXT", "included")
	cfg := &config.Config{
		Version:   "1.0",
		Project:   config.Project{Name: "example"},
		EnvVars:   []string{"AGENTENV_TEST_CONTEXT"},
		Providers: map[string]config.Provider{"claude": {}, "gemini": {}, "unknown": {}},
	}
	g := NewGenerator(slog.New(slog.DiscardHandler), cfg, t.TempDir(), t.TempDir())
	ctrl := gomock.NewController(t)
	failures := []error{errors.New("claude failed"), errors.New("gemini failed")}
	for i, name := range []string{"claude", "gemini"} {
		p := mocks.NewMockproviderAccessor(ctrl)
		p.EXPECT().
			Generate(gomock.Any(), cfg.Providers[name]).
			DoAndReturn(func(data map[string]any, _ config.Provider) error {
				if data["provider"] != name {
					t.Errorf("provider context = %v, want %s", data["provider"], name)
				}
				if data["project"].(map[string]any)["name"] != "example" {
					t.Error("project context missing")
				}
				if data["environment"].(map[string]any)["AGENTENV_TEST_CONTEXT"] != "included" {
					t.Error("environment context missing")
				}
				for _, removed := range []string{"analysis", "conventions", "github_actions", "git", "mutated"} {
					if _, ok := data[removed]; ok {
						t.Errorf("unexpected context %s", removed)
					}
				}
				data["mutated"] = true
				return failures[i]
			})
		g.providers[name] = p
	}
	err := g.Generate(t.Context())
	for _, failure := range failures {
		if !errors.Is(err, failure) {
			t.Errorf("Generate() = %v, missing wrapped %v", err, failure)
		}
	}
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("unknown provider was not reported: %v", err)
	}
}

func TestGeneratorContinuesAfterProviderFailure(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	dir := t.TempDir()
	out := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "valid.tpl.md"),
		[]byte("{{ .project.name }} {{ .provider }}"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Project: config.Project{Name: "example"}, Providers: map[string]config.Provider{
		"claude": {Template: "missing", Output: "CLAUDE.md"}, "gemini": {Template: "valid.tpl.md", Output: "GEMINI.md"},
	}}
	g := NewGenerator(slog.New(slog.DiscardHandler), cfg, dir, out)
	if err := g.Generate(t.Context()); err == nil || !strings.Contains(err.Error(), "provider claude") {
		t.Fatalf("Generate() = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(out, "GEMINI.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "example gemini" {
		t.Fatalf("successful provider output = %q", data)
	}
}
