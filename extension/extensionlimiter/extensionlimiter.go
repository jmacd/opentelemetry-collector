// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package extensionlimiter // import "go.opentelemetry.io/collector/extension/extensionlimiter"

import "context"

// LimiterWrapper is a general-purpose interface for callers wishing
// to limit resources with simple scoping.
type LimiterWrapper interface {
	// @@@
	MustDeny(context.Context) error

	// @@@
	LimitCall(context.Context, uint64, func(ctx context.Context) error) error
}

type RateLimiterProvider interface {
	RateLimiter(WeightKey) (RateLimiter, error)
}

type ResourceLimiterProvider interface {
	ResourceLimiter(WeightKey) (ResourceLimiter, error)
}

type LimiterWrapperProvider interface {
	LimiterWrapper(WeightKey) (LimiterWrapper, error)
}

// type RateLimiterProviderFunc func(WeightKey) (RateLimiter, error)

// type ResourceLimiterProviderFunc func(WeightKey) (ResourceLimiter, error)
