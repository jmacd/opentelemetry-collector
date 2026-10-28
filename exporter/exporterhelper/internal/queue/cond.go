// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package queue // import "go.opentelemetry.io/collector/exporter/exporterhelper/internal/queue"

import (
	"context"
	"sync"
)

// cond is a context-aware sync.Cond. Callers must hold L and recheck their
// condition after Wait, since cancellation also wakes other waiters.
type cond struct {
	*sync.Cond
}

func newCond(l sync.Locker) *cond {
	return &cond{Cond: sync.NewCond(l)}
}

// Wait atomically unlocks c.L and suspends execution of the calling goroutine. After later resuming execution, Wait locks c.L before returning.
func (c *cond) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() {
		// Taking L prevents cancellation from being missed between the check
		// above and entering Cond.Wait.
		c.L.Lock()
		c.Broadcast()
		c.L.Unlock()
	})
	defer stop()
	c.Cond.Wait()
	return ctx.Err()
}
