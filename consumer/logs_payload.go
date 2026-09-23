// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package consumer

import (
	"context"

	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
	"go.opentelemetry.io/collector/pdata/xpdata/pref"
)

// LogsPayload is the experimental native extension of Logs. The payload is
// borrowed for the call. An asynchronous consumer must retain its own reference.
type LogsPayload interface {
	ConsumeLogsPayload(context.Context, *payload.Payload) error
}

type ConsumeLogsPayloadFunc func(context.Context, *payload.Payload) error

func (f ConsumeLogsPayloadFunc) ConsumeLogsPayload(ctx context.Context, p *payload.Payload) error {
	return f(ctx, p)
}

// ConsumeLogsPayload dispatches natively, or crosses an explicit legacy object
// boundary. Mutating legacy consumers receive an independent writable copy.
func ConsumeLogsPayload(ctx context.Context, next Logs, p *payload.Payload) error {
	if native, ok := next.(LogsPayload); ok {
		return native.ConsumeLogsPayload(ctx, p)
	}
	if next.Capabilities().MutatesData {
		ld, err := plogpayload.MutableLogs(p)
		if err != nil {
			return consumererror.NewPermanent(err)
		}
		pref.MarkPipelineOwnedLogs(ld)
		defer pref.UnrefLogs(ld)
		return next.ConsumeLogs(ctx, ld)
	}
	ld, err := plogpayload.ReadOnlyLogs(p)
	if err != nil {
		return consumererror.NewPermanent(err)
	}
	return next.ConsumeLogs(ctx, ld)
}
