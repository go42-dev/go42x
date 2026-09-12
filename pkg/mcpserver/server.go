package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/mark3labs/mcp-go/server"
)

//go:generate mockgen -source $GOFILE -package mocks -destination mocks/mocks.go

type Toolset interface {
	Name() string
	Tools() []server.ServerTool
}

type Server struct {
	server          *server.MCPServer
	logger          *slog.Logger
	name            string
	version         string
	enabledToolsets []string
}

// New creates a server from the supplied tool groups, with default name "go42x"
// and version "dev". The caller owns the tool groups and their dependencies.
func New(toolsets []Toolset, opts ...Option) (*Server, error) {
	s := &Server{
		name:    "go42x",
		version: "dev",
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.name == "" || s.version == "" {
		return nil, fmt.Errorf("MCP server name and version are required")
	}

	available := make(map[string]Toolset, len(toolsets))
	for _, toolset := range toolsets {
		if toolset == nil || toolset.Name() == "" {
			return nil, fmt.Errorf("toolset name is required")
		}
		name := toolset.Name()
		if _, exists := available[name]; exists {
			return nil, fmt.Errorf("duplicate toolset %q", name)
		}
		available[name] = toolset
	}

	selected := s.enabledToolsets
	if selected == nil {
		for _, toolset := range toolsets {
			selected = append(selected, toolset.Name())
		}
	}

	mcpServer := server.NewMCPServer(
		s.name, s.version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	groups := make(map[string]bool, len(selected))
	names := make(map[string]string)

	for _, name := range selected {
		toolset, exists := available[name]
		if !exists {
			return nil, fmt.Errorf("unknown toolset %q", name)
		}

		if groups[name] {
			return nil, fmt.Errorf("toolset %q selected more than once", name)
		}

		groups[name] = true

		for _, tool := range toolset.Tools() {
			if tool.Tool.Name == "" || tool.Handler == nil {
				return nil, fmt.Errorf("toolset %q: tool name and handler are required", name)
			}
			if previous, exists := names[tool.Tool.Name]; exists {
				return nil, fmt.Errorf("duplicate tool %q in toolsets %q and %q", tool.Tool.Name, previous, name)
			}
			names[tool.Tool.Name] = name
			mcpServer.AddTools(tool)
		}
	}

	s.server = mcpServer

	return s, nil
}

// Serve runs the stdio transport until EOF or cancellation. It leaves the streams
// open for the caller and waits for active tool calls before returning.
func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stdio := server.NewStdioServer(s.server)
	stdio.SetErrorLogger(slog.NewLogLogger(s.logger.Handler(), slog.LevelError))

	err := stdio.Listen(ctx, input, output)
	if errors.Is(err, context.Canceled) {
		return nil
	}

	return err
}
