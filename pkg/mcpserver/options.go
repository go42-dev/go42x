package mcpserver

import "log/slog"

type Option func(s *Server)

// WithLogger sets the logger used for transport errors. Nil discards logs.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Server) {
		s.logger = logger
	}
}

// WithName overrides the default server name, "go42x".
func WithName(name string) Option {
	return func(s *Server) {
		s.name = name
	}
}

// WithVersion overrides the default server version, "dev".
func WithVersion(version string) Option {
	return func(s *Server) {
		s.version = version
	}
}

// WithToolsets selects supplied groups by name. Without this option,
// all groups are enabled. Calling it without names disables all groups.
func WithToolsets(names ...string) Option {
	return func(s *Server) {
		s.enabledToolsets = append([]string{}, names...)
	}
}
