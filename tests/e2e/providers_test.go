package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/internal/doctor/check"
)

const providerProjectConfig = `version: "1.0"
project: {name: provider-selection}
context: {template: agents.tpl.md}
providers:
  claude: {enabled: true}
  codex: {enabled: false, approval-policy: on-request}
`

func TestProviderSelectionPrecedence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		local       bool
		env         *string
		flags, want []string
	}{
		{name: "shared defaults", want: []string{"claude"}},
		{name: "local overrides", local: true, want: []string{"codex"}},
		{name: "environment overrides local", local: true, env: new("claude,antigravity"), want: []string{"antigravity", "claude"}},
		{name: "flag overrides invalid environment", local: true, env: new("typo"), flags: []string{"--providers=antigravity"}, want: []string{"antigravity"}},
		{name: "empty flag", local: true, env: new("claude"), flags: []string{"--providers="}},
		{name: "empty environment", local: true, env: new("")},
		{name: "workflow fragment", local: true, flags: []string{"--providers=gh-aw"}, want: []string{"gh-aw"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			p.configure(t)
			p.write(t, ".go42x/go42x.yaml", providerProjectConfig)
			if tc.local {
				p.write(t, ".go42x/go42x.local.yaml", "providers: {claude: {enabled: false}, codex: {enabled: true}}\n")
			}
			if tc.env != nil {
				p.env = append(p.env, "GO42X_PROVIDERS="+*tc.env)
			}
			before := p.snapshot(t)
			p.run(t, 0, append([]string{"agentenv", "generate"}, tc.flags...)...)
			for provider, path := range map[string]string{
				"claude": ".claude/settings.local.json", "codex": ".codex/config.toml", "antigravity": ".agents/plugins/project-tools/mcp_config.json",
				"gh-aw": ".github/workflows/900-aw-mcp.md",
			} {
				_, err := os.Stat(filepath.Join(p.root, filepath.FromSlash(path)))
				if want := slices.Contains(tc.want, provider); want && err != nil || !want && !os.IsNotExist(err) {
					t.Errorf("provider %s output: %v; want generated=%v", provider, err, want)
				}
			}
			if !strings.Contains(p.read(t, "AGENTS.md"), "provider-selection") {
				t.Fatal("shared instructions were not generated")
			}
			for path, state := range before {
				if !state.mode.IsDir() && p.read(t, path) != state.content {
					t.Errorf("generation rewrote source %s", path)
				}
			}
			p.run(t, 0, "kwb", "build")
			before = p.snapshot(t)
			// Doctor must resolve the target checkout's local file, not the caller's.
			caller := newProject(t)
			caller.env = p.env
			result := caller.run(t, 0, append([]string{"doctor", "--root", p.root, "--json"}, tc.flags...)...)
			var report check.Report
			if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, result := range report.Checks {
				if result.ID == "agentenv.providers" {
					found = true
					if !slices.Equal(result.Evidence, tc.want) {
						t.Errorf("doctor selected %v; generate selected %v", result.Evidence, tc.want)
					}
				}
				if result.ID == "agentenv.outputs" && result.Status != check.Pass {
					t.Errorf("doctor disagrees with generation: %+v", result)
				}
			}
			if !found {
				t.Fatal("doctor omitted provider diagnostics")
			}
			p.assertUnchanged(t, before)
		})
	}
}

func TestProviderSelectionRejectsInvalidNamesWithoutWrites(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"typo", "gemini", "codex,codex", "codex,", ",codex"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			p.configure(t)
			before := p.snapshot(t)
			p.env = append(p.env, "GO42X_PROVIDERS="+value)
			p.run(t, 2, "agentenv", "generate")
			p.run(t, 2, "doctor")
			p.run(t, 2, "agentenv", "generate", "--providers", value)
			p.run(t, 2, "doctor", "--providers", value)
			p.assertUnchanged(t, before)
		})
	}
}

func TestInvalidLocalConfigurationCannotPartiallyGenerate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, local, private string }{
		{
			name:    "invalid approval policy",
			local:   "providers: {codex: {approval-policy: secret-invalid-policy}}\n",
			private: "secret-invalid-policy",
		},
		{
			name:  "unknown disabled provider",
			local: "providers: {typo: {enabled: false}}\n",
		},
		{
			name:  "retired disabled provider",
			local: "providers: {gemini: {enabled: false}}\n",
		},
		{
			name:  "unsupported option on unselected provider",
			local: "providers: {copilot: {auto-approve-tools: []}}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			p.configure(t)
			p.run(t, 0, "agentenv", "generate")
			p.write(t, ".go42x/go42x.local.yaml", tc.local)
			before := p.snapshot(t)

			p.run(t, 1, "agentenv", "generate", "--clean", "--providers=codex")
			result := p.run(t, 1, "doctor", "--providers=codex", "--json")
			if len(tc.private) > 0 && strings.Contains(result.stdout+result.stderr, tc.private) {
				t.Fatal("doctor printed invalid local configuration contents")
			}
			p.assertUnchanged(t, before)
		})
	}
}

func TestGHAWFragmentGeneration(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.configure(t)
	p.env = append(p.env, "GITHUB_TOKEN=local-token-do-not-write", "CONTEXT7_API_KEY=local-key-do-not-write")
	const local = `mcp:
  local:
    command: docker
    args: [run, --rm, -i, example/project-mcp:test, go42x, mcp]
  remote:
    enabled: true
    name: remote
    type: http
    url: https://api.example.com/mcp
    headers:
      Authorization: "Bearer ${GITHUB_TOKEN}"
      X-API-Key: "$CONTEXT7_API_KEY"
`
	p.write(t, ".go42x/go42x.local.yaml", local)
	const workflow = "---\non: pull_request\nengine: codex\nimports: [900-aw-mcp.md]\n---\nDescribe this PR.\n"
	p.write(t, ".github/workflows/910-aw-description.md", workflow)

	p.run(t, 0, "agentenv", "generate", "--providers=gh-aw")
	content := p.read(t, ".github/workflows/900-aw-mcp.md")
	var fragment map[string]any
	if err := yaml.Unmarshal(
		[]byte(strings.TrimSuffix(strings.TrimPrefix(content, "---\n"), "---\n")),
		&fragment,
	); err != nil {
		t.Fatal(err)
	}
	assertSetting(t, fragment, "mcp-servers.local.command", "docker")
	assertSetting(t, fragment, "mcp-servers.remote.headers.Authorization", "Bearer ${{ github.token }}")
	assertSetting(t, fragment, "mcp-servers.remote.headers.X-API-Key", "${{ secrets.CONTEXT7_API_KEY }}")
	assertSetting(t, fragment, "mcp-servers.remote.allowed", []any{"*"})
	if strings.Contains(content, "local-token-do-not-write") || strings.Contains(content, "local-key-do-not-write") {
		t.Fatal("fragment contains local credential values")
	}
	if p.read(t, ".github/workflows/910-aw-description.md") != workflow ||
		p.read(t, ".go42x/go42x.local.yaml") != local {
		t.Fatal("generation changed an authored workflow or configuration")
	}

	before := p.snapshot(t)
	p.run(t, 0, "agentenv", "generate", "--providers=gh-aw")
	p.assertUnchanged(t, before)
	p.run(t, 0, "agentenv", "generate", "--providers=", "--clean")
	if p.read(t, ".github/workflows/900-aw-mcp.md") != content {
		t.Fatal("disabling the provider changed its output during clean generation")
	}

	p.write(
		t,
		".go42x/go42x.local.yaml",
		strings.Replace(local, "command: docker", "type: sse\n    url: https://api.example.com/events", 1),
	)
	before = p.snapshot(t)
	result := p.run(t, 1, "agentenv", "generate", "--providers=gh-aw", "--clean")
	if !strings.Contains(result.stderr, "gh-aw supports only stdio and HTTP") {
		t.Fatalf("unsupported transport diagnostic: %s", result.stderr)
	}
	p.assertUnchanged(t, before)

	p.write(t, ".go42x/go42x.local.yaml", strings.Replace(local, "command: docker", "command: task", 1))
	before = p.snapshot(t)
	result = p.run(t, 1, "agentenv", "generate", "--providers=gh-aw")
	if !strings.Contains(result.stderr, "gh-aw cannot run this stdio command directly") {
		t.Fatalf("unsupported command diagnostic: %s", result.stderr)
	}
	p.assertUnchanged(t, before)
}
