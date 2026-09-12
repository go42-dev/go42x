package mcp

import (
	"context"
	"fmt"
	"math"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/go42-dev/go42x/pkg/kwb"
)

type Adapter struct{ service *kwb.Service }

func New(service *kwb.Service) *Adapter { return &Adapter{service: service} }
func (s *Adapter) Name() string         { return "kwb" }

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
	}
}

func toolResult(value any, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultStructuredOnly(value), nil
}

func (s *Adapter) searchHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := validateNumbers(request); err != nil {
		return toolResult(nil, err)
	}
	return toolResult(s.service.Search(ctx, kwb.SearchOptions{
		Query: request.GetString("query", ""), Kind: request.GetString("kind", ""),
		Language: request.GetString("language", ""), PathPrefix: request.GetString("path_prefix", ""),
		Limit: request.GetInt("limit", 0), Offset: request.GetInt("offset", 0),
	}))
}

func (s *Adapter) getFileHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := validateNumbers(request); err != nil {
		return toolResult(nil, err)
	}
	return toolResult(
		s.service.GetFile(
			ctx,
			request.GetString("path", ""),
			request.GetInt("start_line", 0),
			request.GetInt("end_line", 0),
		),
	)
}

func (s *Adapter) listFilesHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := validateNumbers(request); err != nil {
		return toolResult(nil, err)
	}
	return toolResult(s.service.ListFiles(ctx, kwb.ListOptions{
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
	return toolResult(s.service.GetStats(ctx))
}

func validateNumbers(request mcp.CallToolRequest) error {
	for _, name := range []string{"limit", "offset", "start_line", "end_line"} {
		value, exists := request.GetArguments()[name]
		if !exists {
			continue
		}
		var number float64
		switch value := value.(type) {
		case int:
			number = float64(value)
		case float64:
			number = value
		default:
			return fmt.Errorf("%s must be an integer", name)
		}
		minimum := 1.0
		if name == "offset" {
			minimum = 0
		}
		if math.IsNaN(number) || number < minimum || number > math.MaxInt32 || number != math.Trunc(number) {
			return fmt.Errorf("%s must be an integer between %.0f and %d", name, minimum, math.MaxInt32)
		}
	}
	return nil
}
