// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package exporterhelper

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/config/configretry"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/featuregate"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
	"go.opentelemetry.io/collector/pdata/xpdata/pref"
)

func TestNativePartialRetry(t *testing.T) {
	gate := plogpayload.FeatureGate
	before := gate.IsEnabled()
	require.NoError(t, featuregate.GlobalRegistry().Set(gate.ID(), true))
	defer func() { require.NoError(t, featuregate.GlobalRegistry().Set(gate.ID(), before)) }()
	codec := plogpayload.ProtoCodec()
	codec.Decode = func(payload.Representation) (payload.Representation, error) {
		t.Error("partial retry decoded objects")
		return nil, errors.New("object conversion forbidden")
	}
	reg, err := payload.NewRegistry(plogpayload.ObjectsCodec(), codec)
	require.NoError(t, err)
	ld := plog.NewLogs()
	logs := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for range 7 {
		logs.AppendEmpty().Body().SetStr("test")
	}
	buf, err := plogotlp.NewExportRequestFromLogs(ld).MarshalProto()
	require.NoError(t, err)
	p, err := plogpayload.NewFromProto(reg, buf)
	require.NoError(t, err)
	defer p.Release()
	var counts []int
	retry := configretry.NewDefaultBackOffConfig()
	retry.InitialInterval = time.Millisecond
	retry.MaxInterval = time.Millisecond
	retry.MaxElapsedTime = time.Second
	set := exportertest.NewNopSettings(component.MustNewType("test"))
	exp, err := NewLogs(t.Context(), set, &struct{}{}, func(context.Context, plog.Logs) error {
		t.Error("legacy logs pusher called")
		return errors.New("unexpected legacy pusher")
	}, WithLogsPayload(func(_ context.Context, p *payload.Payload) error {
		count, countErr := p.ItemsCount()
		if countErr != nil {
			return countErr
		}
		counts = append(counts, count)
		if len(counts) == 1 {
			return &payload.PartialError{Err: errors.New("retry last four"), Retry: []payload.LogRange{{Start: 3, End: 7}}}
		}
		return nil
	}), WithRetry(retry))
	require.NoError(t, err)
	require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
	require.NoError(t, exp.(consumer.LogsPayload).ConsumeLogsPayload(t.Context(), p))
	require.NoError(t, exp.Shutdown(t.Context()))
	assert.Equal(t, []int{7, 4}, counts)
}

func TestNativeQueueRetainsLegacyPooledObjects(t *testing.T) {
	for _, id := range []string{plogpayload.FeatureGate.ID(), "pdata.useProtoPooling"} {
		var before bool
		featuregate.GlobalRegistry().VisitAll(func(g *featuregate.Gate) {
			if g.ID() == id {
				before = g.IsEnabled()
			}
		})
		require.NoError(t, featuregate.GlobalRegistry().Set(id, true))
		t.Cleanup(func() { require.NoError(t, featuregate.GlobalRegistry().Set(id, before)) })
	}
	started, release := make(chan struct{}), make(chan struct{})
	observed := make(chan int, 1)
	queue := NewDefaultQueueConfig()
	queue.NumConsumers = 1
	queue.WaitForResult = false
	set := exportertest.NewNopSettings(component.MustNewType("test"))
	exp, err := NewLogs(t.Context(), set, &struct{}{}, func(context.Context, plog.Logs) error {
		return errors.New("legacy pusher must not run")
	}, WithLogsPayload(func(_ context.Context, p *payload.Payload) error {
		close(started)
		<-release
		ld, err := plogpayload.ReadOnlyLogs(p)
		if err != nil {
			return err
		}
		observed <- ld.LogRecordCount()
		return nil
	}), WithQueue(configoptional.Some(queue)))
	require.NoError(t, err)
	require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("pool-survivor")
	pref.MarkPipelineOwnedLogs(ld)
	require.NoError(t, exp.ConsumeLogs(t.Context(), ld))
	pref.UnrefLogs(ld)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("queue did not start consuming")
	}
	close(release)
	select {
	case count := <-observed:
		assert.Equal(t, 1, count)
	case <-time.After(5 * time.Second):
		t.Fatal("queued data was not delivered")
	}
	require.NoError(t, exp.Shutdown(t.Context()))
}
