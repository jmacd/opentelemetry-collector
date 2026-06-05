// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package metadata

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/component/componenttest"
)

// TestNameSubstitution_Default verifies that NewTelemetryBuilder produces the
// default "otelcol_exporter_*" metric names when no substitution option is
// supplied.
func TestNameSubstitution_Default(t *testing.T) {
	tt := componenttest.NewTelemetry()
	t.Cleanup(func() { require.NoError(t, tt.Shutdown(context.Background())) })

	tb, err := NewTelemetryBuilder(tt.NewTelemetrySettings())
	require.NoError(t, err)
	t.Cleanup(tb.Shutdown)

	tb.ExporterEnqueueFailedLogRecords.Add(t.Context(), 1)
	tb.ExporterQueueBatchSendSize.Record(t.Context(), 42)

	got, err := tt.GetMetric("otelcol_exporter_enqueue_failed_log_records")
	require.NoError(t, err)
	assert.Equal(t, "otelcol_exporter_enqueue_failed_log_records", got.Name)

	got, err = tt.GetMetric("otelcol_exporter_queue_batch_send_size")
	require.NoError(t, err)
	assert.Equal(t, "otelcol_exporter_queue_batch_send_size", got.Name)
}

// TestNameSubstitution_Processor verifies that supplying
// WithMetricNamePrefixReplacement rewrites every metric name produced by the
// builder.
func TestNameSubstitution_Processor(t *testing.T) {
	tt := componenttest.NewTelemetry()
	t.Cleanup(func() { require.NoError(t, tt.Shutdown(context.Background())) })

	tb, err := NewTelemetryBuilder(
		tt.NewTelemetrySettings(),
		WithMetricNamePrefixReplacement("otelcol_exporter_", "otelcol_processor_"),
	)
	require.NoError(t, err)
	t.Cleanup(tb.Shutdown)

	tb.ExporterEnqueueFailedLogRecords.Add(t.Context(), 1)
	tb.ExporterQueueBatchSendSize.Record(t.Context(), 42)

	got, err := tt.GetMetric("otelcol_processor_enqueue_failed_log_records")
	require.NoError(t, err)
	assert.Equal(t, "otelcol_processor_enqueue_failed_log_records", got.Name)

	got, err = tt.GetMetric("otelcol_processor_queue_batch_send_size")
	require.NoError(t, err)
	assert.Equal(t, "otelcol_processor_queue_batch_send_size", got.Name)

	// Confirm the original "exporter_" names are not present.
	_, err = tt.GetMetric("otelcol_exporter_enqueue_failed_log_records")
	assert.Error(t, err)
}

// TestNameSubstitution_EmptyOldPrefix rejects an empty oldPrefix.
func TestNameSubstitution_EmptyOldPrefix(t *testing.T) {
	tt := componenttest.NewTelemetry()
	t.Cleanup(func() { require.NoError(t, tt.Shutdown(context.Background())) })

	_, err := NewTelemetryBuilder(
		tt.NewTelemetrySettings(),
		WithMetricNamePrefixReplacement("", "otelcol_processor_"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oldPrefix must not be empty")
}

// TestNameSubstitution_PrefixMismatch errors when the supplied oldPrefix does
// not match the metrics produced by this builder. Every metric name violation
// is reported (errors are joined).
func TestNameSubstitution_PrefixMismatch(t *testing.T) {
	tt := componenttest.NewTelemetry()
	t.Cleanup(func() { require.NoError(t, tt.Shutdown(context.Background())) })

	_, err := NewTelemetryBuilder(
		tt.NewTelemetrySettings(),
		WithMetricNamePrefixReplacement("not_a_real_prefix_", "x_"),
	)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, `does not start with prefix "not_a_real_prefix_"`)
	// All three metrics should be reported as mismatches.
	assert.Contains(t, msg, "otelcol_exporter_enqueue_failed_log_records")
	assert.Contains(t, msg, "otelcol_exporter_queue_batch_send_size")
	assert.Contains(t, msg, "otelcol_exporter_queue_size")
}

// TestNameSubstitution_Identity verifies that supplying old == new is a
// supported no-op.
func TestNameSubstitution_Identity(t *testing.T) {
	tt := componenttest.NewTelemetry()
	t.Cleanup(func() { require.NoError(t, tt.Shutdown(context.Background())) })

	tb, err := NewTelemetryBuilder(
		tt.NewTelemetrySettings(),
		WithMetricNamePrefixReplacement("otelcol_exporter_", "otelcol_exporter_"),
	)
	require.NoError(t, err)
	t.Cleanup(tb.Shutdown)

	tb.ExporterEnqueueFailedLogRecords.Add(t.Context(), 1)
	got, err := tt.GetMetric("otelcol_exporter_enqueue_failed_log_records")
	require.NoError(t, err)
	assert.Equal(t, "otelcol_exporter_enqueue_failed_log_records", got.Name)
}

// TestNameSubstitution_EmptyNewPrefix verifies that newPrefix may be empty
// (this strips the configured oldPrefix).
func TestNameSubstitution_EmptyNewPrefix(t *testing.T) {
	tt := componenttest.NewTelemetry()
	t.Cleanup(func() { require.NoError(t, tt.Shutdown(context.Background())) })

	tb, err := NewTelemetryBuilder(
		tt.NewTelemetrySettings(),
		WithMetricNamePrefixReplacement("otelcol_exporter_", ""),
	)
	require.NoError(t, err)
	t.Cleanup(tb.Shutdown)

	tb.ExporterEnqueueFailedLogRecords.Add(t.Context(), 1)
	got, err := tt.GetMetric("enqueue_failed_log_records")
	require.NoError(t, err)
	assert.Equal(t, "enqueue_failed_log_records", got.Name)
}
