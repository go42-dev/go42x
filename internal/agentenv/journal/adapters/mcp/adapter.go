package mcp

import (
	"context"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
)

type Adapter struct {
	service   *journal.Service
	lookupEnv func(string) (string, bool)
}

func New(service *journal.Service) *Adapter {
	return &Adapter{
		service:   service,
		lookupEnv: os.LookupEnv,
	}
}

func (s *Adapter) Name() string {
	return "journal"
}

func (s *Adapter) Tools() []server.ServerTool {
	session := mcp.WithString(
		"session",
		mcp.MaxLength(200),
		mcp.Description("Your session identifier; when empty, the one your runtime names in the server's environment"),
	)
	return []server.ServerTool{
		{
			Tool: mcp.NewTool(
				"journal_list",
				mcp.WithDescription(
					"List journal entries, most recently updated first. Check them before starting a task and continue the one that matches it.",
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("Only entries with every listed tag")),
				mcp.WithString(
					"since",
					mcp.Description(
						"Only entries updated at or after this RFC 3339 time, such as 2026-11-10T09:00:00Z",
					),
				),
				mcp.WithBoolean("kept", mcp.Description("Only kept entries, which gc never deletes")),
				mcp.WithString(
					"pr",
					mcp.Description(
						"Only entries for this pull request number or URL; a number also matches a URL that ends with it",
					),
				),
				mcp.WithString(
					"issue",
					mcp.Description(
						"Only entries for this issue number or URL; a number also matches a URL that ends with it",
					),
				),
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
				mcp.WithString(
					"since",
					mcp.Description("Only log events at or after this RFC 3339 time, such as 2026-11-10T09:00:00Z"),
				),
				mcp.WithBoolean(
					"sections",
					mcp.Description(
						"Return the notes split by section, without the template's comments and empty sections, instead of the whole notes",
					),
				),
				mcp.WithOutputSchema[journal.ShowResult](),
			),
			Handler: s.show,
		},
		{
			Tool: mcp.NewTool(
				"journal_attach",
				mcp.WithDescription(
					"Copy a file from the project into a journal entry, such as a report or a log. An existing file in the entry is never replaced. Record the change with journal_note.",
				),
				mcp.WithString("id", mcp.Required(), mcp.Description("Entry ID")),
				mcp.WithString(
					"path",
					mcp.Required(),
					mcp.MinLength(1),
					mcp.Description("File to copy, relative to the project root"),
				),
				mcp.WithString(
					"name",
					mcp.MaxLength(128),
					mcp.Description("File name of the copy in the entry; the file's own name when empty"),
				),
				mcp.WithOutputSchema[journal.EntryFile](),
			),
			Handler: s.attach,
		},
		{
			Tool: mcp.NewTool(
				"journal_new",
				mcp.WithDescription(
					"Create a journal entry for a task, with an entry.json record and a notes.md template. Keep the task's working files in the returned directory and handoff notes in its notes.md. Entries that go without an update for the retention period are deleted by gc unless kept.",
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
				mcp.WithBoolean("keep", mcp.Description("Keep the entry, so gc never deletes it")),
				session,
				mcp.WithOutputSchema[journal.EntryResult](),
			),
			Handler: s.create,
		},
		{
			Tool: mcp.NewTool(
				"journal_note",
				mcp.WithDescription(
					"Record an update after editing an entry's notes.md, with a summary for its log.",
				),
				mcp.WithString("id", mcp.Required(), mcp.Description("Entry ID")),
				mcp.WithString("summary", mcp.MaxLength(1000), mcp.Description("What changed")),
				mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("Replacement tags")),
				mcp.WithString("pr", mcp.Description("Pull request number or URL")),
				mcp.WithString("issue", mcp.Description("Issue number or URL")),
				mcp.WithBoolean(
					"keep",
					mcp.Description("true keeps the entry, so gc never deletes it; false lets gc delete it again"),
				),
				session,
				mcp.WithOutputSchema[journal.EntryResult](),
			),
			Handler: s.note,
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
	since, err := journal.ParseSince(request.GetString("since", ""))
	if err != nil {
		return toolResult(nil, err)
	}
	return toolResult(s.service.List(ctx, journal.ListOptions{
		Tags:  tags,
		Since: since,
		Kept:  request.GetBool("kept", false),
		PR:    request.GetString("pr", ""),
		Issue: request.GetString("issue", ""),
	}))
}

func (s *Adapter) show(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	since, err := journal.ParseSince(request.GetString("since", ""))
	if err != nil {
		return toolResult(nil, err)
	}
	return toolResult(s.service.Show(ctx, journal.ShowOptions{
		ID:       request.GetString("id", ""),
		Since:    since,
		Sections: request.GetBool("sections", false),
	}))
}

func (s *Adapter) attach(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return toolResult(s.service.Attach(ctx, journal.AttachOptions{
		ID:   request.GetString("id", ""),
		Path: request.GetString("path", ""),
		Name: request.GetString("name", ""),
	}))
}

func (s *Adapter) create(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tags, err := getStrings(request, "tags")
	if err != nil {
		return toolResult(nil, err)
	}
	return toolResult(s.service.New(ctx, s.actor(ctx, request), journal.NewOptions{
		Title: request.GetString("title", ""),
		Tags:  tags,
		PR:    request.GetString("pr", ""),
		Issue: request.GetString("issue", ""),
		Keep:  request.GetBool("keep", false),
	}))
}

func (s *Adapter) note(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tags, err := getStrings(request, "tags")
	if err != nil {
		return toolResult(nil, err)
	}
	options := journal.NoteOptions{
		ID:      request.GetString("id", ""),
		Summary: request.GetString("summary", ""),
		Tags:    tags,
		PR:      request.GetString("pr", ""),
		Issue:   request.GetString("issue", ""),
	}
	if keep, ok := request.GetArguments()["keep"].(bool); ok {
		options.Keep = &keep
	}
	return toolResult(s.service.Note(ctx, s.actor(ctx, request), options))
}

func (s *Adapter) search(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return toolResult(s.service.Search(ctx, journal.SearchOptions{
		Query: request.GetString("query", ""),
		Limit: request.GetInt("limit", 20),
	}))
}

// actor identifies the client that calls a tool by the name it reported during
// initialization, and its session by the session argument. Without one, the session is
// the one the client's runtime names in the server's environment, as for CLI calls: the
// runtime that starts the server passes its environment on.
// @note The server reads the environment it started with, so a session that changes
// while the server runs is recorded only when the client passes it.
func (s *Adapter) actor(ctx context.Context, request mcp.CallToolRequest) journal.Actor {
	runtime := journal.RuntimeUnknown
	if session, ok := server.ClientSessionFromContext(ctx).(server.SessionWithClientInfo); ok {
		runtime = journal.RuntimeFromClient(session.GetClientInfo().Name)
	}
	session := request.GetString("session", "")
	if len(session) == 0 {
		session = journal.SessionFromEnv(runtime, s.lookupEnv)
	}
	return journal.Actor{
		Runtime: runtime,
		Session: session,
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
