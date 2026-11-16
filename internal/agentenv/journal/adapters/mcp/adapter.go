package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
)

type Adapter struct {
	service *journal.Service
}

func New(service *journal.Service) *Adapter {
	return &Adapter{
		service: service,
	}
}

func (s *Adapter) Name() string {
	return "journal"
}

func (s *Adapter) Tools() []server.ServerTool {
	session := mcp.WithString(
		"session",
		mcp.MaxLength(200),
		mcp.Description("Your session identifier, when your runtime reports one"),
	)
	return []server.ServerTool{
		{
			Tool: mcp.NewTool(
				"journal_list",
				mcp.WithDescription(
					"List journal entries in .go42x/journal, most recently updated first. Check open entries before starting a task and continue the one that matches it.",
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithString(
					"status",
					mcp.Enum(string(journal.StatusOpen), string(journal.StatusDone), string(journal.StatusAbandoned)),
					mcp.Description("Only entries with this status"),
				),
				mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("Only entries with every listed tag")),
				mcp.WithOutputSchema[journal.ListResult](),
			),
			Handler: s.list,
		},
		{
			Tool: mcp.NewTool(
				"journal_show",
				mcp.WithDescription("Read a journal entry with its notes, log, and files."),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithString("id", mcp.Required(), mcp.Description("Entry ID, as journal_list returns it")),
				mcp.WithOutputSchema[journal.ShowResult](),
			),
			Handler: s.show,
		},
		{
			Tool: mcp.NewTool(
				"journal_new",
				mcp.WithDescription(
					"Create an open journal entry for a task, with an entry.json record and a notes.md template. Keep the task's working files in the returned directory and handoff notes in its notes.md.",
				),
				mcp.WithString(
					"title",
					mcp.Required(),
					mcp.MinLength(1),
					mcp.MaxLength(200),
					mcp.Description("What the task is about"),
				),
				mcp.WithArray(
					"tags",
					mcp.WithStringItems(),
					mcp.Description("One type of work and the affected areas: lowercase letters, digits, and hyphens"),
				),
				mcp.WithString("pr", mcp.Description("Pull request number or URL")),
				mcp.WithString("issue", mcp.Description("Issue number or URL")),
				session,
				mcp.WithOutputSchema[journal.EntryResult](),
			),
			Handler: s.create,
		},
		{
			Tool: mcp.NewTool(
				"journal_note",
				mcp.WithDescription(
					"Record an update after editing an entry's notes.md, with a summary for its log. A finished entry opens again.",
				),
				mcp.WithString("id", mcp.Required(), mcp.Description("Entry ID")),
				mcp.WithString("summary", mcp.MaxLength(1000), mcp.Description("What changed")),
				mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("Replacement tags")),
				mcp.WithString("pr", mcp.Description("Pull request number or URL")),
				mcp.WithString("issue", mcp.Description("Issue number or URL")),
				session,
				mcp.WithOutputSchema[journal.EntryResult](),
			),
			Handler: s.note,
		},
		{
			Tool: mcp.NewTool(
				"journal_done",
				mcp.WithDescription(
					"Finish an open journal entry. Finished entries expire after 30 days, abandoned ones after 7, and kept ones never.",
				),
				mcp.WithString("id", mcp.Required(), mcp.Description("Entry ID")),
				mcp.WithString("summary", mcp.MaxLength(1000), mcp.Description("Outcome")),
				mcp.WithBoolean("abandoned", mcp.Description("The task was dropped without finishing it")),
				mcp.WithBoolean("keep", mcp.Description("Finish without an expiry")),
				session,
				mcp.WithOutputSchema[journal.EntryResult](),
			),
			Handler: s.done,
		},
		{
			Tool: mcp.NewTool(
				"journal_search",
				mcp.WithDescription("Search journal entries by title, ID, tags, and notes, ignoring case."),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithString("query", mcp.Required(), mcp.MinLength(1), mcp.Description("Text to find")),
				mcp.WithNumber(
					"limit",
					mcp.Min(0),
					mcp.Max(1000),
					mcp.Description("Maximum entries; default 20, 0 returns all"),
				),
				mcp.WithOutputSchema[journal.SearchResult](),
			),
			Handler: s.search,
		},
	}
}

func (s *Adapter) list(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tags, err := getStrings(request, "tags")
	if err != nil {
		return toolResult(nil, err)
	}
	return toolResult(s.service.List(ctx, journal.ListOptions{
		Status: journal.Status(request.GetString("status", "")),
		Tags:   tags,
	}))
}

func (s *Adapter) show(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return toolResult(s.service.Show(ctx, request.GetString("id", "")))
}

func (s *Adapter) create(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tags, err := getStrings(request, "tags")
	if err != nil {
		return toolResult(nil, err)
	}
	return toolResult(s.service.New(ctx, actor(ctx, request), journal.NewOptions{
		Title: request.GetString("title", ""),
		Tags:  tags,
		PR:    request.GetString("pr", ""),
		Issue: request.GetString("issue", ""),
	}))
}

func (s *Adapter) note(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tags, err := getStrings(request, "tags")
	if err != nil {
		return toolResult(nil, err)
	}
	return toolResult(s.service.Note(ctx, actor(ctx, request), journal.NoteOptions{
		ID:      request.GetString("id", ""),
		Summary: request.GetString("summary", ""),
		Tags:    tags,
		PR:      request.GetString("pr", ""),
		Issue:   request.GetString("issue", ""),
	}))
}

func (s *Adapter) done(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return toolResult(s.service.Done(ctx, actor(ctx, request), journal.DoneOptions{
		ID:        request.GetString("id", ""),
		Summary:   request.GetString("summary", ""),
		Abandoned: request.GetBool("abandoned", false),
		Keep:      request.GetBool("keep", false),
	}))
}

func (s *Adapter) search(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return toolResult(s.service.Search(ctx, journal.SearchOptions{
		Query: request.GetString("query", ""),
		Limit: request.GetInt("limit", 20),
	}))
}

// actor identifies the client that calls a tool by the name it reported during
// initialization.
func actor(ctx context.Context, request mcp.CallToolRequest) journal.Actor {
	runtime := journal.RuntimeUnknown
	if session, ok := server.ClientSessionFromContext(ctx).(server.SessionWithClientInfo); ok {
		runtime = journal.RuntimeFromClient(session.GetClientInfo().Name)
	}
	return journal.Actor{
		Runtime: runtime,
		Session: request.GetString("session", ""),
	}
}

// getStrings extracts an array of strings, returning nil when the argument is absent.
func getStrings(request mcp.CallToolRequest, name string) ([]string, error) {
	value, ok := request.GetArguments()[name]
	if !ok {
		return nil, nil
	}

	switch value := value.(type) {
	case []string:
		return value, nil
	case []any:
		result := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be an array of strings", name)
			}

			result = append(result, text)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", name)
	}
}

// toolResult wraps a service result, reporting failures as MCP tool errors.
func toolResult(value any, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultStructuredOnly(value), nil
}
