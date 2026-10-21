package doctor

import (
	"fmt"
	"time"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/config"
)

type Settings struct {
	RootPath  string
	IndexPath string
	ProbeMCP  bool
	Timeout   time.Duration
	JSON      bool
	Providers []string
}

func NewSettings() *Settings {
	return &Settings{
		RootPath: ".",
		Timeout:  10 * time.Second,
	}
}

func (s *Settings) Validate() error {
	if s == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if len(s.RootPath) == 0 {
		return fmt.Errorf("project root is required")
	}
	if s.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	return provider.ValidateProviders(s.Providers, config.ProviderNames())
}
