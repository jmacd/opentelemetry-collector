// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package configlimiter

import (
	"testing"
	"time"
)

func TestLocalRateConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  LocalRateConfig
		wantErr bool
	}{
		{
			name: "valid token bucket config",
			config: LocalRateConfig{
				Limiters: []LimiterConfig{
					{
						TokenBucket: &TokenBucketConfig{
							Rated: 1000,
							Burst: 2000,
						},
						Unit:       "requests/second",
						MetricName: "test.limiter",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid admission config",
			config: LocalRateConfig{
				Limiters: []LimiterConfig{
					{
						Admission: &AdmissionConfig{
							Allowed: 1000000,
							Waiting: 100000,
						},
						Unit:       "request_bytes",
						MetricName: "test.admission",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty limiters",
			config: LocalRateConfig{
				Limiters: []LimiterConfig{},
			},
			wantErr: true,
		},
		{
			name: "invalid unit for rate limiter",
			config: LocalRateConfig{
				Limiters: []LimiterConfig{
					{
						TokenBucket: &TokenBucketConfig{
							Rated: 1000,
							Burst: 2000,
						},
						Unit:       "requests", // missing time component
						MetricName: "test.limiter",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid unit for resource limiter",
			config: LocalRateConfig{
				Limiters: []LimiterConfig{
					{
						Admission: &AdmissionConfig{
							Allowed: 1000000,
							Waiting: 100000,
						},
						Unit:       "request_bytes/second", // has time component
						MetricName: "test.admission",
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("LocalRateConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGlobalRateConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  GlobalRateConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: GlobalRateConfig{
				Domain: "test_domain",
				Service: ServiceConfig{
					Endpoint:    "localhost:8080",
					Timeout:     time.Second * 5,
					FailureMode: "allow",
				},
			},
			wantErr: false,
		},
		{
			name: "missing domain",
			config: GlobalRateConfig{
				Service: ServiceConfig{
					Endpoint:    "localhost:8080",
					Timeout:     time.Second * 5,
					FailureMode: "allow",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid failure mode",
			config: GlobalRateConfig{
				Domain: "test_domain",
				Service: ServiceConfig{
					Endpoint:    "localhost:8080",
					Timeout:     time.Second * 5,
					FailureMode: "invalid",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("GlobalRateConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCardinalityConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  CardinalityConfig
		wantErr bool
	}{
		{
			name: "valid refuse behavior",
			config: CardinalityConfig{
				MaxCount: 10,
				Behavior: "refuse",
			},
			wantErr: false,
		},
		{
			name: "valid replace behavior",
			config: CardinalityConfig{
				MaxCount: 20,
				Behavior: "replace",
			},
			wantErr: false,
		},
		{
			name: "invalid behavior",
			config: CardinalityConfig{
				MaxCount: 10,
				Behavior: "invalid",
			},
			wantErr: true,
		},
		{
			name: "zero max_count",
			config: CardinalityConfig{
				MaxCount: 0,
				Behavior: "refuse",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("CardinalityConfig.validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
