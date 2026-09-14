package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/pkg/doctor/check"
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
		{name: "environment overrides local", local: true, env: new("claude,gemini"), want: []string{"claude", "gemini"}},
		{name: "flag overrides invalid environment", local: true, env: new("typo"), flags: []string{"--providers=gemini"}, want: []string{"gemini"}},
		{name: "empty flag", local: true, env: new("claude"), flags: []string{"--providers="}},
		{name: "empty environment", local: true, env: new("")},
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
				"claude": ".claude/settings.local.json", "codex": ".codex/config.toml", "gemini": ".gemini/settings.json",
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
	for _, value := range []string{"typo", "codex,codex", "codex,", ",codex"} {
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
	p := newProject(t)
	p.configure(t)
	p.run(t, 0, "agentenv", "generate")
	p.write(t, ".go42x/go42x.local.yaml", "providers: {codex: {approval-policy: secret-invalid-policy}}\n")
	before := p.snapshot(t)
	p.run(t, 1, "agentenv", "generate", "--clean", "--providers=codex")
	result := p.run(t, 1, "doctor", "--providers=codex", "--json")
	if strings.Contains(result.stdout+result.stderr, "secret-invalid-policy") {
		t.Fatal("doctor printed invalid local configuration contents")
	}
	p.assertUnchanged(t, before)
}
