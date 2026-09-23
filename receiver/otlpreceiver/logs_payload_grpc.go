// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otlpreceiver

import (
	"context"

	"google.golang.org/grpc"

	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
	"go.opentelemetry.io/collector/receiver/otlpreceiver/internal/logs"
)

type rawLogsServer interface {
	ExportProto(context.Context, []byte) (plogotlp.ExportResponse, error)
}

func registerPayloadLogs(s *grpc.Server, receiver *logs.Receiver) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "opentelemetry.proto.collector.logs.v1.LogsService",
		HandlerType: (*rawLogsServer)(nil),
		Methods: []grpc.MethodDesc{{MethodName: "Export", Handler: func(srv any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			req := &plogpayload.WireMessage{}
			if err := decode(req); err != nil {
				return nil, err
			}
			handle := func(ctx context.Context, input any) (any, error) {
				resp, err := receiver.ExportProto(ctx, input.(*plogpayload.WireMessage).Body)
				if err != nil {
					return nil, err
				}
				wire, err := resp.MarshalProto()
				return &plogpayload.WireMessage{Body: wire}, err
			}
			if interceptor == nil {
				return handle(ctx, req)
			}
			return interceptor(ctx, req, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/opentelemetry.proto.collector.logs.v1.LogsService/Export"}, handle)
		}}},
	}, receiver)
}
