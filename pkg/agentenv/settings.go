package agentenv

import (
	"fmt"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

type Settings struct {
	Clean bool
	// Providers is an exact selection; nil uses project and local configuration.
	Providers []string
}

func (o *Settings) Validate() error {
	if o == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	return config.ValidateProviders(o.Providers)
}
