package agentenv

import (
	"fmt"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/config"
)

type Settings struct {
	Clean     bool
	Providers []string
	JSON      bool
}

func (o *Settings) Validate() error {
	if o == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	return provider.ValidateProviders(o.Providers, config.ProviderNames())
}
