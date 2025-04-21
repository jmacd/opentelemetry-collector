// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package extensionlimiter // import "go.opentelemetry.io/collector/extension/extensionlimiter"

import (
	"context"
)

// RateLimiter is an interface that components can use to apply
// rate limiting (e.g., network-bytes-per-second, requests-per-second).
type RateLimiter interface {
	// Limit attempts to apply rate limiting based on the provided weight value.
	// Limit is expected to block the caller until the weight can be admitted.
	Limit(ctx context.Context, value uint64) error
}

var _ RateLimiter = RateLimiterFunc(nil)

// RateLimiterFunc is an easy way to construct RateLimiters.
type RateLimiterFunc func(ctx context.Context, value uint64) error

// Limit implements RateLimiter.
func (f RateLimiterFunc) Limit(ctx context.Context, value uint64) error {
	if f == nil {
		return nil
	}
	return f(ctx, value)
}
