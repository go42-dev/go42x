package agentenv

import (
	"fmt"
	"strings"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/config"
)

type Settings struct {
	BaseDir   string
	Clean     bool
	Providers []string
	JSON      bool
}

func (o *Settings) Validate() error {
	if o == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if len(o.BaseDir) > 0 && len(strings.TrimSpace(o.BaseDir)) == 0 {
		return fmt.Errorf("base directory must not be blank")
	}
	return provider.ValidateProviders(o.Providers, config.ProviderNames())
}

// ExportSettings selects the destination and report format for a bundle export.
type ExportSettings struct {
	OutputDir string
	JSON      bool
}

func (s *ExportSettings) Validate() error {
	if s == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if len(strings.TrimSpace(s.OutputDir)) == 0 {
		return fmt.Errorf("output directory is required")
	}
	return nil
}
