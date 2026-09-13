package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/go42-dev/go42x/pkg/kwb"
)

func TestStructuredToolResponses(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "server.go"),
		[]byte("package example\n// NewHTTPServer starts a listener.\nfunc NewHTTPServer() {}\n"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "README.md"),
		[]byte("---\nid: server\ntitle: Server\n---\n# Server\n[Code](server.go)\n"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	settings := kwb.NewSettings()
	settings.Entrypoint = "README.md"
	settings.RootPath, settings.IndexPath = root, filepath.Join(t.TempDir(), "index")
	service, err := kwb.NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close() //nolint:errcheck
	if _, err := service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	arguments := map[string]map[string]any{
		"kwb_search":      {"query": "http server", "kind": "code", "offset": 0},
		"kwb_get_file":    {"path": "server.go", "start_line": 2, "end_line": 3},
		"kwb_list_files":  {"limit": 1, "offset": 0.0},
		"kwb_stats":       {},
		"docs_get":        {"id": "server"},
		"docs_impact":     {"paths": []string{"server.go"}},
		"project_context": {"task": "http server", "paths": []string{"server.go"}},
	}
	for _, tool := range New(service).Tools() {
		t.Run(tool.Tool.Name, func(t *testing.T) {
			if tool.Tool.OutputSchema.Type != "object" {
				t.Fatal("missing output schema")
			}
			request := mcp.CallToolRequest{}
			request.Params.Arguments = arguments[tool.Tool.Name]
			result, err := tool.Handler(t.Context(), request)
			if err != nil || result.IsError || result.StructuredContent == nil || len(result.Content) != 1 {
				t.Fatalf("result: %+v %v", result, err)
			}
			data, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var structured, fallback any
			if err := json.Unmarshal(data, &structured); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &fallback); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(structured, fallback) {
				t.Fatal("text fallback differs from structured result")
			}
			if tool.Tool.Name == "kwb_search" {
				var response kwb.SearchResponse
				if err := json.Unmarshal(data, &response); err != nil {
					t.Fatal(err)
				}
				if len(response.Results) != 1 || response.Results[0].Path != "server.go" {
					t.Fatalf("search response: %+v", response)
				}
			}
		})
	}
	for _, limit := range []any{0, -1, 1.5, "invalid", 1e30, kwb.MaxSearchLimit + 1} {
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]any{"query": "server", "limit": limit}
		result, err := New(service).searchHandler(t.Context(), request)
		if err != nil || !result.IsError {
			t.Fatalf("invalid limit %v accepted: %+v %v", limit, result, err)
		}
	}
	for _, tool := range New(service).Tools() {
		if tool.Tool.Name != "kwb_search" && tool.Tool.Name != "kwb_list_files" {
			continue
		}
		for _, offset := range []any{-1, 0.5, "invalid", 1e30} {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]any{"query": "server", "offset": offset}
			result, err := tool.Handler(t.Context(), request)
			if err != nil || !result.IsError {
				t.Fatalf("%s accepted invalid offset %v: %+v %v", tool.Tool.Name, offset, result, err)
			}
		}
	}
}
