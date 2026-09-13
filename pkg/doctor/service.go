// Package doctor assembles and runs diagnostics supplied by project subsystems.
package doctor

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/go42-dev/go42x/pkg/agentenv"
	"github.com/go42-dev/go42x/pkg/doctor/check"
	"github.com/go42-dev/go42x/pkg/kwb"
	"github.com/go42-dev/go42x/pkg/mcpserver"
)

type Service struct {
	logger   *slog.Logger
	settings *Settings
	checks   []check.Check
}

// NewService registers the built-in subsystem checks for the selected project.
func NewService(settings *Settings, opts ...Option) (*Service, error) {
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("invalid settings: %w", err)
	}

	svc := &Service{
		settings: settings,
	}

	for _, opt := range opts {
		opt(svc)
	}
	if svc.logger == nil {
		svc.logger = slog.New(slog.DiscardHandler)
	}

	root, err := filepath.Abs(svc.settings.RootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}

	indexPath := svc.settings.IndexPath
	if indexPath != "" {
		indexPath, err = filepath.Abs(indexPath)
		if err != nil {
			return nil, fmt.Errorf("resolve index path: %w", err)
		}
	}

	agentEnvSvc, err := agentenv.NewAgentEnvService(
		&agentenv.Settings{},
		agentenv.WithLogger(svc.logger.With("component", "agentenv")))
	if err != nil {
		return nil, err
	}

	svc.Register(agentEnvSvc.DoctorChecks(root)...)

	mcpCheck := mcpserver.DoctorCheck(
		root, agentEnvSvc.MCPServers,
		mcpserver.DoctorOptions{
			ProbeMCP: svc.settings.ProbeMCP,
			Timeout:  svc.settings.Timeout,
		})
	mcpCheck.DependsOn = []string{"agentenv.config"}

	svc.Register(mcpCheck)

	svc.Register(kwb.DoctorChecks(root, indexPath)...)

	return svc, nil
}

// Register adds subsystem checks before Run. The complete dependency graph is
// validated before any check executes.
func (s *Service) Register(checks ...check.Check) {
	s.checks = append(s.checks, checks...)
}

func (s *Service) Run(ctx context.Context) (*check.Report, error) {
	return check.Run(ctx, s.checks...)
}
