// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
	"go.opentelemetry.io/collector/receiver/otlpreceiver/internal/errors"
)

func (r *Receiver) ExportProto(ctx context.Context, buf []byte) (plogotlp.ExportResponse, error) {
	reg, err := plogpayload.NewRegistry()
	if err != nil {
		return plogotlp.NewExportResponse(), err
	}
	p, err := plogpayload.NewFromProto(reg, buf)
	if err != nil {
		return plogotlp.NewExportResponse(), status.Error(codes.InvalidArgument, err.Error())
	}
	defer p.Release()
	count, err := p.ItemsCount()
	if err != nil {
		return plogotlp.NewExportResponse(), status.Error(codes.InvalidArgument, err.Error())
	}
	if count == 0 {
		return plogotlp.NewExportResponse(), nil
	}
	ctx = r.obsreport.StartLogsOp(ctx)
	err = consumer.ConsumeLogsPayload(ctx, r.nextConsumer, p)
	r.obsreport.EndLogsOp(ctx, dataFormatProtobuf, count, err)
	if err != nil {
		err = errors.GetStatusFromError(err)
	}
	return plogotlp.NewExportResponse(), err
}
