package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"

	"github.com/mark3labs/mcp-go/server"
)

//go:generate mockgen -source $GOFILE -package mocks -destination mocks/mocks.go

type toolsetAccessor interface {
	Name() string
	Tools() []server.ServerTool
}

type Server struct {
	logger    *slog.Logger
	server    *server.MCPServer
	name      string
	version   string
	toolsets  map[string]bool
	toolNames map[string]string
}

// New creates a server with default name "go42x" and version "dev".
// Register toolsets with AddToolset before calling Serve.
func New(opts ...Option) (*Server, error) {
	s := &Server{
		name:      "go42x",
		version:   "dev",
		toolsets:  make(map[string]bool),
		toolNames: make(map[string]string),
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

	s.server = server.NewMCPServer(
		s.name, s.version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	return s, nil
}

// AddToolset registers a toolset before Serve, exposing all of its tools.
// The caller owns the toolset and its dependencies.
func (s *Server) AddToolset(set toolsetAccessor) error {
	if set == nil {
		return fmt.Errorf("toolset name is required")
	}
	name := set.Name()
	if name == "" {
		return fmt.Errorf("toolset name is required")
	}
	if s.toolsets[name] {
		return fmt.Errorf("duplicate toolset %q", name)
	}

	tools := set.Tools()

	names := make(map[string]string, len(tools))
	for _, tool := range tools {
		if tool.Tool.Name == "" || tool.Handler == nil {
			return fmt.Errorf("toolset %q: tool name and handler are required", name)
		}
		previous, exists := s.toolNames[tool.Tool.Name]
		if !exists {
			previous, exists = names[tool.Tool.Name]
		}
		if exists {
			return fmt.Errorf("duplicate tool %q in toolsets %q and %q", tool.Tool.Name, previous, name)
		}
		names[tool.Tool.Name] = name
	}

	s.server.AddTools(tools...)
	maps.Copy(s.toolNames, names)
	s.toolsets[name] = true

	return nil
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
