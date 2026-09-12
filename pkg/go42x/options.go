package go42x

import "log/slog"

type Option func(s *Service)

func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) {
		s.logger = logger
	}
}
