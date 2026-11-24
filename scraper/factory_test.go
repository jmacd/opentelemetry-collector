// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package scraper

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentdeprecation"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/pipeline"
)

var testType = component.MustNewType("test")

func nopSettings() Settings {
	return Settings{
		ID:                component.NewID(testType),
		TelemetrySettings: componenttest.NewNopTelemetrySettings(),
	}
}

func TestNewFactory(t *testing.T) {
	defaultCfg := struct{}{}
	f := NewFactory(
		testType,
		func() component.Config { return &defaultCfg },
	)
	assert.Equal(t, testType, f.Type())
	assert.EqualValues(t, &defaultCfg, f.CreateDefaultConfig())
	_, err := f.CreateLogs(context.Background(), nopSettings(), &defaultCfg)
	require.ErrorIs(t, err, pipeline.ErrSignalNotSupported)
	_, err = f.CreateMetrics(context.Background(), nopSettings(), &defaultCfg)
	require.ErrorIs(t, err, pipeline.ErrSignalNotSupported)
}

func TestScraperDeprecationOnUse(t *testing.T) {
	typ := component.MustNewType("oldmetrics")
	componentdeprecation.MustRegister("scraper", typ.String(), "metrics", zap.WarnLevel, "Use newmetrics.")
	f := NewFactory(typ, nil, WithLogs(createLogs, component.StabilityLevelStable), WithMetrics(createMetrics, component.StabilityLevelDeprecated))
	core, logs := observer.New(zap.DebugLevel)
	set := nopSettings()
	set.ID = component.NewID(typ)
	set.Logger = zap.New(core)
	_, err := f.CreateLogs(t.Context(), set, nil)
	require.NoError(t, err)
	assert.Zero(t, logs.Len())
	_, err = f.CreateMetrics(t.Context(), set, nil)
	require.NoError(t, err)
	require.Len(t, logs.All(), 1)
	assert.Equal(t, "Deprecation notice for metrics: Use newmetrics.", logs.All()[0].Message)
}

func TestNewFactoryWithOptions(t *testing.T) {
	testType := component.MustNewType("test")
	defaultCfg := struct{}{}
	f := NewFactory(
		testType,
		func() component.Config { return &defaultCfg },
		WithLogs(createLogs, component.StabilityLevelAlpha),
		WithMetrics(createMetrics, component.StabilityLevelAlpha),
	)
	assert.Equal(t, testType, f.Type())
	assert.EqualValues(t, &defaultCfg, f.CreateDefaultConfig())

	assert.Equal(t, component.StabilityLevelAlpha, f.LogsStability())
	_, err := f.CreateLogs(context.Background(), Settings{}, &defaultCfg)
	require.NoError(t, err)

	assert.Equal(t, component.StabilityLevelAlpha, f.MetricsStability())
	_, err = f.CreateMetrics(context.Background(), Settings{}, &defaultCfg)
	require.NoError(t, err)
}

func createLogs(context.Context, Settings, component.Config) (Logs, error) {
	return NewLogs(newTestScrapeLogsFunc(nil))
}

func createMetrics(context.Context, Settings, component.Config) (Metrics, error) {
	return NewMetrics(newTestScrapeMetricsFunc(nil))
}
