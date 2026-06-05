// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package exporterhelper

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/metric/metricdata/metricdatatest"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/metadatatest"
	"go.opentelemetry.io/collector/pdata/testdata"
)

// TestTraces_WithTelemetryComponentKind_Processor verifies that when the
// exporterhelper is constructed with WithTelemetryComponentKind(KindProcessor),
// the recorded metric uses the "otelcol_processor_*" naming and the
// "processor" attribute key instead of the exporter-shaped defaults.
func TestTraces_WithTelemetryComponentKind_Processor(t *testing.T) {
	tt := componenttest.NewTelemetry()
	t.Cleanup(func() { require.NoError(t, tt.Shutdown(context.Background())) })

	te, err := NewTraces(
		context.Background(),
		exporter.Settings{ID: fakeTracesName, TelemetrySettings: tt.NewTelemetrySettings(), BuildInfo: component.NewDefaultBuildInfo()},
		&fakeTracesConfig,
		newTraceDataPusher(nil),
		WithTelemetryComponentKind(component.KindProcessor),
	)
	require.NoError(t, err)
	require.NotNil(t, te)

	require.NoError(t, te.ConsumeTraces(context.Background(), testdata.GenerateTraces(2)))

	metadatatest.AssertEqualExporterSentSpans(t, tt,
		[]metricdata.DataPoint[int64]{
			{
				Attributes: attribute.NewSet(
					attribute.String("processor", fakeTracesName.String())),
				Value: int64(2),
			},
		},
		metadatatest.WithMetricNamePrefixReplacement("otelcol_exporter_", "otelcol_processor_"),
		metricdatatest.IgnoreTimestamp(),
		metricdatatest.IgnoreExemplars(),
	)
}

func TestWithTelemetryComponentKind_RejectsUnsupportedKinds(t *testing.T) {
	for _, kind := range []component.Kind{component.KindReceiver, component.KindExtension, component.KindConnector} {
		_, err := NewTraces(
			context.Background(),
			exporter.Settings{ID: fakeTracesName, TelemetrySettings: componenttest.NewNopTelemetrySettings(), BuildInfo: component.NewDefaultBuildInfo()},
			&fakeTracesConfig,
			newTraceDataPusher(nil),
			WithTelemetryComponentKind(kind),
		)
		require.Error(t, err, "kind %v should be rejected", kind)
		assert.Contains(t, err.Error(), "unsupported component kind")
	}
}
