// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otlpexporter

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
)

func (e *baseExporter) pushLogsPayload(ctx context.Context, p *payload.Payload) error {
	if e.clientConn == nil {
		return errors.New("otlp exporter not started")
	}
	buf, err := plogpayload.ProtoBytes(p)
	if err != nil {
		return consumererror.NewPermanent(err)
	}
	resp := &plogpayload.WireMessage{}
	err = e.clientConn.Invoke(ctx, "/opentelemetry.proto.collector.logs.v1.LogsService/Export",
		&plogpayload.WireMessage{Body: buf}, resp, e.callOptions...)
	if exportErr := processError(err); exportErr != nil {
		return exportErr
	}
	response := plogotlp.NewExportResponse()
	if err = response.UnmarshalProto(resp.Body); err != nil {
		return consumererror.NewPermanent(err)
	}
	partial := response.PartialSuccess()
	if partial.ErrorMessage() != "" || partial.RejectedLogRecords() != 0 {
		e.settings.Logger.Warn("Partial success response",
			zap.String("message", partial.ErrorMessage()),
			zap.Int64("dropped_log_records", partial.RejectedLogRecords()))
	}
	return nil
}
