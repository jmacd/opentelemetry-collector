// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package configlimiter // import "go.opentelemetry.io/collector/config/configlimiter"

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// TokenBucketConfig configures a token bucket rate limiting algorithm.
// Based on the standard token bucket algorithm described at:
// https://en.wikipedia.org/wiki/Token_bucket
type TokenBucketConfig struct {
	// Rated is the number of tokens added to the bucket per time unit.
	// This is the sustained rate limit.
	Rated int64 `mapstructure:"rated"`

	// Burst is the maximum number of tokens the bucket can hold.
	// This allows for burst traffic up to this limit.
	Burst int64 `mapstructure:"burst"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// AdmissionConfig configures an admission control algorithm for resource limiting.
// This algorithm tracks resource usage and applies back-pressure when limits are exceeded.
type AdmissionConfig struct {
	// Allowed is the number of {countable} units allowed to pass through
	// before applying admission control.
	Allowed int64 `mapstructure:"allowed"`

	// Waiting is the number of {countable} units allowed to wait in LIFO order
	// without immediately failing when the allowed limit is exceeded.
	Waiting int64 `mapstructure:"waiting"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// CardinalityConfig configures limits over the count of distinct limiter instances
// and the behavior when limits are exceeded.
type CardinalityConfig struct {
	// MaxCount limits the maximum number of independent limiters
	// for this extension instance.
	MaxCount int `mapstructure:"max_count"`

	// Behavior defines what happens when max_count is exceeded.
	// Valid values are:
	// - "refuse": Fail requests after max_count descriptors exist
	// - "replace": Use LRU to re-use the longest-unused limiter (loses isolation)
	Behavior string `mapstructure:"behavior"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// Condition represents a key-value condition for pattern matching.
// Used to determine which rate limit patterns apply to incoming requests.
type Condition struct {
	// Key is the descriptor key to match against.
	Key string `mapstructure:"key"`

	// Value is the expected value for the key. If omitted, acts as a wildcard
	// that matches any value for the key (useful for per-entity limiting).
	Value string `mapstructure:"value,omitempty"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// LimiterConfig represents a single rate limiting pattern within an extension.
// Each pattern can match incoming requests based on conditions and apply
// the configured algorithm (token bucket or admission control).
type LimiterConfig struct {
	// TokenBucket configures token bucket rate limiting.
	// Mutually exclusive with Admission.
	TokenBucket *TokenBucketConfig `mapstructure:"token_bucket,omitempty"`

	// Admission configures admission control resource limiting.
	// Mutually exclusive with TokenBucket.
	Admission *AdmissionConfig `mapstructure:"admission,omitempty"`

	// Unit specifies the unit of measurement for this limiter.
	// For rate limiters: must be {countable}/{time_unit} (e.g., "requests/second", "network_bytes/second")
	// For resource limiters: must be {countable} (e.g., "request_bytes", "concurrent_requests")
	// This ensures type safety and prevents misuse of limiters.
	Unit string `mapstructure:"unit"`

	// Conditions specify the matching criteria for this limiter pattern.
	// All conditions must match for the pattern to apply (AND logic).
	// Empty conditions create a wildcard pattern that applies to all requests.
	Conditions []Condition `mapstructure:"conditions"`

	// Cardinality configures limits over distinct limiter instances.
	// Only used when conditions are specified (ignored for wildcard patterns).
	Cardinality *CardinalityConfig `mapstructure:"cardinality,omitempty"`

	// MetricName specifies the OpenTelemetry metric instrument name for this limiter.
	// For rate limiters: creates a Counter instrument
	// For resource limiters: creates an UpDownCounter instrument
	// The limit request's key-values are applied as attributes to metric events.
	MetricName string `mapstructure:"metric_name"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// ServiceConfig configures connection to an external rate limiting service
// for global rate limiting coordination.
type ServiceConfig struct {
	// Endpoint is the gRPC endpoint of the rate limiting service.
	Endpoint string `mapstructure:"endpoint"`

	// Timeout is the maximum time to wait for a rate limiting decision.
	Timeout time.Duration `mapstructure:"timeout"`

	// FailureMode specifies behavior when the rate limiting service is unavailable.
	// Valid values are:
	// - "allow": Allow requests when service is unavailable (fail-open)
	// - "deny": Deny requests when service is unavailable (fail-closed)
	FailureMode string `mapstructure:"failure_mode"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// LocalRateConfig configures a local rate limiting extension.
// Local limiters use token bucket algorithms and operate independently
// on each collector instance.
type LocalRateConfig struct {
	// Limiters defines the rate limiting patterns for this extension.
	// Multiple patterns allow different limits for different request characteristics.
	Limiters []LimiterConfig `mapstructure:"limiters"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// GlobalRateConfig configures a global rate limiting extension.
// Global limiters forward all rate limit requests to an external service
// for coordinated rate limiting across multiple collector instances.
type GlobalRateConfig struct {
	// Domain is the rate limiting domain sent to the external service.
	// Used to namespace rate limits within the service.
	Domain string `mapstructure:"domain"`

	// Service configures connection to the external rate limiting service.
	Service ServiceConfig `mapstructure:"service"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// LocalResourceConfig configures a local resource limiting extension.
// Resource limiters use admission control algorithms to manage
// resource consumption (memory, connections, etc.).
type LocalResourceConfig struct {
	// Limiters defines the resource limiting patterns for this extension.
	// Multiple patterns allow different limits for different request characteristics.
	Limiters []LimiterConfig `mapstructure:"limiters"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// ExtractorConfig configures how to extract descriptor key-value pairs
// from incoming requests to build rate limit requests.
type ExtractorConfig struct {
	// OpenTelemetrySignal extracts the signal type (traces, metrics, logs, profiles).
	OpenTelemetrySignal *OpenTelemetrySignalExtractor `mapstructure:"opentelemetry_signal,omitempty"`

	// GenericKey creates a static key-value pair for all requests.
	GenericKey *GenericKeyExtractor `mapstructure:"generic_key,omitempty"`

	// RequestHeaders extracts values from request headers.
	RequestHeaders *RequestHeadersExtractor `mapstructure:"request_headers,omitempty"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// OpenTelemetrySignalExtractor extracts the OpenTelemetry signal type
// from the request context and adds it as a descriptor key-value pair.
type OpenTelemetrySignalExtractor struct {
	// DescriptorKey is the key name to use in the rate limit descriptor.
	DescriptorKey string `mapstructure:"descriptor_key"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// GenericKeyExtractor creates a static key-value pair for all requests.
// Useful for applying the same rate limit to all traffic from a source.
type GenericKeyExtractor struct {
	// DescriptorKey is the key name to use in the rate limit descriptor.
	DescriptorKey string `mapstructure:"descriptor_key"`

	// DescriptorValue is the static value to use for all requests.
	DescriptorValue string `mapstructure:"descriptor_value"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// RequestHeadersExtractor extracts values from HTTP/gRPC request headers
// and adds them as descriptor key-value pairs.
type RequestHeadersExtractor struct {
	// HeaderName is the name of the header to extract from.
	HeaderName string `mapstructure:"header_name"`

	// DescriptorKey is the key name to use in the rate limit descriptor.
	DescriptorKey string `mapstructure:"descriptor_key"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// LimitRequest represents a single rate limiting request with extractors
// and the limiters that should evaluate it. This corresponds to one
// "request" in the Envoy terminology.
type LimitRequest struct {
	// Request contains the extractors that build the descriptor for this request.
	// All extractors are applied and their results combined into a single descriptor.
	Request []ExtractorConfig `mapstructure:"request"`

	// NetworkBytes specifies limiters that should evaluate this request for network byte limits.
	// Used for compressed data size limiting (e.g., HTTP request body size).
	NetworkBytes []LimiterReference `mapstructure:"network_bytes,omitempty"`

	// RequestBytes specifies limiters that should evaluate this request for request byte limits.
	// Used for uncompressed data size limiting (e.g., parsed telemetry data size).
	RequestBytes []LimiterReference `mapstructure:"request_bytes,omitempty"`

	// RequestCount specifies limiters that should evaluate this request for request count limits.
	// Used for request rate limiting (e.g., requests per second).
	RequestCount []LimiterReference `mapstructure:"request_count,omitempty"`

	// RequestItems specifies limiters that should evaluate this request for item count limits.
	// Used for limiting based on the number of telemetry items (spans, metrics, logs).
	RequestItems []LimiterReference `mapstructure:"request_items,omitempty"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// LimiterReference references a rate limiting extension by ID.
// The referenced extension must be configured in the extensions section.
type LimiterReference struct {
	// ID is the extension identifier (e.g., "localrate/http", "globalrate/grpc").
	ID string `mapstructure:"id"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// ProtocolLimitersConfig configures rate limiting for a specific protocol
// (HTTP or gRPC) within a receiver. Applied as middleware before request
// processing.
type ProtocolLimitersConfig struct {
	// Limiters defines the rate limiting requests for this protocol.
	// Multiple requests allow multi-dimensional rate limiting.
	Limiters []LimitRequest `mapstructure:"limiters"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// ReceiverLimitersConfig configures rate limiting at the receiver level,
// applied after protocol-specific middleware limiting.
type ReceiverLimitersConfig struct {
	// Limiters defines additional rate limiting requests applied at the receiver level.
	// These typically handle limits that require access to parsed telemetry data.
	Limiters []LimitRequest `mapstructure:"limiters"`

	// prevent unkeyed literal initialization
	_ struct{}
}

// Validate checks if the configuration is valid.
func (cfg *LocalRateConfig) Validate() error {
	if len(cfg.Limiters) == 0 {
		return errors.New("at least one limiter must be configured")
	}

	for i, limiter := range cfg.Limiters {
		if err := limiter.validate("rate"); err != nil {
			return fmt.Errorf("limiter[%d]: %w", i, err)
		}
	}
	return nil
}

// Validate checks if the configuration is valid.
func (cfg *GlobalRateConfig) Validate() error {
	if cfg.Domain == "" {
		return errors.New("domain must be specified")
	}
	return cfg.Service.validate()
}

// Validate checks if the configuration is valid.
func (cfg *LocalResourceConfig) Validate() error {
	if len(cfg.Limiters) == 0 {
		return errors.New("at least one limiter must be configured")
	}

	for i, limiter := range cfg.Limiters {
		if err := limiter.validate("resource"); err != nil {
			return fmt.Errorf("limiter[%d]: %w", i, err)
		}
	}
	return nil
}

// validate checks if the limiter configuration is valid.
func (cfg *LimiterConfig) validate(limiterType string) error {
	// Exactly one algorithm must be specified
	algorithms := 0
	if cfg.TokenBucket != nil {
		algorithms++
	}
	if cfg.Admission != nil {
		algorithms++
	}
	if algorithms != 1 {
		return errors.New("exactly one of token_bucket or admission must be specified")
	}

	// Validate unit format
	if cfg.Unit == "" {
		return errors.New("unit must be specified")
	}

	if err := cfg.validateUnit(limiterType); err != nil {
		return err
	}

	// Validate algorithm-specific configuration
	if cfg.TokenBucket != nil {
		if err := cfg.TokenBucket.validate(); err != nil {
			return fmt.Errorf("token_bucket: %w", err)
		}
	}

	if cfg.Admission != nil {
		if err := cfg.Admission.validate(); err != nil {
			return fmt.Errorf("admission: %w", err)
		}
	}

	// Validate cardinality config
	if cfg.Cardinality != nil {
		if len(cfg.Conditions) == 0 {
			return errors.New("cardinality is only valid when conditions are specified")
		}
		if err := cfg.Cardinality.validate(); err != nil {
			return fmt.Errorf("cardinality: %w", err)
		}
	}

	// Validate metric name
	if cfg.MetricName == "" {
		return errors.New("metric_name must be specified")
	}

	return nil
}

// validateUnit checks if the unit format is appropriate for the limiter type.
func (cfg *LimiterConfig) validateUnit(limiterType string) error {
	switch limiterType {
	case "rate":
		// Rate limiters must have time-based units (e.g., "requests/second")
		if !strings.Contains(cfg.Unit, "/") {
			return errors.New("rate limiter unit must be in format {countable}/{time_unit}")
		}
		parts := strings.Split(cfg.Unit, "/")
		if len(parts) != 2 {
			return errors.New("rate limiter unit must be in format {countable}/{time_unit}")
		}
		timeUnit := parts[1]
		validTimeUnits := []string{"second", "minute", "hour", "day"}
		for _, valid := range validTimeUnits {
			if timeUnit == valid {
				return nil
			}
		}
		return fmt.Errorf("invalid time unit '%s', must be one of: %v", timeUnit, validTimeUnits)

	case "resource":
		// Resource limiters must not have time-based units (e.g., "request_bytes")
		if strings.Contains(cfg.Unit, "/") {
			return errors.New("resource limiter unit must be a {countable} without time component")
		}
		return nil

	default:
		return fmt.Errorf("unknown limiter type: %s", limiterType)
	}
}

// validate checks if the token bucket configuration is valid.
func (cfg *TokenBucketConfig) validate() error {
	if cfg.Rated <= 0 {
		return errors.New("rated must be positive")
	}
	if cfg.Burst <= 0 {
		return errors.New("burst must be positive")
	}
	if cfg.Burst < cfg.Rated {
		return errors.New("burst must be greater than or equal to rated")
	}
	return nil
}

// validate checks if the admission configuration is valid.
func (cfg *AdmissionConfig) validate() error {
	if cfg.Allowed <= 0 {
		return errors.New("allowed must be positive")
	}
	if cfg.Waiting < 0 {
		return errors.New("waiting must be non-negative")
	}
	return nil
}

// validate checks if the cardinality configuration is valid.
func (cfg *CardinalityConfig) validate() error {
	if cfg.MaxCount <= 0 {
		return errors.New("max_count must be positive")
	}
	validBehaviors := []string{"refuse", "replace"}
	for _, valid := range validBehaviors {
		if cfg.Behavior == valid {
			return nil
		}
	}
	return fmt.Errorf("behavior must be one of: %v", validBehaviors)
}

// validate checks if the service configuration is valid.
func (cfg *ServiceConfig) validate() error {
	if cfg.Endpoint == "" {
		return errors.New("endpoint must be specified")
	}
	if cfg.Timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	validFailureModes := []string{"allow", "deny"}
	for _, valid := range validFailureModes {
		if cfg.FailureMode == valid {
			return nil
		}
	}
	return fmt.Errorf("failure_mode must be one of: %v", validFailureModes)
}
