// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package debugexporter

import (
	"bytes"
	"context"

	"go.uber.org/zap"

	"go.opentelemetry.io/collector/config/configtelemetry"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

func (s *debugExporter) pushLogsPayload(_ context.Context, p *payload.Payload) error {
	count, err := p.ItemsCount()
	if err != nil {
		return consumererror.NewPermanent(err)
	}
	view, err := p.View()
	if err != nil {
		return consumererror.NewPermanent(err)
	}
	resources := 0
	if err = view.Resources(func(payload.ResourceLogs) error { resources++; return nil }); err != nil {
		return consumererror.NewPermanent(err)
	}
	var buf bytes.Buffer
	if s.verbosity != configtelemetry.LevelBasic {
		if err = payload.WriteDebug(&buf, view); err != nil {
			return consumererror.NewPermanent(err)
		}
	}
	s.logger.Info("Logs", zap.Int("resource logs", resources), zap.Int("log records", count))
	if buf.Len() > 0 {
		s.logger.Info(buf.String())
	}
	return nil
}
