package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/go42-dev/go42x/pkg/kwb"
	kwbmcp "github.com/go42-dev/go42x/pkg/kwb/adapters/mcp"
)

type testToolset struct {
	name  string
	tools []server.ServerTool
}

func (s testToolset) Name() string { return s.name }

func (s testToolset) Tools() []server.ServerTool { return s.tools }

func probeTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("project_info"),
		Handler: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("project ready"), nil
		},
	}
}

func connect(t *testing.T, runtime *Server) (*client.Client, *mcp.InitializeResult) {
	t.Helper()
	c, err := client.NewInProcessClient(runtime.server)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	request := mcp.InitializeRequest{}
	request.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	request.Params.ClientInfo = mcp.Implementation{
		Name:    "test",
		Version: "1",
	}
	result, err := c.Initialize(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	return c, result
}

func TestServerIdentityOptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
		want mcp.Implementation
	}{
		{"defaults", nil, mcp.Implementation{
			Name:    "go42x",
			Version: "dev",
		}},
		{"custom", []Option{WithName("project"), WithVersion("1.2.3")}, mcp.Implementation{
			Name:    "project",
			Version: "1.2.3",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, err := New(tc.opts...)
			if err != nil {
				t.Fatal(err)
			}
			c, result := connect(t, runtime)
			if !reflect.DeepEqual(result.ServerInfo, tc.want) {
				t.Fatalf("identity = %+v, want %+v", result.ServerInfo, tc.want)
			}
			list, err := c.ListTools(t.Context(), mcp.ListToolsRequest{})
			if err != nil || len(list.Tools) != 0 {
				t.Fatalf("expected no tools by default: list=%+v error=%v", list, err)
			}
		})
	}
	for _, opt := range []Option{WithName(""), WithVersion("")} {
		if _, err := New(opt); err == nil {
			t.Fatal("empty identity override was accepted")
		}
	}
}

type failingReader struct {
	err error
}

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestTransportUsesLoggerOption(t *testing.T) {
	var logs, output bytes.Buffer
	runtime, err := New(WithLogger(slog.New(slog.NewTextHandler(&logs, nil)).With("component", "mcp-test")))
	if err != nil {
		t.Fatal(err)
	}
	readErr := errors.New("test input failed")
	if err := runtime.Serve(t.Context(), failingReader{
		readErr,
	}, &output); !errors.Is(err, readErr) {
		t.Fatalf("Serve error = %v, want %v", err, readErr)
	}
	for _, want := range []string{"level=ERROR", "component=mcp-test", readErr.Error()} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("logger output %q does not contain %q", logs.String(), want)
		}
	}
	if output.Len() != 0 {
		t.Fatalf("logs leaked into protocol output: %q", output.String())
	}
	for _, opts := range [][]Option{nil, {WithLogger(nil)}} {
		runtime, err := New(opts...)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.Serve(t.Context(), failingReader{
			readErr,
		}, io.Discard); !errors.Is(err, readErr) {
			t.Fatalf("Serve with default logger: %v", err)
		}
	}
}

func TestAllRegisteredToolsetsAreAvailable(t *testing.T) {
	settings := kwb.NewSettings()
	settings.IndexPath = filepath.Join(t.TempDir(), "missing-index")
	service, err := kwb.NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	runtime, err := New(WithVersion("test"))
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []toolsetAccessor{kwbmcp.New(service), testToolset{
		"project",
		[]server.ServerTool{probeTool()},
	}} {
		if err := runtime.AddToolset(group); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := connect(t, runtime)
	list, err := c.ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(list.Tools))
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	want := []string{
		"docs_get",
		"docs_impact",
		"kwb_get_file",
		"kwb_list_files",
		"kwb_search",
		"kwb_stats",
		"project_context",
		"project_info",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
	for _, name := range []string{"kwb_search", "kwb_stats"} {
		request := mcp.CallToolRequest{}
		request.Params.Name = name
		if name == "kwb_search" {
			request.Params.Arguments = map[string]any{"query": "hello"}
		}
		result, err := c.CallTool(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError || !strings.Contains(result.Content[0].(mcp.TextContent).Text, "go42x kwb build --index") {
			t.Fatalf("expected actionable missing-index error: %+v", result)
		}
	}
	request := mcp.CallToolRequest{}
	request.Params.Name = "project_info"
	result, err := c.CallTool(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || result.Content[0].(mcp.TextContent).Text != "project ready" {
		t.Fatalf("independent tool failed: %+v", result)
	}
}

func TestKnowledgeBaseStats(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"README.md", "example.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("example content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	settings := kwb.NewSettings()
	settings.IndexPath = filepath.Join(t.TempDir(), "index")
	service, err := kwb.NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	// A server starts with no open index, so exercise opening it on the first call.
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.AddToolset(kwbmcp.New(service)); err != nil {
		t.Fatal(err)
	}
	c, _ := connect(t, runtime)
	request := mcp.CallToolRequest{}
	request.Params.Name = "kwb_stats"
	result, err := c.CallTool(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.StructuredContent == nil {
		t.Fatalf("expected structured statistics with text fallback: %+v", result)
	}
	structured, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result.Content[0])
	}
	for _, data := range [][]byte{structured, []byte(text.Text)} {
		var stats struct {
			DocumentCount uint64 `json:"document_count"`
			IndexPath     string `json:"index_path"`
		}
		if err := json.Unmarshal(data, &stats); err != nil {
			t.Fatal(err)
		}
		indexPath, err := filepath.EvalSymlinks(settings.IndexPath)
		if err != nil {
			t.Fatal(err)
		}
		if stats.DocumentCount != 3 || stats.IndexPath != indexPath {
			t.Fatalf("unexpected index statistics: %+v", stats)
		}
	}
}

func TestRegistrationRejectsAmbiguousTools(t *testing.T) {
	group := testToolset{
		"project",
		[]server.ServerTool{probeTool()},
	}
	for _, tc := range []struct {
		name   string
		groups []toolsetAccessor
		want   string
	}{
		{"duplicate group", []toolsetAccessor{group, group}, "duplicate toolset"},
		{"duplicate tool across groups", []toolsetAccessor{group, testToolset{
			"other",
			group.tools,
		}}, "duplicate tool"},
		{"duplicate tool within group", []toolsetAccessor{testToolset{
			"project",
			[]server.ServerTool{probeTool(), probeTool()},
		}}, "duplicate tool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, err := New()
			if err == nil {
				for _, group := range tc.groups {
					if err = runtime.AddToolset(group); err != nil {
						break
					}
				}
			}
			if err == nil {
				err = runtime.Serve(t.Context(), strings.NewReader(""), io.Discard)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestServeCancellationReachesActiveTools(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	tool := probeTool()
	tool.Handler = func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return mcp.NewToolResultError(ctx.Err().Error()), nil
	}
	runtime, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.AddToolset(testToolset{
		"project",
		[]server.ServerTool{tool},
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	input, writer := io.Pipe()
	defer func() {
		if err := input.Close(); err != nil {
			t.Error(err)
		}
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- runtime.Serve(ctx, input, io.Discard) }()
	go func() {
		_, _ = io.WriteString(
			writer,
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`+"\n"+
				`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"project_info","arguments":{}}}`+"\n",
		)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
	select {
	case <-finished:
	default:
		t.Fatal("server returned before its tool finished")
	}
}

func TestDocumentationAndContextToolsWithoutIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "guides"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "guides/auth.md"),
		[]byte(
			"---\nid: auth\ntitle: Tokens\ncollection: handbook\nsidebar_position: 1\n---\n# Tokens\n[Code](../src/auth.go)\n",
		),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	settings := kwb.NewSettings()
	settings.RootPath = root
	settings.IndexPath = filepath.Join(root, ".go42x/kwb/index")
	knowledge, err := kwb.NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	defer knowledge.Close() //nolint:errcheck
	runtime, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.AddToolset(kwbmcp.New(knowledge)); err != nil {
		t.Fatal(err)
	}
	c, _ := connect(t, runtime)
	listed, err := c.ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil || len(listed.Tools) != 7 {
		t.Fatalf("list=%+v %v", listed, err)
	}
	arguments := map[string]map[string]any{
		"docs_get":        {"id": "auth"},
		"docs_impact":     {"paths": []string{"src/auth.go"}},
		"project_context": {"task": "rotate tokens", "paths": []string{"src/auth.go"}},
	}
	for _, tool := range listed.Tools {
		if tool.OutputSchema.Type != "object" || tool.Annotations.ReadOnlyHint == nil ||
			!*tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool schema: %+v", tool)
		}
		args, ok := arguments[tool.Name]
		if !ok {
			continue
		}
		request := mcp.CallToolRequest{}
		request.Params.Name = tool.Name
		request.Params.Arguments = args
		result, err := c.CallTool(t.Context(), request)
		if err != nil || result.IsError || result.StructuredContent == nil || len(result.Content) != 1 {
			t.Fatalf("%s: %+v %v", tool.Name, result, err)
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var structured, fallback any
		if err = json.Unmarshal(data, &structured); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &fallback); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(structured, fallback) {
			t.Fatal("structured content differs from fallback")
		}
	}
	for _, arguments := range []map[string]any{{"id": "auth", "start_line": 1.5}, {"id": "missing"}} {
		request := mcp.CallToolRequest{}
		request.Params.Name = "docs_get"
		request.Params.Arguments = arguments
		result, err := c.CallTool(t.Context(), request)
		if err == nil && !result.IsError {
			t.Fatalf("invalid request accepted: %+v", arguments)
		}
	}
}
