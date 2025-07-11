// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package configlimiter

import "time"

// ExampleConfigurations demonstrates how to construct rate limiting configurations
// that correspond to the example.yaml file from our design discussion.

// CreateLocalRateHTTPConfig creates a local rate limiter for HTTP traffic.
func CreateLocalRateHTTPConfig() LocalRateConfig {
	return LocalRateConfig{
		Limiters: []LimiterConfig{
			{
				TokenBucket: &TokenBucketConfig{
					Rated: 50000,
					Burst: 100000,
				},
				Unit: "network_bytes/second",
				Conditions: []Condition{
					{Key: "signal"},
					{Key: "data_source"},
					{Key: "tenant_id"},
				},
				Cardinality: &CardinalityConfig{
					MaxCount: 20,
					Behavior: "replace",
				},
				MetricName: "otelhttp.recv_wire.limiter",
			},
		},
	}
}

// CreateLocalRateGRPCConfig creates a local rate limiter for gRPC traffic.
func CreateLocalRateGRPCConfig() LocalRateConfig {
	return LocalRateConfig{
		Limiters: []LimiterConfig{
			{
				TokenBucket: &TokenBucketConfig{
					Rated: 1000000,
					Burst: 5000000,
				},
				Unit: "network_bytes/second",
				Conditions: []Condition{
					{Key: "signal"},
					{Key: "data_source"},
					{Key: "tenant_id"},
				},
				MetricName: "otelgrpc.recv_wire.limiter",
			},
		},
	}
}

// CreateGlobalRateHTTPConfig creates a global rate limiter for HTTP traffic.
func CreateGlobalRateHTTPConfig() GlobalRateConfig {
	return GlobalRateConfig{
		Domain: "public_ingest",
		Service: ServiceConfig{
			Endpoint:    "rate-limit-service:8080",
			Timeout:     time.Millisecond * 100,
			FailureMode: "allow",
		},
	}
}

// CreateGlobalRateGRPCConfig creates a global rate limiter for gRPC traffic.
func CreateGlobalRateGRPCConfig() GlobalRateConfig {
	return GlobalRateConfig{
		Domain: "internal_ingest",
		Service: ServiceConfig{
			Endpoint:    "rate-limit-service:8080",
			Timeout:     time.Millisecond * 100,
			FailureMode: "allow",
		},
	}
}

// CreateLocalResourceMemoryConfig creates a local resource limiter for memory.
func CreateLocalResourceMemoryConfig() LocalResourceConfig {
	return LocalResourceConfig{
		Limiters: []LimiterConfig{
			{
				// Premium tenant limiter
				Admission: &AdmissionConfig{
					Allowed: 10000000,
					Waiting: 2000000,
				},
				Unit: "request_bytes",
				Conditions: []Condition{
					{Key: "tenant_id", Value: "premium"},
				},
				MetricName: "otelcol.admitted.premium.bytes",
			},
			{
				// Standard tenant limiter
				Admission: &AdmissionConfig{
					Allowed: 1000000,
					Waiting: 200000,
				},
				Unit: "request_bytes",
				Conditions: []Condition{
					{Key: "tenant_id", Value: "standard"},
				},
				MetricName: "otelcol.admitted.standard.bytes",
			},
		},
	}
}

// CreateProtocolHTTPLimiters creates protocol-level limiters for HTTP.
func CreateProtocolHTTPLimiters() ProtocolLimitersConfig {
	return ProtocolLimitersConfig{
		Limiters: []LimitRequest{
			{
				Request: []ExtractorConfig{
					{
						OpenTelemetrySignal: &OpenTelemetrySignalExtractor{
							DescriptorKey: "signal",
						},
					},
					{
						GenericKey: &GenericKeyExtractor{
							DescriptorKey:   "data_source",
							DescriptorValue: "external",
						},
					},
					{
						RequestHeaders: &RequestHeadersExtractor{
							HeaderName:    "x-tenant-id",
							DescriptorKey: "tenant_id",
						},
					},
				},
				NetworkBytes: []LimiterReference{
					{ID: "localrate/http"},
					{ID: "globalrate/http"},
				},
				RequestBytes: []LimiterReference{
					{ID: "localresource/memory"},
				},
			},
		},
	}
}

// CreateProtocolGRPCLimiters creates protocol-level limiters for gRPC.
func CreateProtocolGRPCLimiters() ProtocolLimitersConfig {
	return ProtocolLimitersConfig{
		Limiters: []LimitRequest{
			{
				Request: []ExtractorConfig{
					{
						OpenTelemetrySignal: &OpenTelemetrySignalExtractor{
							DescriptorKey: "signal",
						},
					},
					{
						GenericKey: &GenericKeyExtractor{
							DescriptorKey:   "data_source",
							DescriptorValue: "internal",
						},
					},
					{
						RequestHeaders: &RequestHeadersExtractor{
							HeaderName:    "grpc-service-id",
							DescriptorKey: "tenant_id",
						},
					},
				},
				NetworkBytes: []LimiterReference{
					{ID: "localrate/grpc"},
					{ID: "globalrate/grpc"},
				},
				RequestBytes: []LimiterReference{
					{ID: "localresource/memory"},
				},
			},
		},
	}
}

// CreateReceiverLevelLimiters creates receiver-level limiters.
func CreateReceiverLevelLimiters() ReceiverLimitersConfig {
	return ReceiverLimitersConfig{
		Limiters: []LimitRequest{
			{
				RequestItems: []LimiterReference{
					{ID: "localrate/items"},
				},
			},
		},
	}
}
