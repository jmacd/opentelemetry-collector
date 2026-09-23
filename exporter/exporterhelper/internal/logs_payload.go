// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"context"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/queuebatch"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
	"go.opentelemetry.io/collector/pdata/xpdata/pref"
)

type payloadLogsExporter struct {
	*BaseExporter
	registry *payload.Registry
}

func (e *payloadLogsExporter) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (e *payloadLogsExporter) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	pref.RefLogs(ld)
	p, err := plogpayload.NewFromLogs(e.registry, ld)
	if err != nil {
		pref.UnrefLogs(ld)
		return consumererror.NewPermanent(err)
	}
	defer p.Release()
	return e.ConsumeLogsPayload(ctx, p)
}

func (e *payloadLogsExporter) ConsumeLogsPayload(ctx context.Context, p *payload.Payload) error {
	req, err := queuebatch.NewPayloadRequest(p)
	if err != nil {
		return consumererror.NewPermanent(err)
	}
	return e.Send(ctx, req)
}

func WithLogsPayload(pusher func(context.Context, *payload.Payload) error) Option {
	return func(be *BaseExporter) error {
		if !plogpayload.FeatureGate.IsEnabled() {
			return nil
		}
		be.PayloadPusher = pusher
		be.queueBatchSettings = queuebatch.NewPayloadQueueBatchSettings()
		return nil
	}
}
