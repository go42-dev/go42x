package kwb

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync/atomic"
	"time"
)

const (
	// refreshInterval is the minimum time between two refreshes.
	refreshInterval = 10 * time.Second
	// refreshTimeout bounds one refresh, including the wait for another process's build.
	refreshTimeout = 2 * time.Minute
)

// Refresher keeps an existing index current while a long-running server uses it.
type Refresher struct {
	service  *Service
	requests chan struct{}
	cancel   context.CancelFunc
	done     chan struct{}
	running  atomic.Bool
}

// StartRefresher refreshes the index in the background now, then again after each Request, at
// most once per refresh interval. Searches on the service request a refresh when they return stale
// results. Shutdown stops the refresher.
func (s *Service) StartRefresher(ctx context.Context) *Refresher {
	ctx, cancel := context.WithCancel(ctx)
	r := &Refresher{
		service:  s,
		requests: make(chan struct{}, 1),
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	r.requests <- struct{}{}
	s.refresher.Store(r)
	go func() {
		defer close(r.done)
		r.run(ctx)
	}()
	return r
}

// Request asks for a refresh without waiting for it. Requests made before a refresh starts are
// combined into one.
func (r *Refresher) Request() {
	select {
	case r.requests <- struct{}{}:
	default:
	}
}

// Shutdown cancels a running refresh and waits for the refresher to stop.
func (r *Refresher) Shutdown(ctx context.Context) error {
	r.service.refresher.CompareAndSwap(r, nil)
	r.cancel()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for index refresher: %w", ctx.Err())
	}
}

func (r *Refresher) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.requests:
		}
		r.refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(refreshInterval):
		}
	}
}

func (r *Refresher) refresh(ctx context.Context) {
	r.running.Store(true)
	defer r.running.Store(false)
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	report, err := r.service.RefreshIndex(ctx)
	logger := r.service.logger
	switch {
	case errors.Is(err, fs.ErrNotExist):
		logger.InfoContext(ctx, "No knowledge-base index to refresh; run go42x kwb build to create one")
	case err != nil:
		logger.WarnContext(ctx, "Knowledge-base refresh failed", slog.Any("error", err))
	case report.Indexed == 0 && report.Deleted == 0 && !report.Rebuilt:
		logger.DebugContext(ctx, "Knowledge-base index is current", slog.Duration("duration", report.Duration))
	default:
		logger.InfoContext(ctx, "Knowledge-base index refreshed",
			slog.Int("indexed", report.Indexed),
			slog.Int("deleted", report.Deleted),
			slog.Bool("rebuilt", report.Rebuilt),
			slog.Duration("duration", report.Duration),
		)
	}
}
