package agentenv

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator"
	"github.com/go42-dev/go42x/pkg/doctor/check"
)

// DoctorChecks supplies agent-environment diagnostics for registration with doctor.Service.
// Configuration is loaded once per run and shared by dependent checks.
func (s *Service) DoctorChecks(root string) []check.Check {
	checks := []check.Check{{ID: "agentenv.config", Run: func(ctx context.Context) check.Result {
		var err error
		s.config, err = config.LoadConfig(filepath.Join(root, agentEnvDir, configFile))
		if err != nil {
			message := "Configuration cannot be read or validated"
			if errors.Is(err, os.ErrNotExist) {
				message = "Configuration is missing"
			}
			// Parser errors can contain credentials from invalid YAML; never echo them.
			return check.Result{
				Status:      check.Fail,
				Message:     message,
				Remediation: "Run go42x agentenv init if needed, then review .go42x/go42x.yaml",
			}
		}
		return check.Result{Status: check.Pass, Message: "Configuration is valid"}
	}}, {ID: "agentenv.outputs", DependsOn: []string{"agentenv.config"}, Run: func(ctx context.Context) check.Result {
		gen := generator.NewGenerator(slog.New(slog.DiscardHandler), s.config, filepath.Join(root, agentEnvDir), root)
		plan, err := gen.Prepare(ctx, false)
		if err != nil {
			return check.Result{
				Status:      check.Fail,
				Message:     "Templates or provider outputs cannot be prepared",
				Remediation: "Review templates and existing provider settings; run go42x agentenv generate after correcting them",
			}
		}
		changes := plan.Changes()
		if len(changes) == 0 {
			return check.Result{Status: check.Pass, Message: "Generated outputs match configuration and templates"}
		}
		result := check.Result{
			Status:      check.Warn,
			Message:     "Generated outputs are missing or differ from the prepared configuration",
			Remediation: "go42x agentenv generate",
		}
		for _, change := range changes {
			result.Evidence = append(result.Evidence, change.Operation+" "+change.Path)
		}
		return result
	}}, {ID: "agentenv.providers", DependsOn: []string{"agentenv.config"}, Run: func(ctx context.Context) check.Result {
		var enabled []string
		for _, name := range slices.Sorted(maps.Keys(s.config.Providers)) {
			if s.config.ProviderEnabled(name) {
				enabled = append(enabled, name)
			}
		}
		if len(enabled) == 0 {
			return check.Result{
				Status:      check.Warn,
				Message:     "No agent providers are enabled",
				Remediation: "Enable the providers you use in .go42x/go42x.yaml",
			}
		}
		return check.Result{Status: check.Pass, Message: "Enabled agent providers", Evidence: enabled}
	}}}

	return checks
}

// MCPServers returns the definitions from the latest successful configuration
// check, or nil when configuration has not been loaded or is invalid.
func (s *Service) MCPServers() map[string]config.MCPServer {
	if s.config == nil {
		return nil
	}
	return maps.Clone(s.config.MCP)
}
