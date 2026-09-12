package go42x

import (
	"context"
	"fmt"
	"log/slog"
)

type Service struct {
	logger   *slog.Logger
	settings *Settings
}

func NewCommitService(settings *Settings, opts ...Option) (*Service, error) {
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("invalid options: %w", err)
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

	return svc, nil
}

func (s *Service) Execute(ctx context.Context) (retErr error) {
	s.logger.InfoContext(ctx, "OK")
	return nil
}
