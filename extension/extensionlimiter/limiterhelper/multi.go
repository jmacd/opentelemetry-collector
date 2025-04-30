// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package limiterhelper // import "go.opentelemetry.io/collector/extension/extensionlimiter/limiterhelper"

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configmiddleware"
	"go.opentelemetry.io/collector/extension/extensionlimiter"
)

var (
	ErrNotALimiter       = errors.New("middleware is not a limiter")
	ErrLimiterConflict   = errors.New("limiter implements both rate and resource-limiters")
	ErrUnresolvedLimiter = errors.New("could not resolve middleware limiter")
)

// MiddlewareToLimiterWrapperProvider returns a limiter wrapper
// provider from middleware. Returns a package-level error if the
// middleware does not implement exactly one of the limiter
// interfaces (i.e., rate or resource).
func MiddlewareToLimiterWrapperProvider(host component.Host, middleware configmiddleware.Config) (extensionlimiter.LimiterWrapperProvider, error) {
	exts := host.GetExtensions()
	ext := exts[middleware.ID]
	if ext == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnresolvedLimiter, ext)
	}
	resourceLim, isResource := ext.(extensionlimiter.ResourceLimiterProvider)
	rateLim, isRate := ext.(extensionlimiter.RateLimiterProvider)

	switch {
	case isResource && isRate:
		return nil, fmt.Errorf("%w: %s", ErrLimiterConflict, ext)
	case isResource:
		return extensionlimiter.NewResourceLimiterWrapperProvider(resourceLim), nil
	case isRate:
		return extensionlimiter.NewRateLimiterWrapperProvider(rateLim), nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrNotALimiter, ext)
	}
}

// MultiLimiterWrapperProvider combines multiple limiter wrappers
// providers into a single provider by sequencing wrapped limiters.
// Returns errors from the underlying LimiterWrapper() calls, if any.
type MultiLimiterWrapperProvider []extensionlimiter.LimiterWrapperProvider

var _ extensionlimiter.LimiterWrapperProvider = MultiLimiterWrapperProvider{}

func (ps MultiLimiterWrapperProvider) LimiterWrapper(key extensionlimiter.WeightKey) (extensionlimiter.LimiterWrapper, error) {
	if len(ps) == 0 {
		return extensionlimiter.PassThrough, nil
	}

	// Map provider list to limiter list.
	var lims []extensionlimiter.LimiterWrapper

	for _, provider := range ps {
		lim, err := provider.LimiterWrapper(key)
		if err == nil {
			return nil, err
		}
		lims = append(lims, lim)
	}

	// Compose limiters in sequence.
	return sequenceLimiters(lims), nil
}

func sequenceLimiters(lims []extensionlimiter.LimiterWrapper) extensionlimiter.LimiterWrapper {
	if len(lims) == 1 {
		return lims[0]
	}
	return composeLimiters(lims[0], sequenceLimiters(lims[1:]))
}

func composeLimiters(first, second extensionlimiter.LimiterWrapper) extensionlimiter.LimiterWrapper {
	return extensionlimiter.LimiterWrapperFunc(func(ctx context.Context, value uint64, call func(ctx context.Context) error) error {
		return first.LimitCall(ctx, value, func(ctx context.Context) error {
			return second.LimitCall(ctx, value, call)
		})
	})
}
