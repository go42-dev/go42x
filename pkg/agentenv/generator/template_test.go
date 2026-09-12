package generator

import (
	"strings"
	"testing"
)

func TestTemplateProcessing(t *testing.T) {
	e := newTemplateEngine(t.TempDir())
	for _, tt := range []struct{ name, template, want string }{
		{"context", "Hello {{ .name }}", "Hello World"},
		{"functions", `{{ lower .name }} {{ upper "go" }} {{ trim " x " }} {{ join .items "," }}`, "world GO x a,b"},
		{"MCP Claude", `{{ mcpTool "claude" "go42x" "kwb_search" }}`, "mcp__go42x__kwb_search"},
		{"MCP Gemini", `{{ mcpTool "gemini" "go42x" "kwb_search" }}`, "mcp_go42x_kwb_search"},
		{"MCP Crush", `{{ mcpTool "crush" "go42x" "kwb_search" }}`, "mcp_go42x_kwb_search"},
		{"MCP Copilot", `{{ mcpTool "copilot" "go42x" "kwb_search" }}`, "go42x/kwb_search"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.Process(tt.template, map[string]any{"name": "World", "items": []string{"a", "b"}})
			if err != nil || got != tt.want {
				t.Fatalf("Process() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	for _, tt := range []struct{ template, want string }{{"{{", "failed to parse template"}, {"{{ missingFunction }}", "failed to parse template"}, {"{{ upper .items }}", "failed to execute template"}} {
		if _, err := e.Process(
			tt.template,
			map[string]any{"items": []string{"a"}},
		); err == nil ||
			!strings.Contains(err.Error(), tt.want) {
			t.Fatalf("Process(%q) = %v, want %s", tt.template, err, tt.want)
		}
	}
}

func TestTemplateInjection(t *testing.T) {
	e := newTemplateEngine("")
	content := "before {{ .chunks }} {{ .modes }} {{ .workflows }} after"
	content = e.InjectChunks(content, "chunk")
	content = e.InjectModes(content, "mode")
	content = e.InjectWorkflows(content, "workflow")
	if content != "before chunk mode workflow after" {
		t.Fatalf("injected = %q", content)
	}
	if got := e.InjectChunks("no placeholder", "chunk"); got != "no placeholder" {
		t.Fatalf("unexpected injection: %q", got)
	}
	if got := e.InjectChunks("{{ .chunks }}", ""); got != "" {
		t.Fatalf("empty injection: %q", got)
	}
}

func TestContextMapsAreIndependent(t *testing.T) {
	ctx := newContext(t.Context())
	ctx.Set("name", "first")
	a := ctx.ToMap()
	a["name"] = "changed"
	a["provider"] = "claude"
	b := ctx.ToMap()
	if b["name"] != "first" || b["provider"] != nil {
		t.Fatalf("provider mutated shared context: %v", b)
	}
	ctx.Set("name", "second")
	if ctx.ToMap()["name"] != "second" || a["name"] != "changed" {
		t.Fatal("context replacement leaked across snapshots")
	}
}
