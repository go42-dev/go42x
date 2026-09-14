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
	for _, path := range []string{".claude", ".codex", ".gemini", ".go42x/kwb/index"} {
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
	beforeInit := p.snapshot(t)
	p.run(t, 0, "agentenv", "init")
	p.assertUnchanged(t, beforeInit)

	settings := map[string]string{
		".claude/settings.local.json": `{"user_setting":{"theme":"dark"},"permissions":{"deny":["Read(private)"]}}`,
		".gemini/settings.json":       `{"user_setting":{"theme":"dark"},"privacy":{"usageStatisticsEnabled":true}}`,
		".crush.json":                 `{"user_setting":{"theme":"dark"},"lsp":{"go":{"command":"custom-gopls"}}}`,
		".mcp.json":                   `{"user_setting":{"theme":"dark"}}`,
		".codex/config.toml":          "model = 'custom-model'\nsandbox_mode = 'read-only'\napproval_policy = 'on-request'\n",
	}
	for path, content := range settings {
		p.write(t, path, content)
	}
	p.run(t, 0, "agentenv", "generate")
	if !strings.Contains(p.read(t, "AGENTS.md"), "# example-e2e\n") {
		t.Fatal("generate did not render the project's instructions")
	}
	for _, path := range []string{"CLAUDE.md", "GEMINI.md"} {
		if !strings.Contains(p.read(t, path), "@AGENTS.md") {
			t.Errorf("%s does not import shared instructions", path)
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
			assertSetting(t, values, "user_setting.theme", "dark")
			switch path {
			case ".claude/settings.local.json":
				assertSetting(t, values, "permissions.deny", []any{"Read(private)"})
				assertSetting(t, values, "enabledMcpjsonServers", []any{"local"})
			case ".crush.json":
				assertSetting(t, values, "lsp.go.command", "custom-gopls")
				assertSetting(t, values, "mcp.local.command", "go42x")
			case ".gemini/settings.json":
				assertSetting(t, values, "privacy.usageStatisticsEnabled", true)
				assertSetting(t, values, "mcpServers.local.command", "go42x")
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

	generated := p.snapshot(t)
	p.run(t, 0, "agentenv", "generate")
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
		{"invalid TOML", ".codex/config.toml", "model = ["},
		{"invalid template", ".go42x/agents.tpl.md", "{{ if }}"},
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
