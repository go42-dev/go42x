package mcpserver

import (
	"fmt"
	"time"

	"github.com/go42-dev/go42x/pkg/kwb"
)

// Settings configures the built-in MCP toolsets.
type Settings struct {
	RootPath       string
	ExplicitRoot   bool
	IndexPath      string
	DocsEntrypoint string
	SearchTimeout  time.Duration
}

// DoctorOptions configures MCP connection diagnostics.
type DoctorOptions struct {
	ProbeMCP bool
	Timeout  time.Duration
}

func (s *Settings) Validate() error {
	if s == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	return s.KnowledgeBaseSettings().Validate()
}

// KnowledgeBaseSettings returns the knowledge-base configuration shared by the toolsets.
func (s *Settings) KnowledgeBaseSettings() *kwb.Settings {
	settings := kwb.NewSettings()
	settings.RootPath = s.RootPath
	settings.IndexPath = s.IndexPath
	settings.RequireRootMatch = s.ExplicitRoot
	settings.SearchTimeout = s.SearchTimeout
	settings.Entrypoint = s.DocsEntrypoint
	return settings
}
