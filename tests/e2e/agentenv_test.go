package e2e_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/go42-dev/go42x/assets"
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

func TestAgentEnvUpstream(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	// Init and update must use the bundle even when downloads cannot work.
	p.env = append(p.env, "HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1")
	p.configure(t)
	p.write(t, ".go42x/agents.tpl.md", "# {{ .project.name }}\n\n{{ .chunks }}\n")
	p.write(t, ".go42x/go42x.local.yaml", "context: {chunks-dir: chunks/}\n")
	templates, err := assets.AgentEnvTemplates()
	if err != nil {
		t.Fatal(err)
	}
	documents := []string{
		"upstream/go/code-review-comments.md",
		"upstream/go/package-names.md",
		"upstream/go/effective-go.md",
		"upstream/google-go/index.md",
		"upstream/google-go/guide.md",
		"upstream/google-go/decisions.md",
		"upstream/google-go/best-practices.md",
	}
	for _, path := range documents {
		want, err := fs.ReadFile(templates, path)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.read(t, ".go42x/"+path); got == "" || got != string(want) {
			t.Errorf("init did not install the bundled reference %s", path)
		}
	}
	lock, err := fs.ReadFile(templates, ".vendor.lock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.read(t, ".go42x/.vendor.lock.yaml"); got != string(lock) {
		t.Fatal("init did not install the bundled upstream lock")
	}
	p.run(t, 0, "vendor", "touch", ".build/schema-reference")
	schemaHeader := strings.SplitN(p.read(t, ".build/schema-reference/.vendor.yaml"), "\n", 2)[0]
	manifest := p.read(t, ".go42x/.vendor.yaml")
	if !strings.Contains(manifest, schemaHeader+"\n") {
		t.Fatalf("bundled manifest schema differs from this binary's schema reference: %s", manifest)
	}
	p.run(t, 0, "vendor", "check", "--manifest", ".go42x/.vendor.yaml")
	p.run(t, 0, "agentenv", "generate")
	instructions := p.read(t, "AGENTS.md")
	if !strings.Contains(instructions, "# Go Code Style\n") {
		t.Fatal("generation omitted the Go style chunk")
	}
	for _, path := range documents {
		if !strings.Contains(instructions, ".go42x/"+path) {
			t.Errorf("generated instructions do not point to %s", path)
		}
		if strings.Contains(instructions, p.read(t, ".go42x/"+path)) {
			t.Errorf("generation inserted the full reference %s into the instructions", path)
		}
	}
	const reference = ".go42x/upstream/go/code-review-comments.md"
	const custom = ".go42x/upstream/custom.md"
	p.write(t, reference, "Customized reference\n")
	p.write(t, custom, "Additional project reference\n")
	p.write(t, ".go42x/.vendor.yaml", "Customized upstream manifest\n")
	p.write(t, ".go42x/.vendor.lock.yaml", "Customized upstream lock\n")
	const projectManifest = "version: \"1.0\"\ndependencies: {}\n"
	p.write(t, ".vendor.yaml", projectManifest)
	beforeInit := p.snapshot(t)
	p.run(t, 0, "agentenv", "init")
	p.assertUnchanged(t, beforeInit)
	p.run(t, 0, "agentenv", "update")
	want, err := fs.ReadFile(templates, "upstream/go/code-review-comments.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.read(t, reference); got != string(want) {
		t.Error("update did not restore the bundled reference")
	}
	if got := p.read(t, custom); got != "Additional project reference\n" {
		t.Error("update changed an additional project reference")
	}
	if got := p.read(t, ".vendor.yaml"); got != projectManifest {
		t.Fatal("update changed the project's own vendoring manifest")
	}
	if got := p.read(t, ".go42x/.vendor.yaml"); got != manifest {
		t.Fatal("update did not restore the bundled upstream manifest")
	}
	if got := p.read(t, ".go42x/.vendor.lock.yaml"); got != string(lock) {
		t.Fatal("update did not restore the bundled upstream lock")
	}
	backups, err := filepath.Glob(
		filepath.Join(p.root, ".go42x/backups/updates/*/files", filepath.FromSlash(reference)),
	)
	if err != nil || len(backups) != 1 {
		t.Fatalf("reference backups = %v, %v", backups, err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil || string(backup) != "Customized reference\n" {
		t.Errorf("reference backup = %q, %v", backup, err)
	}
	if got := p.read(t, "AGENTS.md"); got != instructions {
		t.Error("update regenerated instructions")
	}
	for path, want := range map[string]string{
		".go42x/.vendor.yaml":      "Customized upstream manifest\n",
		".go42x/.vendor.lock.yaml": "Customized upstream lock\n",
	} {
		metadataBackups, err := filepath.Glob(
			filepath.Join(p.root, ".go42x/backups/updates/*/files", filepath.FromSlash(path)),
		)
		if err != nil || len(metadataBackups) != 1 {
			t.Fatalf("upstream metadata backups for %s = %v, %v", path, metadataBackups, err)
		}
		data, err := os.ReadFile(metadataBackups[0])
		if err != nil || string(data) != want {
			t.Errorf("upstream metadata backup %s = %q, %v", path, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(p.root, ".go42x/.vendor.schema.yaml")); !os.IsNotExist(err) {
		t.Fatalf("installed a local vendor schema: %v", err)
	}
	p.run(t, 0, "vendor", "check", "--manifest", ".go42x/.vendor.yaml")
}

func TestAgentEnvLifecycle(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.write(t, ".gitignore", "user-entry\n")
	p.configure(t)
	if _, err := os.Stat(filepath.Join(p.root, ".go42x/go42x.schema.yaml")); !os.IsNotExist(err) {
		t.Fatalf("init installed a local editor schema: %v", err)
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

func TestAntigravityHeaderEnvironment(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.configure(t)
	const local = `mcp:
  github:
    enabled: true
    name: github
    type: http
    url: https://api.githubcopilot.com/mcp/
    headers:
      Authorization: "Bearer ${AGY_TEST_TOKEN}"
      X-Label: "$AGY_TEST_LABEL"
      X-MCP-Toolsets: all
`
	p.write(t, ".go42x/go42x.local.yaml", local)
	p.env = append(p.env, "AGY_TEST_LABEL=example", "AGY_TEST_TOKEN=")
	before := p.snapshot(t)
	result := p.run(t, 1, "agentenv", "generate")
	if !strings.Contains(result.stderr, `environment variable "AGY_TEST_TOKEN" is unset or empty`) {
		t.Fatalf("missing variable error: %s", result.stderr)
	}
	p.assertUnchanged(t, before)
	for _, token := range []string{"test-token-one", "test-token-two"} {
		p.env[len(p.env)-1] = "AGY_TEST_TOKEN=" + token
		p.run(t, 0, "agentenv", "generate")
		var native, copilot map[string]any
		if err := json.Unmarshal(
			[]byte(p.read(t, ".agents/plugins/project-tools/mcp_config.json")),
			&native,
		); err != nil {
			t.Fatal(err)
		}
		assertSetting(t, native, "mcpServers.github.headers.Authorization", "Bearer "+token)
		assertSetting(t, native, "mcpServers.github.headers.X-Label", "example")
		assertSetting(t, native, "mcpServers.github.headers.X-MCP-Toolsets", "all")
		if err := json.Unmarshal([]byte(p.read(t, ".mcp.json")), &copilot); err != nil {
			t.Fatal(err)
		}
		assertSetting(t, copilot, "mcpServers.github.headers.Authorization", "Bearer ${AGY_TEST_TOKEN}")
		if p.read(t, ".go42x/go42x.local.yaml") != local {
			t.Fatal("generation changed the authored header references")
		}
		generated := p.snapshot(t)
		p.run(t, 0, "agentenv", "generate")
		p.assertUnchanged(t, generated)
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
		{
			"missing Antigravity header variable", ".go42x/go42x.local.yaml",
			`mcp: {remote: {enabled: true, name: remote, type: http, url: "https://example.com/mcp", headers: {Authorization: "Bearer private-header-prefix ${AGY_TEST_MISSING_TOKEN}"}}}`,
		},
		{
			"unsupported Antigravity header reference", ".go42x/go42x.local.yaml",
			`mcp: {remote: {enabled: true, name: remote, type: http, url: "https://example.com/mcp", headers: {Authorization: "Bearer ${AGY_TEST_TOKEN:-private-header-prefix}"}}}`,
		},
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
				if strings.Contains(result.stderr, "private-header-prefix") {
					t.Error("generation error exposed a header value")
				}
				p.assertUnchanged(t, before)
			}
		})
	}
}

func TestAgentEnvDoesNotCreateKnowledgeBaseExclusions(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.configure(t)
	p.run(t, 0, "agentenv", "update")
	if _, err := os.Stat(filepath.Join(p.root, ".go42x", "kwb.ignore")); !os.IsNotExist(err) {
		t.Fatalf("agentenv created knowledge-base exclusions: %v", err)
	}
}

func TestAgentEnvUpdate(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.configure(t)
	p.write(t, ".go42x/go42x.local.yaml", "project: {name: local-project}\nproviders: {claude: {enabled: true}}\n")
	preserved := map[string]string{
		".go42x/go42x.local.yaml":                   p.read(t, ".go42x/go42x.local.yaml"),
		".go42x/kwb.ignore":                         "# go42x -- START\ncustom/\n",
		".go42x/chunks/900-custom.tpl.md":           "Additional project instructions",
		"AGENTS.md":                                 "Original instructions",
		".claude/settings.local.json":               `{"user_setting":"preserved"}`,
		".agents/plugins/project-tools/plugin.json": "Existing provider plugin",
	}
	for path, content := range preserved {
		p.write(t, path, content)
	}
	p.env = append(p.env, "GO42X_PROVIDERS=unknown-provider")
	originalGitIgnore := "before\n# go42x -- START\nobsolete\n# go42x -- END\nafter\n"
	p.write(t, ".gitignore", originalGitIgnore)
	p.write(t, ".go42x/chunks/100-operation.tpl.md", "Customized bundled chunk")
	originalConfig := p.read(t, ".go42x/go42x.yaml")
	result := p.run(t, 0, "agentenv", "update")
	templates, err := assets.AgentEnvTemplates()
	if err != nil {
		t.Fatal(err)
	}
	sourceCount := 1 // The managed .gitignore block.
	if err := fs.WalkDir(templates, ".", func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			sourceCount++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.stdout, "Backup: ") ||
		!strings.Contains(result.stdout, fmt.Sprintf("Updated %d source files.", sourceCount)) ||
		!strings.Contains(result.stdout, "Run go42x agentenv generate") {
		t.Fatalf("missing update summary: %s", result.stdout)
	}
	backups, err := filepath.Glob(filepath.Join(p.root, ".go42x/backups/updates/*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("update backup = %v, %v", backups, err)
	}
	for path, want := range map[string]string{
		".gitignore":                         originalGitIgnore,
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
	gitignore := p.read(t, ".gitignore")
	if !strings.HasPrefix(gitignore, "before\n# go42x -- START\n") ||
		!strings.HasSuffix(gitignore, "# go42x -- END\nafter\n") ||
		strings.Contains(gitignore, "obsolete") || !strings.Contains(gitignore, ".go42x/backups/\n") {
		t.Fatalf("update did not replace the managed gitignore block: %s", gitignore)
	}
	for path, want := range preserved {
		if got := p.read(t, path); got != want {
			t.Errorf("update changed %s: %q, want %q", path, got, want)
		}
		if _, err := os.Stat(filepath.Join(backups[0], "files", filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Errorf("update backed up an unchanged custom file or generated output %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(p.root, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("update generated obsolete Claude wrapper: %v", err)
	}
	for _, path := range []string{".build", ".mcp.json", ".codex"} {
		if _, err := os.Stat(filepath.Join(p.root, path)); !os.IsNotExist(err) {
			t.Errorf("update created %s: %v", path, err)
		}
	}
	p.run(t, 0, "agentenv", "update")
	backups, err = filepath.Glob(filepath.Join(p.root, ".go42x/backups/updates/*"))
	if err != nil || len(backups) != 2 {
		t.Fatalf("repeated update did not create another backup: %v, %v", backups, err)
	}
	p.run(t, 0, "agentenv", "generate", "--providers=claude")
	instructions := p.read(t, "AGENTS.md")
	if !strings.Contains(instructions, "local-project") ||
		!strings.Contains(instructions, "Additional project instructions") {
		t.Fatalf("explicit generation did not use the updated sources: %s", instructions)
	}
}

func TestAgentEnvUpdateFailureBeforeApplication(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, path, content string }{
		{"invalid local configuration", ".go42x/go42x.local.yaml", "version: ["},
		{"unknown local field", ".go42x/go42x.local.yaml", "providers: {claude: {enable: true}}"},
		{"invalid effective configuration", ".go42x/go42x.local.yaml", "context: {template: null}"},
		{"incompatible configuration", ".go42x/go42x.local.yaml",
			"providers: {claude: {enabled: true}}\nmcp: {gopls: {cwd: src}}\n"},
		{"malformed gitignore block", ".gitignore", "# go42x -- START\nuser rules\n"},
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

func TestAgentEnvUpdateDoesNotReadProviderSettingsOrTemplates(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.configure(t)
	p.write(t, ".build", "not a directory")
	p.write(t, ".go42x/go42x.local.yaml",
		"providers: {claude: {enabled: true}}\n"+
			"context: {template: ../missing.tpl.md, chunks-dir: ../missing-chunks}\n")
	preserved := map[string]string{
		".claude/settings.local.json":     "{",
		".go42x/chunks/900-custom.tpl.md": "{{ if }}",
		".build":                          "not a directory",
	}
	for path, content := range preserved {
		p.write(t, path, content)
	}
	p.run(t, 0, "agentenv", "update")
	for path, want := range preserved {
		if got := p.read(t, path); got != want {
			t.Errorf("update changed %s: %q, want %q", path, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(p.root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("update generated instructions: %v", err)
	}
}

func TestAgentEnvUpdateUsage(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	for _, option := range []string{
		"--dry-run", "--no-generate", "--providers=codex", "--keep", "--replace", "unexpected-argument",
	} {
		before := p.snapshot(t)
		p.run(t, 2, "agentenv", "update", option)
		p.assertUnchanged(t, before)
	}
	p.run(t, 1, "agentenv", "update")
}
