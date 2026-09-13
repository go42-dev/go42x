// Package mcputil contains the small protocol helpers shared by tool adapters.
package mcputil

import "github.com/mark3labs/mcp-go/mcp"

// Result wraps a service result, reporting failures as MCP tool errors.
func Result(value any, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultStructuredOnly(value), nil
}
