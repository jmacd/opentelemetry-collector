// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package queue // import "go.opentelemetry.io/collector/exporter/exporterhelper/internal/queue"

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/metadata"
	"go.opentelemetry.io/collector/pipeline"
	"go.opentelemetry.io/collector/pipeline/xpipeline"
)

const (
	// exporterKey used to identify exporters in metrics and traces.
	exporterKey = "exporter"

	// dataTypeKey used to identify the data type in the queue size metric.
	dataTypeKey = "data_type"
)

// ObsMetrics reports the telemetry produced by a Queue. It is supplied by the
// component hosting the queue (e.g. an exporter or, in the future, a processor)
// so that the queue's metrics are defined and named by that component. This lets
// the same queue code be reused across components, each reporting under its own
// metric names and attributes, without renaming anything. newExporterObsMetrics
// is the default implementation used by exporterhelper.
type ObsMetrics interface {
	// RecordBatchSendSize records the number of items in a batch offered to the queue.
	RecordBatchSendSize(ctx context.Context, items int64)
	// RecordBatchSendSizeBytes records the number of bytes in a batch offered to the queue.
	RecordBatchSendSizeBytes(ctx context.Context, bytes int64)
	// RecordEnqueueFailure records the number of items that failed to be enqueued.
	RecordEnqueueFailure(ctx context.Context, items int64)
	// RegisterQueueSize registers an observable callback reporting the current queue size.
	RegisterQueueSize(observe func() int64) error
	// RegisterQueueCapacity registers an observable callback reporting the queue capacity.
	RegisterQueueCapacity(observe func() int64) error
	// Shutdown unregisters the observable callbacks registered above.
	Shutdown()
}

// exporterObsMetrics is the default ObsMetrics implementation. It reports the
// otelcol_exporter_* queue metrics defined in exporterhelper's metadata.yaml.
type exporterObsMetrics struct {
	tb                      *metadata.TelemetryBuilder
	metricAttr              metric.MeasurementOption
	asyncAttr               metric.MeasurementOption
	enqueueFailedInst       metric.Int64Counter
	queueBatchSizeInst      metric.Int64Histogram
	queueBatchSizeBytesInst metric.Int64Histogram
}

func newExporterObsMetrics(tel component.TelemetrySettings, id component.ID, signal pipeline.Signal) (ObsMetrics, error) {
	tb, err := metadata.NewTelemetryBuilder(tel)
	if err != nil {
		return nil, err
	}

	exporterAttr := attribute.String(exporterKey, id.String())
	om := &exporterObsMetrics{
		tb:                      tb,
		metricAttr:              metric.WithAttributeSet(attribute.NewSet(exporterAttr)),
		asyncAttr:               metric.WithAttributeSet(attribute.NewSet(exporterAttr, attribute.String(dataTypeKey, signal.String()))),
		queueBatchSizeInst:      tb.ExporterQueueBatchSendSize,
		queueBatchSizeBytesInst: tb.ExporterQueueBatchSendSizeBytes,
	}

	switch signal {
	case pipeline.SignalTraces:
		om.enqueueFailedInst = tb.ExporterEnqueueFailedSpans
	case pipeline.SignalMetrics:
		om.enqueueFailedInst = tb.ExporterEnqueueFailedMetricPoints
	case pipeline.SignalLogs:
		om.enqueueFailedInst = tb.ExporterEnqueueFailedLogRecords
	case xpipeline.SignalProfiles:
		om.enqueueFailedInst = tb.ExporterEnqueueFailedProfileSamples
	}

	return om, nil
}

func (om *exporterObsMetrics) RecordBatchSendSize(ctx context.Context, items int64) {
	om.queueBatchSizeInst.Record(ctx, items, om.metricAttr)
}

func (om *exporterObsMetrics) RecordBatchSendSizeBytes(ctx context.Context, bytes int64) {
	om.queueBatchSizeBytesInst.Record(ctx, bytes, om.metricAttr)
}

func (om *exporterObsMetrics) RecordEnqueueFailure(ctx context.Context, items int64) {
	// enqueueFailedInst is nil for an unrecognized signal.
	if om.enqueueFailedInst != nil {
		om.enqueueFailedInst.Add(ctx, items, om.metricAttr)
	}
}

func (om *exporterObsMetrics) RegisterQueueSize(observe func() int64) error {
	return om.tb.RegisterExporterQueueSizeCallback(func(_ context.Context, o metric.Int64Observer) error {
		o.Observe(observe(), om.asyncAttr)
		return nil
	})
}

func (om *exporterObsMetrics) RegisterQueueCapacity(observe func() int64) error {
	return om.tb.RegisterExporterQueueCapacityCallback(func(_ context.Context, o metric.Int64Observer) error {
		o.Observe(observe(), om.asyncAttr)
		return nil
	})
}

func (om *exporterObsMetrics) Shutdown() {
	om.tb.Shutdown()
}
