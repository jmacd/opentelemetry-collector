// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package pdataprototype

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protowire"

	"go.opentelemetry.io/collector/featuregate"
	"go.opentelemetry.io/collector/otelcol"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
)

func address(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

type nativeSinkServer interface {
	Export(context.Context, *plogpayload.WireMessage) (*plogpayload.WireMessage, error)
}

type nativeSink struct {
	t         *testing.T
	delivered atomic.Int64
}

func (s *nativeSink) Export(_ context.Context, wire *plogpayload.WireMessage) (*plogpayload.WireMessage, error) {
	assert.Contains(s.t, string(wire.Body), "unknown-wire-field-must-survive")
	req := plogotlp.NewExportRequest()
	if err := req.UnmarshalProto(wire.Body); err != nil {
		return nil, err
	}
	assert.LessOrEqual(s.t, req.Logs().LogRecordCount(), 3)
	s.delivered.Add(int64(req.Logs().LogRecordCount()))
	return &plogpayload.WireMessage{}, nil
}

func grpcBackend(t *testing.T) (string, *nativeSink) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	sink := &nativeSink{t: t}
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "opentelemetry.proto.collector.logs.v1.LogsService",
		HandlerType: (*nativeSinkServer)(nil),
		Methods: []grpc.MethodDesc{{MethodName: "Export", Handler: func(service any, ctx context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			wire := &plogpayload.WireMessage{}
			if err := decode(wire); err != nil {
				return nil, err
			}
			return service.(nativeSinkServer).Export(ctx, wire)
		}}},
	}, sink)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		require.NoError(t, <-done)
	})
	return listener.Addr().String(), sink
}

func TestCollectorNativeLogs(t *testing.T) {
	gate := plogpayload.FeatureGate
	previous := gate.IsEnabled()
	require.NoError(t, featuregate.GlobalRegistry().Set(gate.ID(), true))
	t.Cleanup(func() { require.NoError(t, featuregate.GlobalRegistry().Set(gate.ID(), previous)) })
	for _, source := range []string{"resource", "log"} {
		t.Run(source, func(t *testing.T) {
			var mu sync.Mutex
			delivered := map[string]int{}
			batches := 0
			const marker = "unknown-wire-field-must-survive"
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				buf, err := io.ReadAll(r.Body)
				if !assert.NoError(t, err) {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				assert.Contains(t, string(buf), marker, "an object conversion discarded the unknown wire field")
				req := plogotlp.NewExportRequest()
				if !assert.NoError(t, req.UnmarshalProto(buf)) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				assert.LessOrEqual(t, req.Logs().LogRecordCount(), 3)
				mu.Lock()
				delivered[r.URL.Path] += req.Logs().LogRecordCount()
				batches++
				mu.Unlock()
				w.Header().Set("Content-Type", "application/x-protobuf")
			}))
			defer backend.Close()
			grpcOutput, grpcSink := grpcBackend(t)
			httpAddr, grpcAddr := address(t), address(t)
			key := "service.name"
			if source == "log" {
				key = "route"
			}
			config := fmt.Sprintf(`
receivers:
  otlp:
    protocols:
      http: {endpoint: %q}
      grpc: {endpoint: %q}
connectors:
  view_route:
    source: %s
    key: %s
    value: match
    match: [logs/match]
    other: [logs/other]
exporters:
  debug:
    verbosity: detailed
    sampling_initial: 100
    sampling_thereafter: 1
  otlp_http/match:
    logs_endpoint: %q
    compression: none
    sending_queue: &queue
      queue_size: 100
      sizer: items
      wait_for_result: true
      batch: {min_size: 3, max_size: 3, flush_timeout: 5ms}
  otlp_http/other:
    logs_endpoint: %q
    compression: none
    sending_queue: *queue
  otlp_grpc:
    endpoint: %q
    tls: {insecure: true}
    sending_queue: *queue
service:
  telemetry:
    metrics: {level: none}
  pipelines:
    logs/input:
      receivers: [otlp]
      exporters: [view_route]
    logs/match:
      receivers: [view_route]
      exporters: [debug, otlp_http/match, otlp_grpc]
    logs/other:
      receivers: [view_route]
      exporters: [debug, otlp_http/other, otlp_grpc]
`, httpAddr, grpcAddr, source, key, backend.URL+"/match", backend.URL+"/other", grpcOutput)
			core, observed := observer.New(zap.InfoLevel)
			settings := Settings()
			settings.DisableGracefulShutdown = true
			settings.SkipSettingGRPCLogger = true
			settings.ConfigProviderSettings.ResolverSettings.URIs = []string{"yaml:" + config}
			settings.LoggingOptions = []zap.Option{zap.WrapCore(func(zapcore.Core) zapcore.Core { return core })}
			col, err := otelcol.NewCollector(settings)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- col.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				col.Shutdown()
				require.NoError(t, <-done)
			})
			require.Eventually(t, func() bool { return col.GetState() == otelcol.StateRunning }, 15*time.Second, 10*time.Millisecond)
			ld := plog.NewLogs()
			for r := range 2 {
				rl := ld.ResourceLogs().AppendEmpty()
				name := "match"
				if r == 1 {
					name = "other"
				}
				rl.Resource().Attributes().PutStr("service.name", name)
				sl := rl.ScopeLogs().AppendEmpty()
				sl.Scope().SetName("native-e2e")
				for i := range 7 {
					log := sl.LogRecords().AppendEmpty()
					log.Body().SetStr(fmt.Sprintf("message-%d-%d", r, i))
					route := "match"
					if i%2 == 1 {
						route = "other"
					}
					log.Attributes().PutStr("route", route)
				}
			}
			wire, err := plogotlp.NewExportRequestFromLogs(ld).MarshalProto()
			require.NoError(t, err)
			wire = protowire.AppendString(protowire.AppendTag(wire, 100, protowire.BytesType), marker)
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Post("http://"+httpAddr+"/v1/logs", "application/x-protobuf", bytes.NewReader(wire))
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
			conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			defer conn.Close()
			rpcCtx, rpcCancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer rpcCancel()
			require.NoError(t, conn.Invoke(rpcCtx, "/opentelemetry.proto.collector.logs.v1.LogsService/Export",
				&plogpayload.WireMessage{Body: wire}, &plogpayload.WireMessage{}))
			mu.Lock()
			wantMatch, wantOther := 14, 14
			if source == "log" {
				wantMatch, wantOther = 16, 12
			}
			assert.Equal(t, wantMatch, delivered["/match"])
			assert.Equal(t, wantOther, delivered["/other"])
			assert.Greater(t, batches, 4)
			mu.Unlock()
			assert.Equal(t, int64(28), grpcSink.delivered.Load())
			assert.Positive(t, observed.FilterMessageSnippet("LogRecord:").Len(), "debug must render through native views")
			resp, err = client.Post("http://"+httpAddr+"/v1/logs", "application/x-protobuf", bytes.NewReader([]byte{0xff}))
			require.NoError(t, err)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		})
	}
}
