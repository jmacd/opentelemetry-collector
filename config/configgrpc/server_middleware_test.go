// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package configgrpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configmiddleware"
	"go.opentelemetry.io/collector/config/confignet"
	"go.opentelemetry.io/collector/config/configtls"
	"go.opentelemetry.io/collector/extension"
	"go.opentelemetry.io/collector/extension/extensionmiddleware"
	"go.opentelemetry.io/collector/extension/extensionmiddleware/extensionmiddlewaretest"
)

// contextKey is a private type for keys defined in this test.
type contextKey int

// Key for the slice of middleware names in the context.
const middlewareCallsKey contextKey = 0

// getMiddlewareCalls retrieves the middleware calls from context or returns an empty slice.
func getMiddlewareCalls(ctx context.Context) []string {
	calls, ok := ctx.Value(middlewareCallsKey).([]string)
	if !ok {
		return []string{}
	}
	return calls
}

// testServerMiddleware is a test implementation of configmiddleware.Middleware
type testServerMiddleware struct {
	extension.Extension
	extensionmiddleware.GetGRPCServerOptionsFunc
}

func newTestServerMiddleware(name string) extension.Extension {
	return &testServerMiddleware{
		Extension: extensionmiddlewaretest.NewNop(),
		GetGRPCServerOptionsFunc: func() ([]grpc.ServerOption, error) {
			return []grpc.ServerOption{grpc.ChainUnaryInterceptor(
				func(
					ctx context.Context,
					req any, _ *grpc.UnaryServerInfo,
					handler grpc.UnaryHandler,
				) (any, error) {
					ctx = context.WithValue(ctx, middlewareCallsKey, append(getMiddlewareCalls(ctx), name))
					return handler(ctx, req)
				})}, nil
		},
	}
}

func TestGrpcServerUnaryInterceptor(t *testing.T) {
	// Register two test extensions
	host := &mockHost{
		ext: map[component.ID]component.Component{
			component.MustNewID("test1"): newTestServerMiddleware("test1"),
			component.MustNewID("test2"): newTestServerMiddleware("test2"),
		},
	}

	// Setup the server with both middleware options
	server := &grpcTraceServer{}
	var addr string

	// Create the server with middleware interceptors
	{
		var srv *grpc.Server
		srv, addr = server.startTestServerWithHost(t, ServerConfig{
			NetAddr: confignet.AddrConfig{
				Endpoint:  "localhost:0",
				Transport: confignet.TransportTypeTCP,
			},
			Middlewares: []configmiddleware.Config{
				newTestMiddlewareConfig("test1"),
				newTestMiddlewareConfig("test2"),
			},
		}, host)
		defer srv.Stop()
	}

	// Send a request to trigger the interceptors
	resp, errResp := sendTestRequest(t, ClientConfig{
		Endpoint: addr,
		TLSSetting: configtls.ClientConfig{
			Insecure: true,
		},
	})
	require.NoError(t, errResp)
	require.NotNil(t, resp)

	// Verify interceptors were called in the correct order
	assert.Equal(t, []string{"test1", "test2"}, getMiddlewareCalls(server.recordedContext))
}
