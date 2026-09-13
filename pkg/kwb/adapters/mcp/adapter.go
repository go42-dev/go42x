package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/go42-dev/go42x/pkg/kwb"
	"github.com/go42-dev/go42x/pkg/mcputil"
)

type Adapter struct{ service *kwb.Service }

func New(service *kwb.Service) *Adapter {
	return &Adapter{
		service: service,
	}
}

func (s *Adapter) Name() string {
	return "kwb"
}

func (s *Adapter) Tools() []server.ServerTool {
	return []server.ServerTool{
		{Tool: mcp.NewTool(
			"kwb_search",
			mcp.WithDescription(
				"Search documentation sections and code declarations. Returns ranked snippets with source line ranges. Queries are plain text; use filters to narrow results.",
			),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithString(
				"query",
				mcp.Required(),
				mcp.MinLength(1),
				mcp.MaxLength(4096),
				mcp.Description("Words, symbol name, or filename to find"),
			),
			mcp.WithString("kind", mcp.Enum("code", "documentation", "config")),
			mcp.WithString("language", mcp.Description("Language or format, such as go, md, ts, or yaml")),
			mcp.WithString("path_prefix", mcp.Description("Project-relative path prefix")),
			mcp.WithNumber(
				"limit",
				mcp.Min(1),
				mcp.Max(kwb.MaxSearchLimit),
				mcp.Description("Maximum results, default 10"),
			),
			mcp.WithNumber(
				"offset",
				mcp.Min(0),
				mcp.Max(kwb.MaxSearchOffset),
				mcp.Description("Use next_offset from the previous response"),
			),
			mcp.WithOutputSchema[kwb.SearchResponse](),
		), Handler: s.searchHandler},
		{Tool: mcp.NewTool(
			"kwb_get_file",
			mcp.WithDescription(
				"Read current source lines relative to the indexed project root. Defaults to 200 lines; responses are limited to 500 lines and 64 KiB.",
			),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithString("path", mcp.Required(), mcp.Description("File path from a search or file listing")),
			mcp.WithNumber("start_line", mcp.Min(1), mcp.Description("First line, one-based; default 1")),
			mcp.WithNumber("end_line", mcp.Min(1), mcp.Description("Last line, inclusive; default start_line + 199")),
			mcp.WithOutputSchema[kwb.FileContent](),
		), Handler: s.getFileHandler},
		{Tool: mcp.NewTool(
			"kwb_list_files",
			mcp.WithDescription(
				"List indexed files in path order with pagination. Returns total, generation, and next_offset when more files remain.",
			),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithString("type", mcp.Enum("code", "documentation", "config"), mcp.Description("Optional file kind")),
			mcp.WithString("language", mcp.Description("Language or format, such as go or md")),
			mcp.WithString("path_prefix", mcp.Description("Project-relative path prefix")),
			mcp.WithNumber(
				"limit",
				mcp.Min(1),
				mcp.Max(kwb.MaxListLimit),
				mcp.Description("Maximum files, default 100"),
			),
			mcp.WithNumber("offset", mcp.Min(0), mcp.Description("Use next_offset from the previous response")),
			mcp.WithOutputSchema[kwb.FilesResponse](),
		), Handler: s.listFilesHandler},
		{Tool: mcp.NewTool("kwb_stats",
			mcp.WithDescription("Get indexed file and chunk counts, project root, index path, and generation"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithOutputSchema[kwb.Stats](),
		), Handler: s.statsHandler},
		{Tool: mcp.NewTool(
			"docs_get",
			mcp.WithDescription(
				"Read a document by its authored ID from the current project checkout, including status, source lines, and replacement chain. Superseded IDs return the requested historical record. Does not require a search index. Bounded to 500 lines and 64 KiB encoded JSON; use source.next_start_line to continue.",
			),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithString("id", mcp.Required(), mcp.MinLength(1), mcp.MaxLength(128)),
			mcp.WithNumber("start_line", mcp.Min(1)),
			mcp.WithNumber("end_line", mcp.Min(1)),
			mcp.WithOutputSchema[kwb.DocumentResult](),
		), Handler: s.get},
		{Tool: mcp.NewTool(
			"docs_impact",
			mcp.WithDescription(
				"Find documentation affected by project-relative changed files or directories, including deleted paths. Uses explicit Markdown links and up to two related/supersession edges. Returns evidence and unmapped paths; no match does not prove no documentation impact. Bounded to 100 documents and 64 KiB encoded JSON.",
			),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithArray(
				"paths",
				mcp.Required(),
				mcp.WithStringItems(),
				mcp.MinItems(1),
				mcp.MaxItems(kwb.MaxImpactPaths),
			),
			mcp.WithOutputSchema[kwb.ImpactResult](),
		), Handler: s.impact},
		{Tool: mcp.NewTool(
			"project_context",
			mcp.WithDescription(
				"Assemble task context: project profile and rules, documentation linked to changed paths, relevant requirements/decisions, and keyword-ranked source candidates. Uses live documentation and verifies retrieved source hashes. Missing index reduces coverage; proposals and history retain status labels. Default source budget 16 KiB; total encoded response at most 64 KiB.",
			),
			mcp.WithReadOnlyHintAnnotation(
				true,
			),
			mcp.WithString("task", mcp.Required(), mcp.MinLength(1), mcp.MaxLength(4096)),
			mcp.WithArray("paths", mcp.WithStringItems(), mcp.MaxItems(kwb.MaxImpactPaths)),
			mcp.WithNumber("max_bytes", mcp.Min(1), mcp.Max(kwb.MaxContentBytes)),
			mcp.WithOutputSchema[kwb.ContextResult](),
		), Handler: s.context},
	}
}

func (s *Adapter) searchHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := mcputil.Integers(request, 1, "limit"); err != nil {
		return mcputil.Result(nil, err)
	}
	if err := mcputil.Integers(request, 0, "offset"); err != nil {
		return mcputil.Result(nil, err)
	}
	return mcputil.Result(s.service.Search(ctx, kwb.SearchOptions{
		Query:      request.GetString("query", ""),
		Kind:       request.GetString("kind", ""),
		Language:   request.GetString("language", ""),
		PathPrefix: request.GetString("path_prefix", ""),
		Limit:      request.GetInt("limit", 0),
		Offset:     request.GetInt("offset", 0),
	}))
}

func (s *Adapter) getFileHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := mcputil.Integers(request, 1, "start_line", "end_line"); err != nil {
		return mcputil.Result(nil, err)
	}
	return mcputil.Result(
		s.service.GetFile(
			ctx,
			request.GetString("path", ""),
			request.GetInt("start_line", 0),
			request.GetInt("end_line", 0),
		),
	)
}

func (s *Adapter) listFilesHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := mcputil.Integers(request, 1, "limit"); err != nil {
		return mcputil.Result(nil, err)
	}
	if err := mcputil.Integers(request, 0, "offset"); err != nil {
		return mcputil.Result(nil, err)
	}
	return mcputil.Result(s.service.ListFiles(ctx, kwb.ListOptions{
		Kind:     request.GetString("type", ""),
		Language: request.GetString("language", ""),
		PathPrefix: request.GetString(
			"path_prefix",
			"",
		),
		Limit:  request.GetInt("limit", 0),
		Offset: request.GetInt("offset", 0),
	}))
}

func (s *Adapter) statsHandler(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcputil.Result(s.service.GetStats(ctx))
}

func (s *Adapter) get(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := mcputil.Integers(request, 1, "start_line", "end_line"); err != nil {
		return mcputil.Result(nil, err)
	}
	return mcputil.Result(
		s.service.GetDocument(
			ctx,
			request.GetString("id", ""),
			request.GetInt("start_line", 0),
			request.GetInt("end_line", 0),
		),
	)
}

func (s *Adapter) impact(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	paths, err := mcputil.Strings(request, "paths")
	if err != nil {
		return mcputil.Result(nil, err)
	}
	return mcputil.Result(s.service.Impact(ctx, paths))
}

func (s *Adapter) context(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := mcputil.Integers(request, 1, "max_bytes"); err != nil {
		return mcputil.Result(nil, err)
	}
	paths, err := mcputil.Strings(request, "paths")
	if err != nil {
		return mcputil.Result(nil, err)
	}
	return mcputil.Result(
		s.service.Context(
			ctx,
			kwb.ContextOptions{
				Task:     request.GetString("task", ""),
				Paths:    paths,
				MaxBytes: request.GetInt("max_bytes", 0),
			},
		),
	)
}
