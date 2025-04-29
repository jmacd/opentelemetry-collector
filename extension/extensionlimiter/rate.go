// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package extensionlimiter // import "go.opentelemetry.io/collector/extension/extensionlimiter"

import (
	"context"
)

// RateLimiter is an interface that components can use to apply
// rate limiting (e.g., network-bytes-per-second, requests-per-second).
type RateLimiter interface {
	// @@@
	MustDeny(context.Context) error

	// Limit attempts to apply rate limiting based on the provided weight value.
	// Limit is expected to block the caller until the weight can be admitted.
	Limit(ctx context.Context, value uint64) error
}

// RateLimiterFunc is an easy way to construct RateLimiters.
type RateLimiterFunc func(ctx context.Context, value uint64) error

var _ RateLimiter = RateLimiterFunc(nil)

func (f RateLimiterFunc) MustDeny(ctx context.Context) error {
	return f.Limit(ctx, 0)
}

func (f RateLimiterFunc) Limit(ctx context.Context, value uint64) error {
	if f == nil {
		return nil
	}
	return f(ctx, value)
}

// NewRateLimiterWrapper returns a LimiterWrapper from a RateLimiter.
func NewRateLimiterWrapper(limiter RateLimiter) LimiterWrapper {
	return rateLimiterWrapper{limiter: limiter}
}

type rateLimiterWrapper struct {
	limiter RateLimiter
}

var _ LimiterWrapper = rateLimiterWrapper{}

func (w rateLimiterWrapper) MustDeny(ctx context.Context) error {

	return w.limiter.MustDeny(ctx)
}

func (w rateLimiterWrapper) LimitCall(ctx context.Context, value uint64, call func(context.Context) error) error {
	if err := w.limiter.Limit(ctx, value); err != nil {
		return err
	}
	return call(ctx)
}
