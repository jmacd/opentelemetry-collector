// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package queue // import "go.opentelemetry.io/collector/exporter/exporterhelper/internal/queue"

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/metadata"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/request"
)

// obsQueue is a helper to add observability to a queue.
type obsQueue[T request.Request] struct {
	Queue[T]
	om     ObsMetrics
	tracer trace.Tracer
}

func newObsQueue[T request.Request](set Settings[T], delegate Queue[T]) (Queue[T], error) {
	// When the caller does not inject telemetry, fall back to the exporter's
	// default metrics. A component reusing the queue (e.g. a processor) supplies
	// its own ObsMetrics so its metrics are named for that component.
	om := set.ObsMetrics
	if om == nil {
		var err error
		if om, err = newExporterObsMetrics(set.Telemetry, set.ID, set.Signal); err != nil {
			return nil, err
		}
	}

	if err := om.RegisterQueueSize(delegate.Size); err != nil {
		return nil, err
	}
	if err := om.RegisterQueueCapacity(delegate.Capacity); err != nil {
		return nil, err
	}

	return &obsQueue[T]{
		Queue:  delegate,
		om:     om,
		tracer: metadata.Tracer(set.Telemetry),
	}, nil
}

func (or *obsQueue[T]) Shutdown(ctx context.Context) error {
	defer or.om.Shutdown()
	return or.Queue.Shutdown(ctx)
}

func (or *obsQueue[T]) Offer(ctx context.Context, req T) error {
	// Have to read the number of items before sending the request since the request can
	// be modified by the downstream components like the batcher.
	numItems := req.ItemsCount()

	or.om.RecordBatchSendSize(ctx, int64(numItems))
	or.om.RecordBatchSendSizeBytes(ctx, int64(req.BytesSize()))

	ctx, span := or.tracer.Start(ctx, "exporter/enqueue")
	err := or.Queue.Offer(ctx, req)
	span.End()

	if err != nil {
		or.om.RecordEnqueueFailure(ctx, int64(numItems))
	}
	return err
}
