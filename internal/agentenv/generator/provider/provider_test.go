package provider_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/antigravity"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/claude"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/codex"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/copilot"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/crush"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/ghaw"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/fspatch"
)

type testProvider interface {
	Prepare(*fspatch.Plan, map[string]any) error
}

func makeProvider(
	t *testing.T,
	name string,
	cfg *config.Config,
	engine provider.TemplateEngineAccessor,
	dir, out string,
) testProvider {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	switch name {
	case claude.Name:
		return claude.New(
			logger,
			engine,
			claude.Settings{
				OutputDir:        out,
				MCPServers:       cfg.MCP,
				Agents:           cfg.Providers[name].Agents,
				AutoApproveTools: cfg.Providers[name].AutoApproveTools,
				WriteMCPFile:     !cfg.ProviderEnabled(copilot.Name),
			},
			dir,
		)
	case codex.Name:
		return codex.New(
			logger,
			engine,
			codex.Settings{
				OutputDir:          out,
				MCPServers:         cfg.MCP,
				ApprovalPolicy:     cfg.Providers[name].ApprovalPolicy,
				MCPApproval:        cfg.Providers[name].MCPApproval,
				ProjectDocMaxBytes: cfg.Providers[name].ProjectDocMaxBytes,
			},
			dir,
		)
	case antigravity.Name:
		return antigravity.New(
			logger,
			engine,
			antigravity.Settings{
				OutputDir:  out,
				MCPServers: cfg.MCP,
			},
			dir,
		)
	case crush.Name:
		return crush.New(
			logger,
			engine,
			crush.Settings{
				OutputDir:        out,
				MCPServers:       cfg.MCP,
				AutoApproveTools: cfg.Providers[name].AutoApproveTools,
			},
			dir,
		)
	case copilot.Name:
		return copilot.New(
			logger,
			engine,
			copilot.Settings{
				OutputDir:  out,
				MCPServers: cfg.MCP,
			},
			dir,
		)
	case ghaw.Name:
		return ghaw.New(
			logger,
			engine,
			ghaw.Settings{
				OutputDir:  out,
				MCPServers: cfg.MCP,
			},
			dir,
		)
	default:
		t.Fatalf("unknown provider %s", name)
		return nil
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProviderMCPLifecycle(t *testing.T) {
	for _, tt := range []struct {
		name   string
		output string
	}{
		{
			claude.Name,
			".mcp.json",
		},
		{
			codex.Name,
			".codex/config.toml",
		},
		{
			antigravity.Name,
			".agents/plugins/project-tools/mcp_config.json",
		},
		{
			crush.Name,
			".crush.json",
		},
		{
			copilot.Name,
			".mcp.json",
		},
		{
			ghaw.Name,
			ghaw.MCPFile,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := &config.Config{MCP: map[string]provider.MCPServer{
				"enabled-server": {
					Enabled: true,
					Name:    "enabled-server",
					Command: "npx",
					Args:    []string{"example-mcp"},
					Container: &provider.MCPContainer{
						Image:   "example/container:v1",
						Command: "container-mcp",
					},
				},
				"disabled-server": {
					Name:    "disabled-server",
					Command: "npx",
				},
			}}
			original, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			logger := slog.New(slog.DiscardHandler)
			p := makeProvider(t, tt.name, cfg, nil, root, root)
			plan := fspatch.NewPlan(logger, root, filepath.Join(root, ".build/backups"))
			if err := p.Prepare(plan, nil); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("Prepare wrote files before Apply: %v, %v", entries, err)
			}
			if err := plan.Apply(); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(root, tt.output))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(content, []byte("enabled-server")) ||
				bytes.Contains(content, []byte("disabled-server")) {
				t.Fatalf("generated server selection is wrong: %s", content)
			}
			if got := bytes.Contains(content, []byte("example/container:v1")); got != (tt.name == ghaw.Name) {
				t.Fatalf("container execution must only affect gh-aw: %s", content)
			}
			if tt.name != ghaw.Name && !bytes.Contains(content, []byte("npx")) {
				t.Fatalf("local stdio command was lost: %s", content)
			}
			after, err := yaml.Marshal(cfg)
			if err != nil || !bytes.Equal(original, after) {
				t.Fatalf("generation modified shared configuration: %v", err)
			}

			plan = fspatch.NewPlan(logger, root, filepath.Join(root, ".build/backups"))
			if err := p.Prepare(plan, nil); err != nil || len(plan.Changes()) != 0 {
				t.Fatalf("unchanged generation scheduled writes: %v, %v", plan.Changes(), err)
			}

			server := cfg.MCP["enabled-server"]
			server.Enabled = false
			cfg.MCP["enabled-server"] = server
			plan = fspatch.NewPlan(logger, root, filepath.Join(root, ".build/backups"))
			if err := p.Prepare(plan, nil); err != nil {
				t.Fatal(err)
			}
			if err := plan.Apply(); err != nil {
				t.Fatal(err)
			}
			content, err = os.ReadFile(filepath.Join(root, tt.output))
			if err != nil || bytes.Contains(content, []byte("enabled-server")) {
				t.Fatalf("disabled server retained in MCP configuration: %s, %v", content, err)
			}
		})
	}
}

func TestProviderGenerationErrors(t *testing.T) {
	for _, name := range []string{claude.Name, antigravity.Name, crush.Name, copilot.Name} {
		t.Run(name, func(t *testing.T) {
			dir, out := t.TempDir(), t.TempDir()
			path := map[string]string{
				claude.Name:      ".claude/settings.local.json",
				antigravity.Name: ".agents/plugins/project-tools/mcp_config.json",
				crush.Name:       ".crush.json",
				copilot.Name:     ".mcp.json",
			}[name]
			if err := os.MkdirAll(filepath.Join(out, path), 0755); err != nil {
				t.Fatal(err)
			}
			p := makeProvider(t, name, &config.Config{}, nil, dir, out)
			if err := p.Prepare(
				fspatch.NewPlan(slog.New(slog.DiscardHandler), out, filepath.Join(out, ".go42x", "backups")),
				nil,
			); err == nil ||
				!strings.Contains(err.Error(), "failed to prepare config files") {
				t.Fatalf("Prepare() = %v, want configuration error", err)
			}
		})
	}
}

func TestProviderJSONWriting(t *testing.T) {
	dir := t.TempDir()
	plan := fspatch.NewPlan(slog.New(slog.DiscardHandler), dir, filepath.Join(dir, ".go42x", "backups"))
	path := filepath.Join(dir, "nested", "config.json")
	if err := provider.PrepareJSONFile(plan, path, map[string]any{
		"enabled": true,
		"name":    "example",
	}); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if err := provider.PrepareJSONFile(plan, path, make(chan int)); err == nil {
		t.Fatal("unsupported JSON value was accepted")
	}
	if err := provider.PrepareJSONFile(plan, dir, map[string]string{}); err == nil {
		t.Fatal("JSON write error was swallowed")
	}
}

func TestPreparedOutputFiles(t *testing.T) {
	dir := t.TempDir()
	plan := fspatch.NewPlan(slog.New(slog.DiscardHandler), dir, filepath.Join(dir, ".go42x", "backups"))
	writeTestFile(t, filepath.Join(dir, "a.md"), "  alpha \n")
	if err := plan.Write(
		filepath.Join(dir, "a.md", "child"),
		[]byte("data"), fspatch.KindSettings, false,
	); err == nil ||
		!strings.Contains(err.Error(), "output parent must be a real directory") {
		t.Fatalf("prepare error = %v", err)
	}
	path := filepath.Join(dir, "new", "nested", "files.md")
	if err := plan.Write(path, []byte("rendered"), fspatch.KindSettings, false); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "rendered" {
		t.Fatalf("written output = %q, %v", data, err)
	}
}

func TestParseProviders(t *testing.T) {
	for _, tt := range []struct {
		value   string
		want    []string
		invalid bool
	}{
		{
			"",
			[]string{},
			false,
		},
		{
			" \t ",
			[]string{},
			false,
		},
		{
			"codex",
			[]string{"codex"},
			false,
		},
		{
			ghaw.Name,
			[]string{ghaw.Name},
			false,
		},
		{
			" codex, antigravity ",
			[]string{"codex", "antigravity"},
			false,
		},
		{
			"codex,",
			nil,
			true,
		},
		{
			",codex",
			nil,
			true,
		},
		{
			"codex,,claude",
			nil,
			true,
		},
		{
			"codex,codex",
			nil,
			true,
		},
		{
			"Codex",
			nil,
			true,
		},
		{
			"typo",
			nil,
			true,
		},
		{
			"gemini",
			nil,
			true,
		},
	} {
		t.Run(tt.value, func(t *testing.T) {
			got, err := provider.ParseProviders(tt.value, config.ProviderNames())
			if (err != nil) != tt.invalid || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseProviders(%q) = %#v, %v; want %#v, invalid=%v", tt.value, got, err, tt.want, tt.invalid)
			}
		})
	}
}

func TestProviderSelectionUsesSuppliedNames(t *testing.T) {
	supported := []string{"custom"}

	got, err := provider.ParseProviders(" custom ", supported)
	if err != nil || !reflect.DeepEqual(got, supported) {
		t.Fatalf("ParseProviders() = %v, %v; want %v", got, err, supported)
	}
	if err := provider.ValidateProviders([]string{codex.Name}, supported); err == nil {
		t.Fatal("accepted a provider outside the supplied list")
	}
	if err := provider.ValidateProviders([]string{"custom", "custom"}, supported); err == nil {
		t.Fatal("accepted a duplicate provider")
	}
	if err := provider.ValidateProviders([]string{" "}, supported); err == nil {
		t.Fatal("accepted a blank provider")
	}
}
