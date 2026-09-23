// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package exporterhelper

import (
	"context"

	"go.opentelemetry.io/collector/exporter/exporterhelper/internal"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

// WithLogsPayload supplies the native logs pusher when service.PluggableLogs is
// enabled. Set this option before WithQueue so native queue codecs are selected.
func WithLogsPayload(pusher func(context.Context, *payload.Payload) error) Option {
	return internal.WithLogsPayload(pusher)
}
