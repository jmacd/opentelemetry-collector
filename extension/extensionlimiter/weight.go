// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package extensionlimiter // import "go.opentelemetry.io/collector/extension/extensionlimiter"

// WeightKey is an enum type for common rate limits
type WeightKey string

// Predefined weight keys for common rate limits.  This is not a closed set.
//
// Providers should return errors when they do not recognize a weight key.
const (
	// WeightKeyNetworkBytes is typically used with RateLimiters
	// for limiting arrival rate.
	WeightKeyNetworkBytes WeightKey = "network_bytes"

	// WeightKeyRequestCount can be used to limit the rate or
	// total concurrent number of requests (i.e., pipeline data
	// objects).
	WeightKeyRequestCount WeightKey = "request_count"

	// WeightKeyRequestItems can be used to limit the rate or
	// total concurrent number of items (log records, metric data
	// points, spans, profiles).
	WeightKeyRequestItems WeightKey = "request_items"

	// WeightKeyMemorySize is typically used with ResourceLimiters
	// for limiting active memory usage.
	WeightKeyMemorySize WeightKey = "memory_size"
)
