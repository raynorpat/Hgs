package cmd

import (
	"context"
	"time"

	"github.com/gogits/gogs/modules/log"
)

// The web-server owner must stop and join this worker before closing the engine.
func startDraftPurger(purge func(context.Context) error, ready func() bool) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if ready() && ctx.Err() == nil {
				if err := purge(ctx); err != nil && ctx.Err() == nil {
					log.Warn("Temporary editor draft cleanup failed")
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
