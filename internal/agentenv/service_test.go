package agentenv

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/buildinfo"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/vendoring"
)

func testService(t *testing.T, dir string, clean bool) *Service {
	t.Helper()
	t.Chdir(dir)
	s, err := NewAgentEnvService(&Settings{Clean: clean})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestServiceSettingsAndLogger(t *testing.T) {
	if _, err := NewAgentEnvService(nil); err == nil {
		t.Fatal("nil settings accepted")
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	s, err := NewAgentEnvService(&Settings{}, WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir())
	if _, err := s.Init(t.Context()); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(logs.String(), "Initializing agentenv") ||
		!strings.Contains(logs.String(), "component=agentenv") {
		t.Fatalf("custom logger did not receive component logs: %s", logs.String())
	}

	s, err = NewAgentEnvService(&Settings{}, WithLogger(nil))
	if err != nil || s.logger == nil {
		t.Fatalf("default logger = %v, %v", s, err)
	}
}

func TestExportIsIndependentOfProjectConfiguration(t *testing.T) {
	dir := t.TempDir()
	svc := testService(t, dir, false)
	configPath := filepath.Join(dir, ".go42x/go42x.yaml")
	writeFile(t, configPath, "invalid: [project configuration\n")
	output := filepath.Join(dir, ".build/original bundle")

	result, err := svc.Export(t.Context(), output)
	if err != nil || result.Directory != output {
		t.Fatalf("export = %v, %v", result, err)
	}

	bundle, err := assets.AgentEnvTemplates()
	if err != nil {
		t.Fatal(err)
	}
	err = fs.WalkDir(bundle, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		data, err := fs.ReadFile(bundle, path)
		if err != nil {
			return err
		}

		got := readFile(t, filepath.Join(output, filepath.FromSlash(path)))

		if !bytes.Equal(got, pinBundledSchemaURL(path, data)) {
			t.Errorf("exported asset differs: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, configPath)) != "invalid: [project configuration\n" {
		t.Fatal("export changed project configuration")
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("export touched the project gitignore: %v", err)
	}
	if _, err := svc.Export(t.Context(), output); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing destination accepted: %v", err)
	}
}

func TestExportRejectsInvalidDestinationsAndCancellation(t *testing.T) {
	svc := testService(t, t.TempDir(), false)
	for _, path := range []string{"", "  "} {
		if _, err := svc.Export(t.Context(), path); err == nil {
			t.Errorf("empty destination accepted: %q", path)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.Export(ctx, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("export with cancelled context = %v", err)
	}
	if _, err := os.Stat("cancelled"); !os.IsNotExist(err) {
		t.Fatalf("cancelled export created output: %v", err)
	}

	writeFile(t, "original/keep.md", "keep\n")
	if err := os.Symlink("original", "export-link"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Export(t.Context(), "export-link"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("symlink destination accepted: %v", err)
	}
	if string(readFile(t, "original/keep.md")) != "keep\n" {
		t.Fatal("export changed a symlink target")
	}
}

func TestInitExtractsTemplatesAndPreservesCustomFiles(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, ".go42x/chunks/10-personality.tpl.md")
	writeFile(t, custom, "custom personality")
	writeFile(t, filepath.Join(dir, ".gitignore"), "existing-entry\n")
	localPath := filepath.Join(dir, ".go42x", config.FileLocal)
	writeFile(t, localPath, "providers: {codex: {enabled: true}}\n")
	s := testService(t, dir, false)
	if _, err := s.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, custom)) != "custom personality" {
		t.Fatal("custom template overwritten")
	}
	if _, err := config.LoadConfig(filepath.Join(dir, ".go42x/go42x.yaml")); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"modes", "workflows"} {
		if _, err := os.Stat(filepath.Join(dir, ".go42x", removed)); !os.IsNotExist(err) {
			t.Errorf("removed template directory %s was installed: %v", removed, err)
		}
	}
	schemaPath := filepath.Join(dir, ".go42x/go42x.schema.yaml")
	if _, err := os.Stat(schemaPath); !os.IsNotExist(err) {
		t.Fatalf("init installed a local editor schema: %v", err)
	}
	if !strings.Contains(string(readFile(t, filepath.Join(dir, ".go42x/go42x.yaml"))),
		"# yaml-language-server: $schema="+buildinfo.SourceURL("assets/schemas/go42x.schema.yaml")) {
		t.Fatal("default configuration must reference the remote editor schema")
	}
	templateFS, err := assets.AgentEnvTemplates()
	if err != nil {
		t.Fatal(err)
	}
	err = fs.WalkDir(templateFS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		relative := filepath.FromSlash(path)
		target := filepath.Join(dir, ".go42x", relative)

		if target == custom {
			return nil
		}

		want, err := fs.ReadFile(templateFS, path)
		if err != nil {
			return err
		}

		want = pinBundledSchemaURL(path, want)

		if !bytes.Equal(readFile(t, target), want) {
			t.Errorf("embedded template differs: %s", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before := readFile(t, filepath.Join(dir, ".gitignore"))

	if !bytes.HasPrefix(before, []byte("existing-entry\n")) {
		t.Fatal("existing gitignore changed")
	}
	if strings.Count(string(before), assets.AgentEnvGitIgnore()) != 1 {
		t.Fatal("bundled gitignore block missing or duplicated")
	}
	kwbIgnorePath := filepath.Join(dir, ".go42x/kwb.ignore")
	if _, err := os.Stat(kwbIgnorePath); !os.IsNotExist(err) {
		t.Fatalf("init created knowledge-base exclusions: %v", err)
	}
	writeFile(t, kwbIgnorePath, "# go42x -- START\ncustom/\n")
	writeFile(t, filepath.Join(dir, ".go42x/go42x.yaml"), "custom configuration")
	customTemplate := filepath.Join(dir, ".go42x/agents.tpl.md")
	writeFile(t, customTemplate, "custom instructions")
	writeFile(t, schemaPath, "outdated schema")
	manifestPath := filepath.Join(dir, ".go42x/"+vendoring.ManifestFile)
	lockPath := filepath.Join(dir, ".go42x/"+vendoring.LockFile)
	writeFile(t, manifestPath, "custom upstream manifest")
	writeFile(t, lockPath, "custom upstream lock")
	if _, err := s.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, filepath.Join(dir, ".go42x/go42x.yaml"))) != "custom configuration" {
		t.Fatal("existing config overwritten")
	}
	if string(readFile(t, customTemplate)) != "custom instructions" {
		t.Fatal("existing template overwritten")
	}
	if string(readFile(t, schemaPath)) != "outdated schema" {
		t.Fatal("existing editor schema was changed")
	}
	if string(readFile(t, manifestPath)) != "custom upstream manifest" ||
		string(readFile(t, lockPath)) != "custom upstream lock" {
		t.Fatal("repeated init changed upstream metadata")
	}
	if !bytes.Equal(before, readFile(t, filepath.Join(dir, ".gitignore"))) {
		t.Fatal("repeated init changed gitignore")
	}
	if got := string(readFile(t, kwbIgnorePath)); got != "# go42x -- START\ncustom/\n" {
		t.Fatalf("repeated init changed custom knowledge-base ignore rules: %q", got)
	}
	if string(readFile(t, localPath)) != "providers: {codex: {enabled: true}}\n" {
		t.Fatal("repeated init changed local preferences")
	}
}

func TestInitFilesystemErrors(t *testing.T) {
	for _, stage := range []string{"configuration", "templates", "gitignore"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			want := ""
			switch stage {
			case "configuration":
				writeFile(t, filepath.Join(dir, ".go42x"), "blocked")
				want = "check configuration"
			case "templates":
				writeFile(t, filepath.Join(dir, ".go42x/chunks"), "blocked")
				want = "extract template"
			case "gitignore":
				if err := os.Mkdir(filepath.Join(dir, ".gitignore"), 0755); err != nil {
					t.Fatal(err)
				}
				want = "prepare .gitignore"
			}
			if _, err := testService(
				t,
				dir,
				false,
			).Init(t.Context()); err == nil ||
				!strings.Contains(err.Error(), want) {
				t.Fatalf("Init() = %v, want %s", err, want)
			}
		})
	}
}

func TestGenerateAndCleanPreserveSources(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	dir := t.TempDir()
	s := testService(t, dir, true)
	if _, err := s.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, ".go42x/go42x.yaml")
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise generation and cleanup for every provider regardless of template defaults.
	enabled := true
	for name, p := range cfg.Providers {
		p.Enabled = &enabled
		cfg.Providers[name] = p
	}
	// HTTP endpoints make this fixture compatible with every provider.
	for name, server := range cfg.MCP {
		if server.Enabled && server.Transport() == provider.MCPServerTypeStdio {
			server.Type = provider.MCPServerTypeHTTP
			server.URL = "https://example.com/mcp"
			server.Command = ""
			server.Args = nil
			server.Container = nil
			cfg.MCP[name] = server
		}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, configPath, string(data))
	before := readFile(t, configPath)
	custom := filepath.Join(dir, ".go42x/custom.md")
	writeFile(t, custom, "preserve this")
	asset := filepath.Join(dir, ".claude/agents/custom.md")
	writeFile(t, asset, "preserve agent")
	if _, err := s.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	instructionsPath := filepath.Join(dir, "AGENTS.md")

	if len(readFile(t, instructionsPath)) == 0 {
		t.Error("empty shared instructions")
	}
	claudePath := filepath.Join(dir, "CLAUDE.md")
	if _, err := os.Stat(claudePath); !os.IsNotExist(err) {
		t.Fatalf("generated obsolete Claude wrapper: %v", err)
	}
	writeFile(t, claudePath, "User Claude instructions")
	writeFile(t, instructionsPath, "stale")
	if _, err := s.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, instructionsPath)) == "stale" {
		t.Error("stale shared instructions")
	}
	if !bytes.Equal(before, readFile(t, configPath)) || string(readFile(t, custom)) != "preserve this" ||
		string(readFile(t, asset)) != "preserve agent" {
		t.Fatal("clean removed or changed user sources")
	}
	if string(readFile(t, claudePath)) != "User Claude instructions" {
		t.Fatal("clean changed user Claude instructions")
	}
	for _, path := range []string{".claude/settings.local.json", ".mcp.json", ".agents/plugins/project-tools/plugin.json", ".agents/plugins/project-tools/mcp_config.json", ".crush.json"} {
		if len(readFile(t, filepath.Join(dir, path))) == 0 {
			t.Errorf("missing generated settings %s", path)
		}
	}
	// Optional project metadata must also work with the shipped templates.
	cfg.Project = config.Project{Name: "minimal"}
	data, err = yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Generate(t.Context()); err != nil {
		t.Fatalf("minimal project generation: %v", err)
	}
}

func TestGenerateErrors(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_ACTIONS", "false")
	dir := t.TempDir()
	s := testService(t, dir, false)
	if _, err := s.Generate(t.Context()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing config error = %v", err)
	}
	if _, err := s.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name     string
		output   string
		template string
		want     string
		clean    bool
	}{
		{
			"missing template",
			"AGENTS.md",
			"missing",
			"generation failed",
			false,
		},
		{
			"clean blocked",
			"AGENTS.md",
			"agents.tpl.md",
			"prepare instructions AGENTS.md",
			true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				Version:   "1.0",
				Project:   config.Project{Name: "test"},
				Context:   config.Context{Template: tt.template},
				Providers: map[string]config.Provider{"claude": {}},
			}
			data, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".go42x/go42x.yaml"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if tt.clean {
				writeFile(t, filepath.Join(dir, tt.output, "child"), "do not remove")
			}
			_, err = testService(t, dir, tt.clean).Generate(t.Context())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Generate() = %v, want %s", err, tt.want)
			}
		})
	}
	writeFile(t, filepath.Join(dir, ".go42x/go42x.yaml"), "invalid: [")
	if _, err := s.Generate(t.Context()); err == nil || !strings.Contains(err.Error(), "failed to load config") {
		t.Fatalf("malformed config = %v", err)
	}
}

func TestUpdateReplacesOnlyBundledSources(t *testing.T) {
	dir := t.TempDir()
	service := testService(t, dir, false)
	if _, err := service.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	local := "project:\n  name: local-project\nproviders:\n  claude:\n    enabled: true\n" +
		"    agents: [agents/reviewer.tpl.md]\n"
	writeFile(t, filepath.Join(dir, ".go42x", config.FileLocal), local)
	originals := map[string]string{
		".gitignore":                         "before\n# go42x -- START\nobsolete\n# go42x -- END\nafter\n",
		".go42x/go42x.yaml":                  "invalid old configuration: [",
		".go42x/agents.tpl.md":               "{{ if }}",
		".go42x/chunks/100-operation.tpl.md": "old operation instructions",
		".go42x/" + vendoring.ManifestFile:   "old upstream manifest",
		".go42x/" + vendoring.LockFile:       "old upstream lock",
	}
	preserved := map[string]string{
		".go42x/kwb.ignore":                         "# go42x -- START\ncustom/\n",
		".go42x/go42x.schema.json":                  "old schema",
		".go42x/go42x.local.yaml":                   local,
		".go42x/chunks/900-custom.tpl.md":           "{{ if }}",
		".go42x/agents/reviewer.tpl.md":             "Review {{ .project.name }}",
		"AGENTS.md":                                 "old shared instructions",
		".claude/settings.local.json":               "invalid existing settings: {",
		".claude/agents/reviewer.md":                "old reviewer instructions",
		".codex/config.toml":                        "invalid existing settings: [",
		".mcp.json":                                 "old MCP configuration",
		".agents/plugins/project-tools/plugin.json": "old plugin",
	}
	for path, content := range preserved {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(path)), content)
	}
	for path, content := range originals {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(path)), content)
	}
	result, err := service.Update(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range originals {
		got := string(readFile(t, filepath.Join(result.BackupDir, "files", filepath.FromSlash(path))))

		if got != want {
			t.Errorf("backup %s = %q, want %q", path, got, want)
		}
	}
	bundle, err := assets.AgentEnvTemplates()
	if err != nil {
		t.Fatal(err)
	}
	wantSources := 1 // The managed .gitignore block is an additional source.
	err = fs.WalkDir(bundle, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		wantSources++
		want, err := fs.ReadFile(bundle, path)
		if err != nil {
			return err
		}

		want = pinBundledSchemaURL(path, want)
		if got := readFile(t, filepath.Join(dir, ".go42x", filepath.FromSlash(path))); string(got) != string(want) {
			t.Errorf("bundled source was not replaced: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Sources != wantSources || result.Applied != result.Sources {
		t.Fatalf("update result = %+v, want %d sources", result, wantSources)
	}
	if got := string(
		readFile(t, filepath.Join(dir, ".gitignore")),
	); got != "before\n"+assets.AgentEnvGitIgnore()+"after\n" {
		t.Fatalf("managed gitignore block was not refreshed: %s", got)
	}
	for path, want := range preserved {
		if got := string(readFile(t, filepath.Join(dir, filepath.FromSlash(path)))); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
		if _, err := os.Stat(filepath.Join(result.BackupDir, "files", filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Errorf("update backed up an unchanged custom file or generated output %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("update generated obsolete Claude wrapper: %v", err)
	}
	configPath := filepath.Join(dir, ".go42x/go42x.yaml")
	updatedConfig := readFile(t, configPath)
	oldInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := service.Update(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	newInfo, err := os.Stat(configPath)
	if err != nil || os.SameFile(oldInfo, newInfo) {
		t.Fatalf("unchanged bundled source was not replaced: %v", err)
	}
	if repeated.BackupDir == result.BackupDir {
		t.Fatal("repeated update reused an earlier backup")
	}
	if got := readFile(
		t,
		filepath.Join(repeated.BackupDir, "files/.go42x/go42x.yaml"),
	); !bytes.Equal(
		got,
		updatedConfig,
	) {
		t.Fatal("repeated update did not back up unchanged bundled configuration")
	}
	if _, err := os.Stat(filepath.Join(dir, ".build")); !os.IsNotExist(err) {
		t.Fatalf("temporary staging artifacts remain: %v", err)
	}
}

func TestUpdateDoesNotReadTemplatesOrUseBuildDirectory(t *testing.T) {
	dir := t.TempDir()
	service := testService(t, dir, false)
	if _, err := service.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".build"), "not a directory")
	writeFile(t, filepath.Join(dir, ".go42x/go42x.local.yaml"),
		"context:\n  template: ../missing/main.tpl.md\n  chunks-dir: ../missing/chunks\n"+
			"providers:\n  claude:\n    enabled: true\n    agents: [../missing/reviewer.tpl.md]\n")
	if _, err := service.Update(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := string(readFile(t, filepath.Join(dir, ".build"))); got != "not a directory" {
		t.Fatalf("update changed .build: %s", got)
	}
	for _, path := range []string{"AGENTS.md", ".claude", ".mcp.json"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Errorf("update generated %s: %v", path, err)
		}
	}
}

func TestUpdateRequiresInitialization(t *testing.T) {
	service := testService(t, t.TempDir(), false)
	if _, err := service.Update(t.Context()); err == nil || !strings.Contains(err.Error(), "agentenv init") {
		t.Fatalf("update without init = %v", err)
	}
}

func TestUpdateRejectsInvalidProviderConfiguration(t *testing.T) {
	for _, local := range []string{
		"providers: {typo: {enabled: false}}\n",
		"providers: {codex: {approval-policy: invalid}}\n",
		"providers: {copilot: {auto-approve-tools: []}}\n",
	} {
		t.Run(local, func(t *testing.T) {
			root := t.TempDir()
			service := testService(t, root, false)
			if _, err := service.Init(t.Context()); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(root, ".go42x", config.FileLocal), local)
			writeFile(t, filepath.Join(root, ".go42x/agents.tpl.md"), "existing instructions\n")
			before := snapshotTree(t, root)

			result, err := service.Update(t.Context())
			if err == nil || !strings.Contains(err.Error(), "validate updated configuration") || result != nil {
				t.Fatalf("Update() = %+v, %v; want validation failure", result, err)
			}
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("invalid provider configuration caused source or backup writes")
			}
		})
	}
}
