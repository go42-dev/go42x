package mcp

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
)

// clientSession reports a client name the way an initialized MCP session does.
type clientSession struct {
	name string
}

func (s *clientSession) Initialize()       {}
func (s *clientSession) Initialized() bool { return true }
func (s *clientSession) SessionID() string { return "test" }

func (s *clientSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return make(chan mcp.JSONRPCNotification, 1)
}

func (s *clientSession) GetClientInfo() mcp.Implementation {
	return mcp.Implementation{Name: s.name}
}

func (s *clientSession) SetClientInfo(mcp.Implementation) {}
func (s *clientSession) GetClientCapabilities() mcp.ClientCapabilities {
	return mcp.ClientCapabilities{}
}
func (s *clientSession) SetClientCapabilities(mcp.ClientCapabilities) {}
func (s *clientSession) SetLogLevel(mcp.LoggingLevel)                 {}
func (s *clientSession) GetLogLevel() mcp.LoggingLevel                { return mcp.LoggingLevelInfo }

// fixedEnv reads env instead of the process environment, which differs between the
// shells and agents that run the tests.
func fixedEnv(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, found := env[name]
		return value, found
	}
}

// toolsByName indexes the adapter's tools by name.
func toolsByName(adapter *Adapter) map[string]server.ServerTool {
	tools := map[string]server.ServerTool{}
	for _, tool := range adapter.Tools() {
		tools[tool.Tool.Name] = tool
	}
	return tools
}

func call[T any](
	t *testing.T,
	ctx context.Context,
	tools map[string]server.ServerTool,
	name string,
	arguments map[string]any,
) T {
	t.Helper()
	request := mcp.CallToolRequest{}
	request.Params.Arguments = arguments
	result, err := tools[name].Handler(ctx, request)
	if err != nil || result.IsError {
		t.Fatalf("%s: %+v %v", name, result, err)
	}

	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestJournalTools(t *testing.T) {
	t.Parallel()
	settings := journal.NewSettings()
	settings.RootPath = t.TempDir()
	service, err := journal.NewService(settings)
	if err != nil {
		t.Fatal(err)
	}

	adapter := New(service)
	adapter.lookupEnv = fixedEnv(nil)
	tools := map[string]server.ServerTool{}
	for _, tool := range adapter.Tools() {
		if tool.Tool.OutputSchema.Type != "object" {
			t.Errorf("%s has no output schema", tool.Tool.Name)
		}
		readOnly := tool.Tool.Annotations.ReadOnlyHint != nil && *tool.Tool.Annotations.ReadOnlyHint
		if want := tool.Tool.Name == "journal_list" || tool.Tool.Name == "journal_show" ||
			tool.Tool.Name == "journal_search"; readOnly != want {
			t.Errorf("%s read-only = %v, want %v", tool.Tool.Name, readOnly, want)
		}
		tools[tool.Tool.Name] = tool
	}

	ctx := server.NewMCPServer("test", "1").WithContext(t.Context(), &clientSession{name: "claude-code"})
	created := call[journal.EntryResult](t, ctx, tools, "journal_new", map[string]any{
		"title":   "Tool task",
		"tags":    []any{"feature", "MCP"},
		"session": "s-1",
	})
	if created.Entry.Runtime != "claude" || created.Entry.Session != "s-1" || len(created.Entry.Tags) != 2 {
		t.Fatalf("created = %+v", created.Entry)
	}
	id := created.Entry.ID

	noted := call[journal.EntryResult](
		t,
		t.Context(),
		tools,
		"journal_note",
		map[string]any{"id": id, "summary": "Progress"},
	)
	if noted.Entry.Runtime != journal.RuntimeUnknown {
		t.Errorf("note without a client session recorded %q", noted.Entry.Runtime)
	}

	listed := call[journal.ListResult](
		t,
		ctx,
		tools,
		"journal_list",
		map[string]any{"status": "open", "tags": []any{"mcp"}},
	)
	if len(listed.Entries) != 1 || listed.Entries[0].ID != id {
		t.Errorf("listed = %+v", listed)
	}
	found := call[journal.SearchResult](t, ctx, tools, "journal_search", map[string]any{"query": "tool"})
	if len(found.Matches) != 1 {
		t.Errorf("search = %+v", found)
	}

	finished := call[journal.EntryResult](t, ctx, tools, "journal_done", map[string]any{"id": id, "keep": true})
	if finished.Entry.Status != journal.StatusDone || finished.Entry.ExpiresAt != nil {
		t.Errorf("done = %+v", finished.Entry)
	}
	shown := call[journal.ShowResult](t, ctx, tools, "journal_show", map[string]any{"id": id})
	if len(shown.Events) != 3 || shown.Events[2].Event != "done" {
		t.Errorf("events = %+v", shown.Events)
	}

	for name, arguments := range map[string]map[string]any{
		"journal_show": {"id": "../outside"},
		"journal_new":  {"title": "Bad tags", "tags": "not-a-list"},
		"journal_done": {"id": id},
	} {
		request := mcp.CallToolRequest{}
		request.Params.Arguments = arguments
		result, err := tools[name].Handler(ctx, request)
		if err != nil || !result.IsError {
			t.Errorf("%s accepted %v: %+v %v", name, arguments, result, err)
		}
	}
}

func TestJournalToolsSession(t *testing.T) {
	t.Parallel()
	env := map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "c-env"}
	for _, tt := range []struct {
		name      string
		client    string
		arguments map[string]any
		want      journal.Actor
	}{
		{
			name:   "Claude Code without a session argument",
			client: "claude-code",
			want:   journal.Actor{Runtime: "claude", Session: "c-env"},
		},
		{
			name:      "session argument",
			client:    "claude-code",
			arguments: map[string]any{"session": "s-1"},
			want:      journal.Actor{Runtime: "claude", Session: "s-1"},
		},
		{
			name:   "another runtime",
			client: "codex-mcp-client",
			want:   journal.Actor{Runtime: "codex"},
		},
		{
			name: "no client session",
			want: journal.Actor{Runtime: journal.RuntimeUnknown},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := journal.NewSettings()
			settings.RootPath = t.TempDir()
			service, err := journal.NewService(settings)
			if err != nil {
				t.Fatal(err)
			}
			adapter := New(service)
			adapter.lookupEnv = fixedEnv(env)
			tools := toolsByName(adapter)

			ctx := t.Context()
			if len(tt.client) > 0 {
				ctx = server.NewMCPServer("test", "1").WithContext(ctx, &clientSession{name: tt.client})
			}
			arguments := map[string]any{"title": "Session task"}
			maps.Copy(arguments, tt.arguments)
			created := call[journal.EntryResult](t, ctx, tools, "journal_new", arguments)
			if got := (journal.Actor{Runtime: created.Entry.Runtime, Session: created.Entry.Session}); got != tt.want {
				t.Errorf("journal_new recorded %+v, want %+v", got, tt.want)
			}

			arguments = map[string]any{"id": created.Entry.ID, "summary": "Progress"}
			maps.Copy(arguments, tt.arguments)
			noted := call[journal.EntryResult](t, ctx, tools, "journal_note", arguments)
			if got := (journal.Actor{Runtime: noted.Entry.Runtime, Session: noted.Entry.Session}); got != tt.want {
				t.Errorf("journal_note recorded %+v, want %+v", got, tt.want)
			}
		})
	}
}
