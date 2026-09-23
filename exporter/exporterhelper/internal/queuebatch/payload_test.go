// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package queuebatch

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/experr"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/hosttest"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/request"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/storagetest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
	"go.opentelemetry.io/collector/pipeline"
)

func TestPayloadQueueEncodingAndSplitting(t *testing.T) {
	t.Parallel()
	reg, err := plogpayload.NewRegistry()
	require.NoError(t, err)
	ld := plog.NewLogs()
	records := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for range 7 {
		records.AppendEmpty().Body().SetStr("retained")
	}
	buf, err := plogotlp.NewExportRequestFromLogs(ld).MarshalProto()
	require.NoError(t, err)
	p, err := plogpayload.NewFromProto(reg, buf)
	require.NoError(t, err)
	defer p.Release()
	req, err := NewPayloadRequest(p)
	require.NoError(t, err)
	ctx := client.NewContext(t.Context(), client.Info{Metadata: client.NewMetadata(map[string][]string{"tenant": {"a", "b"}})})
	encoded, err := (payloadEncoding{}).Marshal(ctx, req)
	require.NoError(t, err)
	restoreCtx, restored, err := (payloadEncoding{}).Unmarshal(encoded)
	require.NoError(t, err)
	defer restored.(*PayloadRequest).Release()
	assert.Equal(t, []string{"a", "b"}, client.FromContext(restoreCtx).Metadata.Get("tenant"))
	assert.Equal(t, 7, restored.ItemsCount())
	bytes, err := plogpayload.ProtoBytes(restored.(*PayloadRequest).Data)
	require.NoError(t, err)
	assert.Equal(t, buf, bytes)
	encoded[len(encoded)-1] ^= 1
	_, _, err = (payloadEncoding{}).Unmarshal(encoded)
	require.Error(t, err)
	parts, err := req.MergeSplit(t.Context(), 3, request.SizerTypeItems, nil)
	require.NoError(t, err)
	assert.Len(t, parts, 3)
	for i, part := range parts {
		if i == 2 {
			assert.Equal(t, 1, part.ItemsCount())
		} else {
			assert.Equal(t, 3, part.ItemsCount())
		}
		part.(*PayloadRequest).Release()
	}
	malformed, err := plogpayload.NewFromProto(reg, []byte{0xff})
	require.NoError(t, err)
	defer malformed.Release()
	_, err = NewPayloadRequest(malformed)
	require.Error(t, err)
}

func TestNativePersistentQueueRestartAndBatch(t *testing.T) {
	t.Parallel()
	reg, err := plogpayload.NewRegistry()
	require.NoError(t, err)
	ld := plog.NewLogs()
	records := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for range 7 {
		records.AppendEmpty().Body().SetStr("durable")
	}
	wire, err := plogotlp.NewExportRequestFromLogs(ld).MarshalProto()
	require.NoError(t, err)
	p, err := plogpayload.NewFromProto(reg, wire)
	require.NoError(t, err)
	req, err := NewPayloadRequest(p)
	require.NoError(t, err)
	storageID := component.MustNewID("file_storage")
	cfg := newTestConfig()
	cfg.NumConsumers = 1
	cfg.WaitForResult = false
	cfg.StorageID = &storageID
	cfg.Batch = configoptional.Optional[BatchConfig]{}
	set := AllSettings[request.Request]{
		Settings: NewPayloadQueueBatchSettings(), Signal: pipeline.SignalLogs,
		ID: component.MustNewID("test"), Telemetry: componenttest.NewNopTelemetrySettings(),
	}
	host := hosttest.NewHost(map[component.ID]component.Component{storageID: storagetest.NewMockStorageExtension(nil)})
	started, release := make(chan struct{}), make(chan struct{})
	first, err := NewQueueBatch(set, cfg, func(context.Context, request.Request) error {
		close(started)
		<-release
		return experr.NewShutdownErr(errors.New("export interrupted"))
	})
	require.NoError(t, err)
	require.NoError(t, first.Start(t.Context(), host))
	ctx := client.NewContext(t.Context(), client.Info{Metadata: client.NewMetadata(map[string][]string{"tenant": {"preserved"}})})
	require.NoError(t, first.Send(ctx, req))
	p.Release()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("persistent queue did not consume the request")
	}
	close(release)
	require.NoError(t, first.Shutdown(t.Context()))
	cfg.Batch = configoptional.Some(BatchConfig{Sizer: request.SizerTypeItems, MinSize: 3, MaxSize: 3, FlushTimeout: time.Millisecond})
	var delivered atomic.Int64
	restarted, err := NewQueueBatch(set, cfg, func(ctx context.Context, req request.Request) error {
		assert.Equal(t, []string{"preserved"}, client.FromContext(ctx).Metadata.Get("tenant"))
		assert.LessOrEqual(t, req.ItemsCount(), 3)
		view, viewErr := req.(*PayloadRequest).Data.View()
		if viewErr != nil {
			return viewErr
		}
		var buf bytes.Buffer
		if writeErr := payload.WriteDebug(&buf, view); writeErr != nil {
			return writeErr
		}
		assert.Contains(t, buf.String(), "durable")
		delivered.Add(int64(req.ItemsCount()))
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, restarted.Start(t.Context(), host))
	require.Eventually(t, func() bool { return delivered.Load() == 7 }, 5*time.Second, time.Millisecond)
	require.NoError(t, restarted.Shutdown(t.Context()))
}
