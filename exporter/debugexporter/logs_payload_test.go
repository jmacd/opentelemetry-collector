// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package debugexporter

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configtelemetry"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/featuregate"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload/otap"
)

func TestFactoryNativeDebugNeverMaterializes(t *testing.T) {
	gate := plogpayload.FeatureGate
	before := gate.IsEnabled()
	require.NoError(t, featuregate.GlobalRegistry().Set(gate.ID(), true))
	defer func() { require.NoError(t, featuregate.GlobalRegistry().Set(gate.ID(), before)) }()
	for _, format := range []payload.Format{plogpayload.ProtoFormat, plogpayload.ArrowFormat} {
		t.Run(string(format), func(t *testing.T) {
			arrow := otap.NewArrowCodec()
			defer func() { require.NoError(t, arrow.Close()) }()
			protoCodec, arrowCodec := plogpayload.ProtoCodec(), arrow.Codec()
			forbidden := func(payload.Representation) (payload.Representation, error) {
				t.Error("debug exporter materialized objects")
				return nil, errors.New("objects forbidden")
			}
			protoCodec.Decode, arrowCodec.Decode = forbidden, forbidden
			reg, err := payload.NewRegistry(plogpayload.ObjectsCodec(), protoCodec, arrowCodec)
			require.NoError(t, err)
			ld := plog.NewLogs()
			rl := ld.ResourceLogs().AppendEmpty()
			rl.Resource().Attributes().PutStr("service.name", "native-service")
			lr := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
			lr.Body().SetEmptyMap().PutStr("message", "native-body")
			var p *payload.Payload
			if format == plogpayload.ProtoFormat {
				buf, marshalErr := plogotlp.NewExportRequestFromLogs(ld).MarshalProto()
				require.NoError(t, marshalErr)
				p, err = plogpayload.NewFromProto(reg, buf)
				require.NoError(t, err)
			} else {
				objects, objectErr := plogpayload.NewFromLogs(reg, ld)
				require.NoError(t, objectErr)
				rep, objectErr := objects.As(plogpayload.ArrowFormat)
				require.NoError(t, objectErr)
				groups := rep.(*otap.Records).Batches()
				for _, batch := range groups {
					for _, record := range batch {
						record.Record().Retain()
					}
				}
				p, err = otap.NewFromRecords(reg, groups)
				require.NoError(t, err)
				objects.Release()
			}
			defer p.Release()
			core, logged := observer.New(zap.InfoLevel)
			factory := NewFactory()
			cfg := factory.CreateDefaultConfig().(*Config)
			cfg.Verbosity = configtelemetry.LevelDetailed
			set := exportertest.NewNopSettings(factory.Type())
			set.Logger = zap.New(core)
			exp, err := factory.CreateLogs(t.Context(), set, cfg)
			require.NoError(t, err)
			require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
			require.NoError(t, exp.(consumer.LogsPayload).ConsumeLogsPayload(t.Context(), p))
			require.NoError(t, exp.Shutdown(t.Context()))
			assert.Positive(t, logged.FilterMessageSnippet("native-service").Len())
			assert.Positive(t, logged.FilterMessageSnippet("native-body").Len())
		})
	}
}
