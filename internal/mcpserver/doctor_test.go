package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/doctor/check"
)

func TestDoctorCheckUsesCurrentEnabledServers(t *testing.T) {
	servers := map[string]provider.MCPServer{
		"zeta": {
			Enabled: true,
			Type:    provider.MCPServerTypeHTTP,
		},
		"alpha": {
			Enabled: true,
			Type:    provider.MCPServerTypeHTTP,
		},
		"disabled": {Command: "unavailable"},
	}
	group := DoctorCheck(t.TempDir(), func() map[string]provider.MCPServer { return servers }, DoctorOptions{})
	result := group.Run(t.Context())
	var ids []string
	var statuses []check.Status
	for _, child := range result.Children {
		ids = append(ids, child.ID)
		statuses = append(statuses, child.Status)
	}
	if result.Status != check.Pass || !reflect.DeepEqual(ids, []string{
		"mcp.alpha.configuration", "mcp.alpha.probe", "mcp.zeta.configuration", "mcp.zeta.probe",
	}) || !reflect.DeepEqual(statuses, []check.Status{check.Pass, check.Skipped, check.Pass, check.Skipped}) {
		t.Fatalf("server checks: %+v", result)
	}
	servers = nil
	if result := group.Run(t.Context()); result.Status != check.Skipped || len(result.Children) != 0 {
		t.Fatalf("removed servers still checked: %+v", result)
	}
}

func TestMCPProbe(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	server := provider.MCPServer{
		Command: exe,
		Args:    []string{"-test.run=^TestDoctorMCPHelper$"},
		CWD:     t.TempDir(),
		Tools:   []string{"known"},
		Env:     map[string]string{"GO42X_DOCTOR_HELPER": "serve"},
	}
	result := probeMCP(t.Context(), server, 3*time.Second)
	if result.Status != check.Pass {
		t.Fatalf("healthy: %+v", result)
	}
	server.Tools = []string{"missing"}
	if result = probeMCP(
		t.Context(),
		server,
		3*time.Second,
	); result.Status != check.Fail ||
		len(result.Evidence) != 1 {
		t.Fatalf("missing tool: %+v", result)
	}
	server.Env["GO42X_DOCTOR_HELPER"] = "wait"
	start := time.Now()
	result = probeMCP(t.Context(), server, 100*time.Millisecond)
	if result.Status != check.Fail || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout: %+v elapsed=%s", result, time.Since(start))
	}
	server.Env["TOKEN"] = "${GO42X_MISSING_DOCTOR_TOKEN}"
	t.Setenv("GO42X_MISSING_DOCTOR_TOKEN", "")
	if result = probeMCP(t.Context(), server, time.Second); result.Status != check.Skipped {
		t.Fatalf("missing credentials: %+v", result)
	}
}

func TestDoctorMCPHelper(_ *testing.T) {
	mode := os.Getenv("GO42X_DOCTOR_HELPER")
	if mode == "" {
		return
	}
	if mode == "wait" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	// The child deliberately writes a credential-like value to stderr.
	fmt.Fprintln(os.Stderr, "secret-do-not-report")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		if request.ID == nil {
			continue
		}
		var result any
		switch request.Method {
		case "server/discover":
			if err := json.NewEncoder(os.Stdout).
				Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "method not found"}}); err != nil {
				os.Exit(4)
			}
			continue
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "fixture", "version": "1"},
			}
		case "tools/list":
			result = map[string]any{
				"tools": []any{
					map[string]any{"name": "known", "inputSchema": map[string]string{"type": "object"}},
					map[string]any{"name": "extra", "inputSchema": map[string]string{"type": "object"}},
				},
			}
		default:
			os.Exit(3)
		}
		if err := json.NewEncoder(os.Stdout).
			Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
			os.Exit(4)
		}
	}
	os.Exit(0)
}

func TestMCPRemoteProbe(t *testing.T) {
	for _, protocol := range []string{provider.MCPServerTypeHTTP, provider.MCPServerTypeSSE} {
		t.Run(protocol, func(t *testing.T) {
			runtime := server.NewMCPServer("fixture", "1", server.WithToolCapabilities(false))
			runtime.AddTool(
				mcp.NewTool("known"),
				func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					t.Error("doctor called a tool")
					return mcp.NewToolResultText("unexpected"), nil
				},
			)
			var handler http.Handler
			if protocol == provider.MCPServerTypeHTTP {
				handler = server.NewStreamableHTTPServer(runtime)
			} else {
				handler = server.NewSSEServer(runtime)
			}
			remote := httptest.NewServer(handler)
			defer remote.Close()
			url := remote.URL
			if protocol == provider.MCPServerTypeSSE {
				url += "/sse"
			}
			result := probeMCP(
				t.Context(),
				provider.MCPServer{
					Type:  protocol,
					URL:   url,
					Tools: []string{"known"},
				},
				2*time.Second,
			)
			if result.Status != check.Pass {
				t.Fatalf("remote probe: %+v", result)
			}
		})
	}
}
