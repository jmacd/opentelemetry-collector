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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"go.opentelemetry.io/collector/featuregate"
	"go.opentelemetry.io/collector/otelcol"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
)

// BenchmarkCollectorRelay includes both HTTP hops, real service wrappers, and
// the real exporterhelper queue with end-to-end wait_for_result acknowledgments.
func BenchmarkCollectorRelay(b *testing.B) {
	for _, mode := range []string{"eager", "native"} {
		b.Run(mode, func(b *testing.B) {
			gate := plogpayload.FeatureGate
			before := gate.IsEnabled()
			require.NoError(b, featuregate.GlobalRegistry().Set(gate.ID(), mode == "native"))
			defer func() { require.NoError(b, featuregate.GlobalRegistry().Set(gate.ID(), before)) }()
			var delivered atomic.Int64
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					b.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				delivered.Add(1)
				w.Header().Set("Content-Type", "application/x-protobuf")
			}))
			defer backend.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(b, err)
			addr := listener.Addr().String()
			require.NoError(b, listener.Close())
			set := Settings()
			set.DisableGracefulShutdown = true
			set.SkipSettingGRPCLogger = true
			set.LoggingOptions = []zap.Option{zap.WrapCore(func(zapcore.Core) zapcore.Core { return zapcore.NewNopCore() })}
			set.ConfigProviderSettings.ResolverSettings.URIs = []string{"yaml:" + fmt.Sprintf(`
receivers:
  otlp:
    protocols:
      http: {endpoint: %q}
exporters:
  otlp_http:
    endpoint: %q
    compression: none
    sending_queue:
      queue_size: 100
      sizer: requests
      wait_for_result: true
service:
  telemetry:
    metrics: {level: none}
  pipelines:
    logs:
      receivers: [otlp]
      exporters: [otlp_http]
`, addr, backend.URL)}
			col, err := otelcol.NewCollector(set)
			require.NoError(b, err)
			ctx, cancel := context.WithCancel(b.Context())
			done := make(chan error, 1)
			go func() { done <- col.Run(ctx) }()
			defer func() {
				cancel()
				col.Shutdown()
				require.NoError(b, <-done)
			}()
			require.Eventually(b, func() bool { return col.GetState() == otelcol.StateRunning }, 15*time.Second, time.Millisecond)
			ld := plog.NewLogs()
			rl := ld.ResourceLogs().AppendEmpty()
			rl.Resource().Attributes().PutStr("service.name", "relay")
			records := rl.ScopeLogs().AppendEmpty().LogRecords()
			for i := range 128 {
				log := records.AppendEmpty()
				log.Body().SetStr("representative immutable log body")
				log.Attributes().PutInt("index", int64(i))
				log.Attributes().PutStr("route", "matched")
			}
			buf, err := plogotlp.NewExportRequestFromLogs(ld).MarshalProto()
			require.NoError(b, err)
			client := &http.Client{Timeout: 10 * time.Second}
			defer client.CloseIdleConnections()
			send := func() {
				resp, err := client.Post("http://"+addr+"/v1/logs", "application/x-protobuf", bytes.NewReader(buf))
				if err != nil {
					b.Fatal(err)
				}
				if resp.StatusCode != http.StatusOK {
					b.Fatalf("unexpected response %d", resp.StatusCode)
				}
				_, err = io.Copy(io.Discard, resp.Body)
				closeErr := resp.Body.Close()
				if err != nil || closeErr != nil {
					b.Fatalf("read/close response: %v / %v", err, closeErr)
				}
			}
			send()
			b.ReportAllocs()
			b.SetBytes(int64(len(buf)))
			b.ResetTimer()
			for b.Loop() {
				send()
			}
			require.Equal(b, int64(b.N+1), delivered.Load())
		})
	}
}
