package vendoring

import "log/slog"

type Option func(*Service)

func WithVersion(version string) Option {
	return func(s *Service) {
		s.cliVersion = version
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) {
		s.logger = logger
	}
}
