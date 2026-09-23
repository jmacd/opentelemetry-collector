// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otlphttpexporter

import (
	"context"

	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
)

func (e *baseExporter) pushLogsPayload(ctx context.Context, p *payload.Payload) error {
	if e.config.Encoding == EncodingJSON {
		ld, err := plogpayload.ReadOnlyLogs(p)
		if err != nil {
			return consumererror.NewPermanent(err)
		}
		return e.pushLogs(ctx, ld)
	}
	buf, err := plogpayload.ProtoBytes(p)
	if err != nil {
		return consumererror.NewPermanent(err)
	}
	return e.export(ctx, e.logsURL, buf, e.logsPartialSuccessHandler)
}
