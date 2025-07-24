// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package batchprocessor

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/confmap/confmaptest"
	"go.opentelemetry.io/collector/featuregate"
	"go.opentelemetry.io/collector/processor/batchprocessor/internal"
)

func TestUnmarshalDefaultConfig(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	require.NoError(t, confmap.New().Unmarshal(&cfg))
	assert.Equal(t, factory.CreateDefaultConfig(), cfg)
}

func TestUnmarshalConfig(t *testing.T) {
	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	require.NoError(t, cm.Unmarshal(&cfg))
	assert.Equal(t,
		&Config{
			SendBatchSize:            uint32(10000),
			SendBatchMaxSize:         uint32(11000),
			Timeout:                  time.Second * 10,
			MetadataCardinalityLimit: 1000,
		}, cfg)
}

func TestValidateConfig_DefaultBatchMaxSize(t *testing.T) {
	cfg := &Config{
		SendBatchSize:    100,
		SendBatchMaxSize: 0,
	}
	assert.NoError(t, cfg.Validate())
}

func TestValidateConfig_ValidBatchSizes(t *testing.T) {
	cfg := &Config{
		SendBatchSize:    100,
		SendBatchMaxSize: 1000,
	}
	assert.NoError(t, cfg.Validate())
}

func TestValidateConfig_InvalidBatchSize(t *testing.T) {
	cfg := &Config{
		SendBatchSize:    1000,
		SendBatchMaxSize: 100,
	}
	assert.Error(t, cfg.Validate())
}

func TestValidateConfig_InvalidTimeout(t *testing.T) {
	cfg := &Config{
		Timeout: -time.Second,
	}
	assert.Error(t, cfg.Validate())
}

func TestValidateConfig_ValidZero(t *testing.T) {
	cfg := &Config{}
	assert.NoError(t, cfg.Validate())
}

func TestValidateConfig_MetadataKeys(t *testing.T) {
	tests := []struct {
		name              string
		config            Config
		enableFeatureGate bool
		expectError       bool
		errorContains     string
	}{
		{
			name: "metadata_keys allowed with legacy implementation",
			config: Config{
				MetadataKeys: []string{"key1", "key2"},
			},
			enableFeatureGate: false,
			expectError:       false,
		},
		{
			name: "metadata_keys rejected with exporterhelper implementation",
			config: Config{
				MetadataKeys: []string{"key1", "key2"},
			},
			enableFeatureGate: true,
			expectError:       true,
			errorContains:     "metadata_keys is not yet supported with exporterhelper implementation",
		},
		{
			name: "empty metadata_keys allowed with exporterhelper implementation",
			config: Config{
				MetadataKeys: []string{},
			},
			enableFeatureGate: true,
			expectError:       false,
		},
		{
			name: "no metadata_keys allowed with exporterhelper implementation",
			config: Config{
				SendBatchSize: 100,
				Timeout:       300,
			},
			enableFeatureGate: true,
			expectError:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set feature gate
			if tt.enableFeatureGate {
				require.NoError(t, featuregate.GlobalRegistry().Set(internal.UseExporterHelperGate, true))
			} else {
				require.NoError(t, featuregate.GlobalRegistry().Set(internal.UseExporterHelperGate, false))
			}
			defer func() {
				// Reset to default
				require.NoError(t, featuregate.GlobalRegistry().Set(internal.UseExporterHelperGate, false))
			}()

			err := tt.config.Validate()
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// testWithFeatureGates runs a test function with both legacy and exporterhelper implementations
func testWithFeatureGates(t *testing.T, testName string, testFunc func(t *testing.T, useExporterHelper bool)) {
	t.Run(testName+" (legacy)", func(t *testing.T) {
		setFeatureGate(t, false)
		testFunc(t, false)
	})

	t.Run(testName+" (exporterhelper)", func(t *testing.T) {
		setFeatureGate(t, true)
		testFunc(t, true)
	})
}

// setFeatureGate sets the UseExporterHelper feature gate and ensures cleanup
func setFeatureGate(t *testing.T, enabled bool) {
	t.Helper()

	// Set the feature gate
	require.NoError(t, featuregate.GlobalRegistry().Set(internal.UseExporterHelperGate, enabled))

	// Ensure cleanup - reset to default (false)
	t.Cleanup(func() {
		require.NoError(t, featuregate.GlobalRegistry().Set(internal.UseExporterHelperGate, false))
	})
}

// requireImplementation validates that the config is using the expected implementation
func requireImplementation(t *testing.T, cfg *Config, expectExporterHelper bool) {
	t.Helper()

	if expectExporterHelper {
		require.True(t, cfg.UseExporterHelper(), "Expected exporterhelper implementation to be enabled")
	} else {
		require.False(t, cfg.UseExporterHelper(), "Expected legacy implementation to be enabled")
	}
}
