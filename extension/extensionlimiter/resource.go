// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package extensionlimiter // import "go.opentelemetry.io/collector/extension/extensionlimiter"

import (
	"context"
)

// ResourceLimiter is an interface that components can use to apply
// resource limiting (e.g., concurrent requests, memory in use).
type ResourceLimiter interface {
	// Acquire attempts to acquire resources based on the provided weight value.
	//
	// It may block until resources are available or return an error if the limit
	// cannot be satisfied.
	//
	// On success, it returns a ReleaseFunc that should be called
	// when the resources are no longer needed.
	Acquire(ctx context.Context, value uint64) (ReleaseFunc, error)
}

var _ ResourceLimiter = ResourceLimiterFunc(nil)

// ReleaseFunc is called when resources should be released after limiting.
//
// RelaseFunc values are never nil values, even in the error case, for
// safety. Users should unconditionally defer these.
type ReleaseFunc func()

// ResourceLimiterFunc is an easy way to construct ResourceLimiters.
type ResourceLimiterFunc func(ctx context.Context, value uint64) (ReleaseFunc, error)

// Acquire implements ResourceLimiter.
func (f ResourceLimiterFunc) Acquire(ctx context.Context, value uint64) (ReleaseFunc, error) {
	if f == nil {
		return func() {}, nil
	}
	return f(ctx, value)
}
