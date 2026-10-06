package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestGeneratedDirectoriesArePrivate(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory permission bits are not supported on Windows")
	}
	p := newProject(t)
	p.configure(t)
	p.run(t, 0, "agentenv", "generate")
	p.run(t, 0, "kwb", "build")
	for _, path := range []string{".claude", ".codex", ".agents", ".agents/plugins", ".agents/plugins/project-tools", ".go42x/kwb/index"} {
		info, err := os.Stat(filepath.Join(p.root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			t.Errorf("generated directory %s is accessible to other users: %s", path, info.Mode())
		}
	}
}

func TestAgentEnvLifecycle(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.write(t, ".gitignore", "user-entry\n")
	p.configure(t)
	if !json.Valid([]byte(p.read(t, ".go42x/go42x.schema.json"))) {
		t.Fatal("init did not install a valid JSON schema")
	}
	p.write(t, ".go42x/custom.md", "User instructions\n")
	p.write(t, ".claude/agents/custom.md", "User agent\n")
	preserved := map[string]string{
		"GEMINI.md":                         "Legacy user instructions",
		".gemini/settings.json":             "Legacy settings left untouched",
		".agents/plugins/other/plugin.json": `{"name":"other","description":"User plugin"}`,
		".agents/plugins/project-tools/skills/custom/SKILL.md": "User skill",
	}
	for path, content := range preserved {
		p.write(t, path, content)
	}
	beforeInit := p.snapshot(t)
	p.run(t, 0, "agentenv", "init")
	p.assertUnchanged(t, beforeInit)

	settings := map[string]string{
		".claude/settings.local.json":                   `{"user_setting":{"theme":"dark"},"permissions":{"deny":["Read(private)"]}}`,
		".agents/plugins/project-tools/plugin.json":     `{"name":"old-name","description":"Old metadata","$schema":"old-schema"}`,
		".agents/plugins/project-tools/mcp_config.json": `{"user_setting":{"theme":"dark"},"mcpServers":{"stale":{"command":"old"}}}`,
		".crush.json":        `{"user_setting":{"theme":"dark"},"lsp":{"go":{"command":"custom-gopls"}}}`,
		".mcp.json":          `{"user_setting":{"theme":"dark"}}`,
		".codex/config.toml": "model = 'custom-model'\nsandbox_mode = 'read-only'\napproval_policy = 'on-request'\n",
	}
	for path, content := range settings {
		p.write(t, path, content)
	}
	p.run(t, 0, "agentenv", "generate")
	if !strings.Contains(p.read(t, "AGENTS.md"), "# example-e2e\n") {
		t.Fatal("generate did not render the project's instructions")
	}
	if _, err := os.Stat(filepath.Join(p.root, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("generated obsolete Claude wrapper: %v", err)
	}
	for path, content := range preserved {
		if p.read(t, path) != content {
			t.Errorf("generation changed %s", path)
		}
	}
	for path := range settings {
		var values map[string]any
		content := []byte(p.read(t, path))
		if strings.HasSuffix(path, ".toml") {
			if err := toml.Unmarshal(content, &values); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{
				"model": "custom-model", "sandbox_mode": "read-only", "approval_policy": "on-request",
			} {
				assertSetting(t, values, key, want)
			}
			assertSetting(t, values, "mcp_servers.local.command", "go42x")
		} else {
			if err := json.Unmarshal(content, &values); err != nil {
				t.Fatal(err)
			}
			if path != ".agents/plugins/project-tools/plugin.json" {
				assertSetting(t, values, "user_setting.theme", "dark")
			}
			switch path {
			case ".claude/settings.local.json":
				assertSetting(t, values, "permissions.deny", []any{"Read(private)"})
				assertSetting(t, values, "enabledMcpjsonServers", []any{"local"})
			case ".crush.json":
				assertSetting(t, values, "lsp.go.command", "custom-gopls")
				assertSetting(t, values, "mcp.local.command", "go42x")
			case ".agents/plugins/project-tools/plugin.json":
				assertSetting(t, values, "name", "project-tools")
				assertSetting(t, values, "$schema", "https://antigravity.google/schemas/v1/plugin.json")
			case ".agents/plugins/project-tools/mcp_config.json":
				assertSetting(t, values, "mcpServers.local.command", "go42x")
				if len(values["mcpServers"].(map[string]any)) != 1 {
					t.Error("Antigravity retained stale MCP servers")
				}
			case ".mcp.json":
				assertSetting(t, values, "mcpServers.local.command", "go42x")
			}
		}
		// Every replaced settings file must have a recoverable original.
		backups, err := filepath.Glob(filepath.Join(p.root, ".go42x/backups/settings", path) + ".*.bak")
		if err != nil || len(backups) != 1 {
			t.Fatalf("backups for %s: %v, %v", path, backups, err)
		}
		relative, err := filepath.Rel(p.root, backups[0])
		if err != nil {
			t.Fatal(err)
		}
		if p.read(t, relative) != settings[path] {
			t.Errorf("backup does not preserve original %s", path)
		}
	}

	preserved["CLAUDE.md"] = "User Claude instructions"
	p.write(t, "CLAUDE.md", preserved["CLAUDE.md"])
	generated := p.snapshot(t)
	p.run(t, 0, "agentenv", "generate")
	p.assertUnchanged(t, generated)
	p.run(t, 0, "agentenv", "generate", "--providers=")
	p.assertUnchanged(t, generated)
	p.write(t, ".go42x/agents.tpl.md", "# Updated {{ .project.name }}\n")
	p.run(t, 0, "agentenv", "generate", "--clean")
	if !strings.Contains(p.read(t, "AGENTS.md"), "# Updated example-e2e\n") {
		t.Fatal("clean did not regenerate instructions from the changed template")
	}
	for path := range settings {
		if p.read(t, path) != generated[filepath.FromSlash(path)].content {
			t.Errorf("clean changed user settings: %s", path)
		}
	}
	for _, path := range []string{".gitignore", ".go42x/go42x.yaml", ".go42x/custom.md", ".claude/agents/custom.md"} {
		if p.read(t, path) != generated[filepath.FromSlash(path)].content {
			t.Errorf("generation changed user source: %s", path)
		}
	}
	if p.read(t, ".go42x/agents.tpl.md") != "# Updated {{ .project.name }}\n" {
		t.Fatal("clean changed the source template")
	}
	// Disabling the last server must clear the generated MCP set while retaining the plugin.
	p.write(t, ".go42x/go42x.local.yaml", "mcp: {local: {enabled: false}}\n")
	p.run(t, 0, "agentenv", "generate")
	var mcp map[string]any
	if err := json.Unmarshal([]byte(p.read(t, ".agents/plugins/project-tools/mcp_config.json")), &mcp); err != nil {
		t.Fatal(err)
	}
	if len(mcp["mcpServers"].(map[string]any)) != 0 {
		t.Fatal("Antigravity retained a disabled server")
	}
	for path, content := range preserved {
		if p.read(t, path) != content {
			t.Errorf("cleanup changed %s", path)
		}
	}
}

func assertSetting(t *testing.T, settings map[string]any, path string, want any) {
	t.Helper()
	var value any = settings
	for _, key := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("%s does not contain an object at %s", path, key)
		}
		value = object[key]
	}
	if !reflect.DeepEqual(value, want) {
		t.Errorf("%s = %#v, want %#v", path, value, want)
	}
}

func TestGenerationFailurePreservesProject(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile("testdata/go42x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	validConfig := string(fixture)
	for _, tc := range []struct {
		name, path, content string
	}{
		{"invalid YAML", ".go42x/go42x.yaml", "version: ["},
		{
			"unsupported configuration version", ".go42x/go42x.yaml",
			strings.Replace(validConfig, `version: "1.0"`, `version: "99.0"`, 1),
		},
		{"multiple YAML documents", ".go42x/go42x.yaml", validConfig + "\n---\nproject: {name: ignored}\n"},
		{"malformed trailing YAML", ".go42x/go42x.yaml", validConfig + "\n---\nversion: [\n"},
		{"invalid JSON", ".claude/settings.local.json", `{"permissions":`},
		{"invalid Antigravity manifest", ".agents/plugins/project-tools/plugin.json", `{"name":`},
		{"invalid Antigravity MCP config", ".agents/plugins/project-tools/mcp_config.json", `{"mcpServers":`},
		{"invalid TOML", ".codex/config.toml", "model = ["},
		{"invalid template", ".go42x/agents.tpl.md", "{{ if }}"},
		{
			"invalid generated YAML", ".go42x/agents.tpl.md",
			`{{ define "context" }}value: [{{ end }}{{ yamlBlock "context" . }}`,
		},
		{
			"duplicate generated YAML keys", ".go42x/agents.tpl.md",
			"{{ define \"context\" }}value: a\nvalue: b\n{{ end }}{{ yamlBlock \"context\" . }}",
		},
		{
			"multiple generated YAML documents", ".go42x/agents.tpl.md",
			"{{ define \"context\" }}value: a\n---\nvalue: b\n{{ end }}{{ yamlBlock \"context\" . }}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			p.configure(t)
			p.run(t, 0, "agentenv", "generate")
			// Make a successful provider want to change too, catching partial writes.
			p.write(t, ".go42x/agents.tpl.md", "# Changed {{ .project.name }}\n")
			p.write(t, tc.path, tc.content)
			before := p.snapshot(t)
			for _, args := range [][]string{{"agentenv", "generate"}, {"agentenv", "generate", "--clean"}} {
				result := p.run(t, 1, args...)
				if !strings.Contains(result.stderr, "Error:") {
					t.Errorf("failure did not report an error on stderr: %q", result.stderr)
				}
				p.assertUnchanged(t, before)
			}
		})
	}
}

func TestAgentEnvUpdate(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.configure(t)
	p.write(t, ".go42x/go42x.local.yaml", "project: {name: local-project}\nproviders: {claude: {enabled: true}}\n")
	p.write(t, ".go42x/chunks/900-custom.tpl.md", "Additional project instructions")
	p.write(t, "AGENTS.md", "Original instructions")
	p.write(t, ".agents/plugins/project-tools/plugin.json", "Disabled provider plugin")
	p.write(t, ".go42x/chunks/100-operation.tpl.md", "Customized bundled chunk")
	originalConfig := p.read(t, ".go42x/go42x.yaml")
	result := p.run(t, 0, "agentenv", "update")
	if !strings.Contains(result.stdout, "Backup: ") || !strings.Contains(result.stdout, "regenerated") {
		t.Fatalf("missing update summary: %s", result.stdout)
	}
	backups, err := filepath.Glob(filepath.Join(p.root, ".go42x/backups/updates/*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("update backup = %v, %v", backups, err)
	}
	for path, want := range map[string]string{
		"AGENTS.md":                          "Original instructions",
		".go42x/go42x.yaml":                  originalConfig,
		".go42x/chunks/100-operation.tpl.md": "Customized bundled chunk",
	} {
		got, err := os.ReadFile(filepath.Join(backups[0], "files", filepath.FromSlash(path)))
		if err != nil || string(got) != want {
			t.Errorf("backup %s = %q, %v", path, got, err)
		}
	}
	if p.read(t, ".go42x/go42x.yaml") == originalConfig {
		t.Fatal("project configuration was not replaced")
	}
	instructions := p.read(t, "AGENTS.md")
	if !strings.Contains(instructions, "local-project") ||
		!strings.Contains(instructions, "Additional project instructions") {
		t.Fatalf("update did not regenerate instructions: %s", instructions)
	}
	if _, err := os.Stat(filepath.Join(p.root, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("update generated obsolete Claude wrapper: %v", err)
	}
	if p.read(t, ".agents/plugins/project-tools/plugin.json") != "Disabled provider plugin" {
		t.Fatal("update changed the disabled provider's plugin")
	}
	p.run(t, 0, "agentenv", "update")
	backups, err = filepath.Glob(filepath.Join(p.root, ".go42x/backups/updates/*"))
	if err != nil || len(backups) != 2 {
		t.Fatalf("repeated update did not create another backup: %v, %v", backups, err)
	}
}

func TestAgentEnvUpdateFailureBeforeApplication(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, path, content string }{
		{"invalid local configuration", ".go42x/go42x.local.yaml", "version: ["},
		{"invalid provider settings", ".claude/settings.local.json", "{"},
		{"backup failure", ".go42x/backups/updates", "blocked"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			p := newProject(t)
			p.configure(t)
			p.write(t, ".go42x/go42x.local.yaml", "providers: {claude: {enabled: true}}\n")
			p.write(t, "AGENTS.md", "Original instructions")
			p.write(t, scenario.path, scenario.content)
			before := p.snapshot(t)
			p.run(t, 1, "agentenv", "update")
			p.assertUnchanged(t, before)
		})
	}
}

func TestAgentEnvUpdateUsage(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	for _, option := range []string{"--dry-run", "--no-generate", "--keep", "--replace", "unexpected-argument"} {
		before := p.snapshot(t)
		p.run(t, 2, "agentenv", "update", option)
		p.assertUnchanged(t, before)
	}
	p.run(t, 1, "agentenv", "update")
}
