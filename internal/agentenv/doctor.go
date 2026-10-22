package agentenv

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/go42-dev/go42x/internal/agentenv/generator"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/doctor/check"
)

// DoctorChecks supplies agent-environment diagnostics for registration with doctor.Service.
// Configuration is loaded once per run and shared by dependent checks.
func (s *Service) DoctorChecks(root string) []check.Check {
	checks := []check.Check{{
		ID: "agentenv.config",
		Run: func(_ context.Context) check.Result {
			var err error
			s.config, err = config.LoadProjectConfig(
				filepath.Join(root, config.Directory, config.FileProject),
				s.settings.Providers,
			)
			if err != nil {
				s.config = nil
				message := "Configuration cannot be read or validated"
				if errors.Is(err, os.ErrNotExist) {
					message = "Configuration is missing"
				}
				// Parser errors can contain credentials from invalid YAML; never echo them.
				return check.Result{
					Status:      check.StatusFail,
					Message:     message,
					Remediation: "Run go42x agentenv init if needed, then review .go42x/go42x.yaml and go42x.local.yaml",
				}
			}

			return check.Result{
				Status:  check.StatusPass,
				Message: "Configuration is valid",
			}
		},
	}, {
		ID:        "agentenv.outputs",
		DependsOn: []string{"agentenv.config"},
		Run: func(ctx context.Context) check.Result {
			gen := generator.NewGenerator(
				slog.New(slog.DiscardHandler),
				s.config,
				filepath.Join(root, config.Directory),
				root,
			)
			plan, err := gen.Prepare(ctx, false)
			if err != nil {
				return check.Result{
					Status:      check.StatusFail,
					Message:     "Templates or provider outputs cannot be prepared",
					Remediation: "Review templates and existing provider settings; run go42x agentenv generate after correcting them",
				}
			}

			changes := plan.Changes()
			if len(changes) == 0 {
				return check.Result{
					Status:  check.StatusPass,
					Message: "Generated outputs match configuration and templates",
				}
			}

			result := check.Result{
				Status:      check.StatusWarn,
				Message:     "Generated outputs are missing or differ from the prepared configuration",
				Remediation: "go42x agentenv generate",
			}
			for _, change := range changes {
				result.Evidence = append(result.Evidence, change.Operation+" "+change.Path)
			}
			return result
		},
	}, {
		ID:        "agentenv.providers",
		DependsOn: []string{"agentenv.config"},
		Run: func(_ context.Context) check.Result {
			var enabled []string
			for _, name := range slices.Sorted(maps.Keys(s.config.Providers)) {
				if s.config.ProviderEnabled(name) {
					enabled = append(enabled, name)
				}
			}
			if len(enabled) == 0 {
				return check.Result{
					Status:      check.StatusWarn,
					Message:     "No agent providers are enabled",
					Remediation: "Select providers in .go42x/go42x.local.yaml, with GO42X_PROVIDERS, or with --providers",
				}
			}

			return check.Result{
				Status:   check.StatusPass,
				Message:  "Enabled agent providers",
				Evidence: enabled,
			}
		},
	}}

	return checks
}

// MCPServers returns the definitions from the latest successful configuration
// check, or nil when configuration has not been loaded or is invalid.
func (s *Service) MCPServers() map[string]provider.MCPServer {
	if s.config == nil {
		return nil
	}

	return maps.Clone(s.config.MCP)
}
