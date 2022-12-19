// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package otlpreceiver // import "go.opentelemetry.io/collector/receiver/otlpreceiver"

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configgrpc"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/obsreport"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/otlpreceiver/internal/logs"
	"go.opentelemetry.io/collector/receiver/otlpreceiver/internal/metrics"
	"go.opentelemetry.io/collector/receiver/otlpreceiver/internal/trace"
)

// otlpReceiver is the type that exposes Trace and Metrics reception.
type otlpReceiver struct {
	cfg        *Config
	serverGRPC *grpc.Server
	httpMux    *http.ServeMux
	serverHTTP *http.Server
	obsNet     *obsreport.NetworkReporter

	traceReceiver   *trace.Receiver
	metricsReceiver *metrics.Receiver
	logReceiver     *logs.Receiver
	shutdownWG      sync.WaitGroup

	settings receiver.CreateSettings
}

// newOtlpReceiver just creates the OpenTelemetry receiver services. It is the caller's
// responsibility to invoke the respective Start*Reception methods as well
// as the various Stop*Reception methods to end it.
func newOtlpReceiver(cfg *Config, settings receiver.CreateSettings) (*otlpReceiver, error) {
	obsNet, err := obsreport.NewReceiverNetworkReporter(settings)
	if err != nil {
		return nil, err
	}

	r := &otlpReceiver{
		cfg:      cfg,
		settings: settings,
		obsNet:   obsNet,
	}
	if cfg.HTTP != nil {
		r.httpMux = http.NewServeMux()
	}

	return r, nil
}

func (r *otlpReceiver) startGRPCServer(cfg *configgrpc.GRPCServerSettings, host component.Host) error {
	r.settings.Logger.Info("Starting GRPC server", zap.String("endpoint", cfg.NetAddr.Endpoint))

	gln, err := cfg.ToListener()
	if err != nil {
		return err
	}
	r.shutdownWG.Add(1)
	go func() {
		defer r.shutdownWG.Done()

		if errGrpc := r.serverGRPC.Serve(gln); errGrpc != nil && !errors.Is(errGrpc, grpc.ErrServerStopped) {
			host.ReportFatalError(errGrpc)
		}
	}()
	return nil
}

func (r *otlpReceiver) startHTTPServer(cfg *confighttp.HTTPServerSettings, host component.Host) error {
	r.settings.Logger.Info("Starting HTTP server", zap.String("endpoint", cfg.Endpoint))
	var hln net.Listener
	hln, err := cfg.ToListener()
	if err != nil {
		return err
	}
	r.shutdownWG.Add(1)
	go func() {
		defer r.shutdownWG.Done()

		if errHTTP := r.serverHTTP.Serve(hln); errHTTP != nil && !errors.Is(errHTTP, http.ErrServerClosed) {
			host.ReportFatalError(errHTTP)
		}
	}()
	return nil
}

func (r *otlpReceiver) startProtocolServers(host component.Host) error {
	var err error
	if r.cfg.GRPC != nil {
		var serverOpts []grpc.ServerOption

		if r.obsNet != nil {
			serverOpts = append(serverOpts, grpc.StatsHandler(r))
		}

		r.serverGRPC, err = r.cfg.GRPC.ToServer(host, r.settings.TelemetrySettings, serverOpts...)
		if err != nil {
			return err
		}

		if r.traceReceiver != nil {
			ptraceotlp.RegisterGRPCServer(r.serverGRPC, r.traceReceiver)
		}

		if r.metricsReceiver != nil {
			pmetricotlp.RegisterGRPCServer(r.serverGRPC, r.metricsReceiver)
		}

		if r.logReceiver != nil {
			plogotlp.RegisterGRPCServer(r.serverGRPC, r.logReceiver)
		}

		err = r.startGRPCServer(r.cfg.GRPC, host)
		if err != nil {
			return err
		}
	}
	if r.cfg.HTTP != nil {
		r.serverHTTP, err = r.cfg.HTTP.ToServer(
			host,
			r.settings.TelemetrySettings,
			r.httpMux,
			confighttp.WithErrorHandler(errorHandler),
		)
		if err != nil {
			return err
		}

		err = r.startHTTPServer(r.cfg.HTTP, host)
		if err != nil {
			return err
		}
	}

	return err
}

// Start runs the trace receiver on the gRPC server. Currently
// it also enables the metrics receiver too.
func (r *otlpReceiver) Start(_ context.Context, host component.Host) error {
	return r.startProtocolServers(host)
}

// Shutdown is a method to turn off receiving.
func (r *otlpReceiver) Shutdown(ctx context.Context) error {
	var err error

	if r.serverHTTP != nil {
		err = r.serverHTTP.Shutdown(ctx)
	}

	if r.serverGRPC != nil {
		r.serverGRPC.GracefulStop()
	}

	r.shutdownWG.Wait()
	return err
}

func (r *otlpReceiver) registerTraceConsumer(tc consumer.Traces) error {
	if tc == nil {
		return component.ErrNilNextConsumer
	}
	var err error
	r.traceReceiver, err = trace.New(tc, r.settings)
	if err != nil {
		return err
	}
	if r.httpMux != nil {
		r.httpMux.HandleFunc("/v1/traces", func(resp http.ResponseWriter, req *http.Request) {
			if req.Method != http.MethodPost {
				handleUnmatchedMethod(resp)
				return
			}
			switch req.Header.Get("Content-Type") {
			case pbContentType:
				handleTraces(resp, req, r.traceReceiver, pbEncoder)
			case jsonContentType:
				handleTraces(resp, req, r.traceReceiver, jsEncoder)
			default:
				handleUnmatchedContentType(resp)
			}
		})
	}
	return nil
}

func (r *otlpReceiver) registerMetricsConsumer(mc consumer.Metrics) error {
	if mc == nil {
		return component.ErrNilNextConsumer
	}
	var err error
	r.metricsReceiver, err = metrics.New(mc, r.settings)
	if err != nil {
		return err
	}

	if r.httpMux != nil {
		r.httpMux.HandleFunc("/v1/metrics", func(resp http.ResponseWriter, req *http.Request) {
			if req.Method != http.MethodPost {
				handleUnmatchedMethod(resp)
				return
			}
			switch req.Header.Get("Content-Type") {
			case pbContentType:
				handleMetrics(resp, req, r.metricsReceiver, pbEncoder)
			case jsonContentType:
				handleMetrics(resp, req, r.metricsReceiver, jsEncoder)
			default:
				handleUnmatchedContentType(resp)
			}
		})
	}
	return nil
}

func (r *otlpReceiver) registerLogsConsumer(lc consumer.Logs) error {
	if lc == nil {
		return component.ErrNilNextConsumer
	}
	var err error
	r.logReceiver, err = logs.New(lc, r.settings)
	if err != nil {
		return err
	}

	if r.httpMux != nil {
		r.httpMux.HandleFunc("/v1/logs", func(resp http.ResponseWriter, req *http.Request) {
			if req.Method != http.MethodPost {
				handleUnmatchedMethod(resp)
				return
			}
			switch req.Header.Get("Content-Type") {
			case pbContentType:
				handleLogs(resp, req, r.logReceiver, pbEncoder)
			case jsonContentType:
				handleLogs(resp, req, r.logReceiver, jsEncoder)
			default:
				handleUnmatchedContentType(resp)
			}
		})
	}
	return nil
}

func handleUnmatchedMethod(resp http.ResponseWriter) {
	status := http.StatusMethodNotAllowed
	writeResponse(resp, "text/plain", status, []byte(fmt.Sprintf("%v method not allowed, supported: [POST]", status)))
}

func handleUnmatchedContentType(resp http.ResponseWriter) {
	status := http.StatusUnsupportedMediaType
	writeResponse(resp, "text/plain", status, []byte(fmt.Sprintf("%v unsupported media type, supported: [%s, %s]", status, jsonContentType, pbContentType)))
}

// TagRPC implements grpc/stats.Handler
func (r *otlpReceiver) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}

// HandleRPC implements grpc/stats.Handler
func (r *otlpReceiver) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	switch s := rs.(type) {
	case *stats.Begin, *stats.OutHeader, *stats.OutTrailer, *stats.InHeader, *stats.InTrailer:
		// Note we have some info aboute header WireLength,
		// but intentionally not counting.

	case *stats.InPayload:
		var ss obsreport.SizesStruct
		ss.Length = int64(s.Length)
		ss.WireLength = int64(s.WireLength)
		r.obsNet.CountReceive(ctx, ss)

	case *stats.OutPayload:
		var ss obsreport.SizesStruct
		ss.Length = int64(s.Length)
		ss.WireLength = int64(s.WireLength)
		r.obsNet.CountSend(ctx, ss)
	}
}

// TagConn implements grpc/stats.Handler
func (r *otlpReceiver) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn implements grpc/stats.Handler
func (r *otlpReceiver) HandleConn(_ context.Context, s stats.ConnStats) {
	// Note: ConnBegin and ConnEnd
}
