// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package queue

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

// Channel operations let synctest observe waiters blocked on reacquiring the lock.
type channelLocker chan struct{}

func (l channelLocker) Lock()   { l <- struct{}{} }
func (l channelLocker) Unlock() { <-l }

func TestCondBroadcastAfterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lock := make(channelLocker, 1)
		c := newCond(lock)
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 2)
		for range 2 {
			go func() {
				lock.Lock()
				err := c.Wait(ctx)
				lock.Unlock()
				result <- err
			}()
		}
		synctest.Wait()
		lock.Lock()
		cancel()
		synctest.Wait()
		c.Broadcast()
		lock.Unlock()
		for range 2 {
			require.ErrorIs(t, <-result, context.Canceled)
		}
	})
}
