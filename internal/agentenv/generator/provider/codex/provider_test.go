package codex

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/fspatch"
)

func TestSettingsPreserveUnmanagedConfiguration(t *testing.T) {
	t.Setenv("API_KEY", "local-secret-must-not-be-written")

	for _, tt := range []struct {
		name           string
		approvalPolicy *string
		mcpApproval    *string
		wantPolicy     string
	}{
		{
			name:       "preserve existing approval policy",
			wantPolicy: "on-request",
		},
		{
			name:           "apply explicit approval settings",
			approvalPolicy: new("never"),
			mcpApproval:    new("approve"),
			wantPolicy:     "never",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, codexSettingsDir, codexSettingsFile)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(`model = "existing-model"
approval_policy = "on-request"
[mcp_servers.stale]
command = "obsolete"
`), 0600); err != nil {
				t.Fatal(err)
			}
			settings := Settings{
				OutputDir:      root,
				ApprovalPolicy: tt.approvalPolicy,
				MCPApproval:    tt.mcpApproval,
				MCPServers: map[string]provider.MCPServer{
					"current": {
						Enabled: true,
						Command: "example",
						Env:     map[string]string{"API_KEY": "$API_KEY"},
					},
				},
			}
			logger := slog.New(slog.DiscardHandler)
			p := New(logger, settings, nil, "")
			plan := fspatch.NewPlan(logger, root, filepath.Join(root, ".build/backups"))

			if err := p.Prepare(plan, nil); err != nil {
				t.Fatal(err)
			}
			if err := plan.Apply(); err != nil {
				t.Fatal(err)
			}

			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Model          string               `toml:"model"`
				ApprovalPolicy string               `toml:"approval_policy"`
				MCPServers     map[string]MCPServer `toml:"mcp_servers"`
			}
			if err := toml.Unmarshal(content, &got); err != nil {
				t.Fatal(err)
			}
			if got.Model != "existing-model" || got.ApprovalPolicy != tt.wantPolicy {
				t.Fatalf("unmanaged settings or approval policy changed unexpectedly: %+v", got)
			}
			if len(got.MCPServers) != 1 || got.MCPServers["current"].Command != "example" {
				t.Fatalf("managed server section was not replaced: %+v", got.MCPServers)
			}
			entry := got.MCPServers["current"]
			if len(entry.EnvVars) != 1 || entry.EnvVars[0] != "API_KEY" ||
				bytes.Contains(content, []byte("local-secret-must-not-be-written")) {
				t.Fatalf("environment reference was not preserved: %s", content)
			}
			if tt.mcpApproval != nil && entry.DefaultToolsApprovalMode != *tt.mcpApproval {
				t.Fatalf("MCP approval mode = %q, want %q", entry.DefaultToolsApprovalMode, *tt.mcpApproval)
			}
			if tt.mcpApproval == nil && len(entry.DefaultToolsApprovalMode) > 0 {
				t.Fatalf("unexpected MCP approval mode %q", entry.DefaultToolsApprovalMode)
			}

			plan = fspatch.NewPlan(logger, root, filepath.Join(root, ".build/backups"))
			if err := p.Prepare(plan, nil); err != nil || len(plan.Changes()) != 0 {
				t.Fatalf("unchanged preparation = %v, changes %v", err, plan.Changes())
			}
		})
	}
}

func TestPreparationFailurePreservesConfiguration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, codexSettingsDir, codexSettingsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("[invalid TOML\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	p := New(logger, Settings{OutputDir: root}, nil, "")
	plan := fspatch.NewPlan(logger, root, filepath.Join(root, ".build/backups"))

	err := p.Prepare(plan, nil)

	if err == nil || !strings.Contains(err.Error(), "failed to parse") || len(plan.Changes()) != 0 {
		t.Fatalf("Prepare() = %v, changes %v", err, plan.Changes())
	}
	content, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(content, original) {
		t.Fatalf("failed preparation changed configuration: %q, %v", content, err)
	}
}
