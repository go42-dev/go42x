package kwb

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestCatalogWaitHonorsCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "caller cancellation"
		if deadline {
			name = "read deadline"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				root := t.TempDir()
				catalog := newDocumentCatalog()
				// Hold access as if an earlier request were refreshing the cache.
				catalog.access <- struct{}{}
				held := true
				defer func() {
					if held {
						<-catalog.access
					}
				}()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				want := context.Canceled
				if deadline {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, time.Second)
					defer stop()
					want = context.DeadlineExceeded
				}
				done := make(chan error, 1)
				go func() {
					_, err := catalog.Load(ctx, root, "README.md")
					done <- err
				}()
				synctest.Wait()
				select {
				case err := <-done:
					t.Fatalf("read completed while the cache was busy: %v", err)
				default:
				}
				if deadline {
					time.Sleep(time.Second)
				} else {
					cancel()
				}
				synctest.Wait()
				select {
				case err := <-done:
					if !errors.Is(err, want) {
						t.Fatalf("waiting read returned %v, want %v", err, want)
					}
				default:
					t.Fatal("canceled read is still waiting for the cache")
				}
				if catalog.root != "" {
					t.Fatal("canceled waiter changed the cache")
				}
				<-catalog.access
				held = false
				if _, err := catalog.Load(t.Context(), root, "README.md"); err != nil {
					t.Fatalf("subsequent read could not use the cache: %v", err)
				}
			})
		})
	}
}

func TestCatalogRejectsAlreadyCanceledRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	catalog := newDocumentCatalog()
	if result, err := catalog.Load(ctx, t.TempDir(), "README.md"); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: result=%+v error=%v", result, err)
	}
	if catalog.root != "" {
		t.Fatal("already canceled read changed the cache")
	}
	if _, err := catalog.Load(t.Context(), t.TempDir(), "README.md"); err != nil {
		t.Fatalf("canceled read retained cache access: %v", err)
	}
	if result, err := (&Catalog{}).impact(
		ctx,
		[]string{"README.md"},
	); result != nil ||
		!errors.Is(err, context.Canceled) {
		t.Fatalf("canceled impact analysis: result=%+v error=%v", result, err)
	}
}
