// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package extensionlimiter // import "go.opentelemetry.io/collector/extension/extensionlimiter"

// RateLimiter and ResourceLimiter are alternatives.  A limiter
// extension should implement one or the other, not both.  Users will
// request an interface

type RateLimiterProvider interface {
	MiddlewareKeys() []WeightKey
	GetRateLimiter(WeightKey) (RateLimiterProvider, RateLimiter, error)
}

type ResourceLimiterProvider interface {
	MiddlewareKeys() []WeightKey
	GetResourceLimiter(WeightKey) (ResourceLimiterProvider, ResourceLimiter, error)
}
