// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package queue // import "go.opentelemetry.io/collector/exporter/exporterhelper/internal/queue"

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/kindtelemetry"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/metadata"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/request"
	"go.opentelemetry.io/collector/pipeline"
	"go.opentelemetry.io/collector/pipeline/xpipeline"
)

const (
	// exporterKey is the legacy attribute key for the Exporter kind. The
	// Identity-driven AttributeKey is the source of truth at runtime; this
	// constant is kept for backward-compatibility of in-tree tests.
	exporterKey = "exporter"

	// DataTypeKey used to identify the data type in the queue size metric.
	dataTypeKey = "data_type"
)

// obsQueue is a helper to add observability to a queue.
type obsQueue[T request.Request] struct {
	Queue[T]
	tb                      *metadata.TelemetryBuilder
	metricAttr              metric.MeasurementOption
	enqueueFailedInst       metric.Int64Counter
	queueBatchSizeInst      metric.Int64Histogram
	queueBatchSizeBytesInst metric.Int64Histogram
	tracer                  trace.Tracer
	spanName                string
}

func newObsQueue[T request.Request](set Settings[T], delegate Queue[T]) (Queue[T], error) {
	kindID := set.KindID
	if kindID.AttributeKey == "" {
		kindID = kindtelemetry.Default()
	}
	tb, err := metadata.NewTelemetryBuilder(set.Telemetry, kindID.TelemetryBuilderOptions()...)
	if err != nil {
		return nil, err
	}

	componentAttr := attribute.String(kindID.AttributeKey, set.ID.String())
	asyncAttr := metric.WithAttributeSet(attribute.NewSet(componentAttr, attribute.String(dataTypeKey, set.Signal.String())))
	err = tb.RegisterExporterQueueSizeCallback(func(_ context.Context, o metric.Int64Observer) error {
		o.Observe(delegate.Size(), asyncAttr)
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = tb.RegisterExporterQueueCapacityCallback(func(_ context.Context, o metric.Int64Observer) error {
		o.Observe(delegate.Capacity(), asyncAttr)
		return nil
	})
	if err != nil {
		return nil, err
	}

	tracer := metadata.Tracer(set.Telemetry)

	or := &obsQueue[T]{
		Queue:      delegate,
		tb:         tb,
		metricAttr: metric.WithAttributeSet(attribute.NewSet(componentAttr)),
		tracer:     tracer,
		spanName:   kindID.SpanNamespace + "/enqueue",
	}

	switch set.Signal {
	case pipeline.SignalTraces:
		or.enqueueFailedInst = tb.ExporterEnqueueFailedSpans
	case pipeline.SignalMetrics:
		or.enqueueFailedInst = tb.ExporterEnqueueFailedMetricPoints
	case pipeline.SignalLogs:
		or.enqueueFailedInst = tb.ExporterEnqueueFailedLogRecords
	case xpipeline.SignalProfiles:
		or.enqueueFailedInst = tb.ExporterEnqueueFailedProfileSamples
	}

	or.queueBatchSizeInst = tb.ExporterQueueBatchSendSize
	or.queueBatchSizeBytesInst = tb.ExporterQueueBatchSendSizeBytes

	return or, nil
}

func (or *obsQueue[T]) Shutdown(ctx context.Context) error {
	defer or.tb.Shutdown()
	return or.Queue.Shutdown(ctx)
}

func (or *obsQueue[T]) Offer(ctx context.Context, req T) error {
	// Have to read the number of items before sending the request since the request can
	// be modified by the downstream components like the batcher.
	numItems := req.ItemsCount()

	or.queueBatchSizeInst.Record(ctx, int64(numItems), or.metricAttr)
	or.queueBatchSizeBytesInst.Record(ctx, int64(req.BytesSize()), or.metricAttr)

	ctx, span := or.tracer.Start(ctx, or.spanName)
	err := or.Queue.Offer(ctx, req)
	span.End()

	// No metrics recorded for profiles, remove enqueueFailedInst check with nil when profiles metrics available.
	if err != nil && or.enqueueFailedInst != nil {
		or.enqueueFailedInst.Add(ctx, int64(numItems), or.metricAttr)
	}
	return err
}
