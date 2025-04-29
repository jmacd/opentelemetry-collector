// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package extensionlimiter // import "go.opentelemetry.io/collector/extension/extensionlimiter"

import (
	"context"
)

// ResourceLimiter is an interface that components can use to apply
// physical limits on quantities quantities such as the number of
// concurrent requests or memory in use.
//
// This is a relatively low-level interface, callers that can use
// ResourceLimiterWrapper should do so.  This interfaceis meant for
// use in cases where:
//
//   - the described resource has not yet been allocated; here the
//     Acquire() prevents a resource from being over-consumed
//   - the caller will return or continue with concurrent work before
//     it is finished using the indicated resource.
//
// See the README for more recommendations.
type ResourceLimiter interface {
	// Must deny is the logical equivalent of Acquire(0).  If the
	// Acquire would fail even for 0 units of a resource, the
	// caller must deny the request.  Implementations are
	// encouraged to ensure that when MustDeny() is false,
	// Acquire(0) is also false, however callers could use a
	// faster code path to implement MustDeny() since it does not
	// depend on the value.
	MustDeny(context.Context) error

	// Acquire attempts to acquire a quantified resource based on
	// the provided weight value. The caller has these options:
	//
	// - Accept and let the request proceed by returning a release func and a nil error
	// - Fail and return a non-nil error and a nil release func
	// - Block until the resource becomes available, then accept
	// - Block until the context times out, return the error.
	//
	// Note that callers may decide on these options using internal
	// logic, and that all of these options may be good options.
	// See the README for more recommendations.
	//
	// Implementations are not required to call a release func
	// when Acquire(0) is called, because there is nothing to
	// release. This is the equivalent of MustDeny().
	//
	// On success, it returns a ReleaseFunc that should be called
	// when the resources are no longer needed.
	Acquire(ctx context.Context, value uint64) (ReleaseFunc, error)
}

// ReleaseFunc is called when resources should be released after limiting.
//
// RelaseFunc values are never nil values, even in the error case, for
// safety. Users should unconditionally defer these.
type ReleaseFunc func()

// ResourceLimiterFunc is a functional way to construct ResourceLimiters.
type ResourceLimiterFunc func(ctx context.Context, value uint64) (ReleaseFunc, error)

var _ ResourceLimiter = ResourceLimiterFunc(nil)

// MustDeny implements ResourceLimiter
func (f ResourceLimiterFunc) MustDeny(ctx context.Context) error {
	// As defined, Acquire(0) callers can ignore the release func.
	_, err := f.Acquire(ctx, 0)
	return err
}

// Acquire implements ResourceLimiter
func (f ResourceLimiterFunc) Acquire(ctx context.Context, value uint64) (ReleaseFunc, error) {
	if f == nil {
		return func() {}, nil
	}
	return f(ctx, value)
}

// NewResourceLimiterWrapper returns a LimiterWrapper from a ResourceLimiter.
func NewResourceLimiterWrapper(limiter ResourceLimiter) LimiterWrapper {
	return resourceLimiterWrapper{limiter: limiter}
}

type resourceLimiterWrapper struct {
	limiter ResourceLimiter
}

var _ LimiterWrapper = resourceLimiterWrapper{}

func (w resourceLimiterWrapper) MustDeny(ctx context.Context) error {

	return w.limiter.MustDeny(ctx)
}

func (w resourceLimiterWrapper) LimitCall(ctx context.Context, value uint64, call func(context.Context) error) error {
	if release, err := w.limiter.Acquire(ctx, value); err != nil {
		return err
	} else {
		defer release()
	}
	return call(ctx)
}
