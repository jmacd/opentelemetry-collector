// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package extensionlimiter // import "go.opentelemetry.io/collector/extension/extensionlimiter"

import (
	"context"
)

// RateLimiterProvider is a provider for rate limiters.
//
// Limiter implementations will implement this or the
// RateLimiterProvider interface, but MUST not implement both.
// Limiters are covered by configmiddleware configuration, which
// is able to construct LimiterWrappers from these providers.
type RateLimiterProvider interface {
	RateLimiter(WeightKey) (RateLimiter, error)
}

// RateLimiterProviderFunc is a functional way to build RateLimters.
type RateLimiterProviderFunc func(WeightKey) (RateLimiter, error)

var _ RateLimiterProvider = RateLimiterProviderFunc(nil)

// RateLimiterProviderFunc implements RateLimiterProvider.
func (f RateLimiterProviderFunc) RateLimiter(key WeightKey) (RateLimiter, error) {
	return f(key)
}

// ResourceLimiter is an interface that an implementation makes
// available to apply time-based limits on quantities such as the
// number of bytes of arriving requests or number of items in outgoing
// requests.
//
// This is a relatively low-level interface. Callers that can use a
// LimiterWrapper should. This interface is meant for direct use only
// in special cases where control flow cannot be easily scoped to a
// callback, for example:
//
//   - in a streaming receiver where a limiter can be Acquired in
//     Send() and released in after Recv()
//   - inside middleware, in some cases (e.g., grpc.StatsHandler)
//
// See the README for more recommendations.
type RateLimiter interface {
	// Must deny is the logical equivalent of Acquire(0).  If the
	// Acquire would fail even for 0 units of a rate, the
	// caller must deny the request.  Implementations are
	// encouraged to ensure that when MustDeny() is false,
	// Acquire(0) is also false, however callers could use a
	// faster code path to implement MustDeny() since it does not
	// depend on the value.
	MustDeny(context.Context) error

	// Limit attempts to apply rate limiting with the provided
	// weight, based on the key that was given to the provider.
	//
	// This is expected to block the caller until the weight can
	// be admitted, or when the limit is completely saturated,
	// limiters may also return immediate errors.
	Limit(ctx context.Context, value uint64) error
}

// RateLimiterFunc is an easy way to construct RateLimiters.
type RateLimiterFunc func(ctx context.Context, value uint64) error

var _ RateLimiter = RateLimiterFunc(nil)

// MustDeny implements RateLimiter.
func (f RateLimiterFunc) MustDeny(ctx context.Context) error {
	return f.Limit(ctx, 0)
}

// Limit implements RateLimiter.
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

// MustDeny implements LimiterWrapper.
func (w rateLimiterWrapper) MustDeny(ctx context.Context) error {
	return w.limiter.MustDeny(ctx)
}

// LimitCall implements LimiterWrapper.
func (w rateLimiterWrapper) LimitCall(ctx context.Context, value uint64, call func(context.Context) error) error {
	if err := w.limiter.Limit(ctx, value); err != nil {
		return err
	}
	return call(ctx)
}
