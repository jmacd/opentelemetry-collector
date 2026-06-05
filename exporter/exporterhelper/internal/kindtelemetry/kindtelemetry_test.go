// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package kindtelemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/component"
)

func TestForKindDefault(t *testing.T) {
	for _, kind := range []component.Kind{{}, component.KindExporter} {
		id, err := ForKind(kind)
		require.NoError(t, err)
		assert.Equal(t, "exporter", id.AttributeKey)
		assert.Equal(t, "exporter", id.SpanNamespace)
		assert.Empty(t, id.TelemetryBuilderOptions(), "default identity should not request name substitution")
	}
}

func TestForKindProcessor(t *testing.T) {
	id, err := ForKind(component.KindProcessor)
	require.NoError(t, err)
	assert.Equal(t, "processor", id.AttributeKey)
	assert.Equal(t, "processor", id.SpanNamespace)
	assert.Len(t, id.TelemetryBuilderOptions(), 1, "processor identity must request a metric-name prefix substitution")
}

func TestForKindUnsupported(t *testing.T) {
	for _, kind := range []component.Kind{component.KindReceiver, component.KindExtension, component.KindConnector} {
		_, err := ForKind(kind)
		require.Error(t, err, "kind %v should be rejected", kind)
		assert.Contains(t, err.Error(), "unsupported component kind")
	}
}

func TestDefault(t *testing.T) {
	assert.Equal(t, Identity{AttributeKey: "exporter", SpanNamespace: "exporter"}, Default())
}

func TestResolveFallsBackForUnsupportedKind(t *testing.T) {
	assert.Equal(t, Default(), Resolve(component.KindReceiver))
}
