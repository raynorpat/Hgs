package hgbridge

import (
	"context"
	"net/http"
	"time"
)

// InterruptResponseOnCancel interrupts a blocked downstream Write/Flush using
// supported net/http deadlines. Call the returned stop/join before normal
// cancellation and cleanup so healthy keepalive responses retain no deadline.
func InterruptResponseOnCancel(ctx context.Context, controller *http.ResponseController) func() {
	if controller == nil {
		return func() {}
	}
	aborted := make(chan struct{})
	stopDeadlines := make(chan struct{})
	stopAbort := context.AfterFunc(ctx, func() {
		defer close(aborted)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			// TLS close_notify can replace a failed writer's deadline temporarily.
			controller.SetWriteDeadline(time.Now())
			select {
			case <-stopDeadlines:
				return
			case <-ticker.C:
			}
		}
	})
	return func() {
		if !stopAbort() {
			close(stopDeadlines)
			<-aborted
		}
	}
}
