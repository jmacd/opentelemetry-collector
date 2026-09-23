// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otap

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"testing"

	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/open-telemetry/otel-arrow/go/pkg/config"
	"github.com/open-telemetry/otel-arrow/go/pkg/record_message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

func testRegistry(tb testing.TB) (*payload.Registry, *ArrowCodec) {
	tb.Helper()
	alloc := memory.NewCheckedAllocator(memory.NewGoAllocator())
	arrow := NewArrowCodec(config.WithAllocator(alloc))
	reg, err := payload.NewRegistry(ObjectsCodec(), ProtoCodec(), arrow.Codec())
	require.NoError(tb, err)
	tb.Cleanup(func() {
		require.NoError(tb, arrow.Close())
		alloc.AssertSize(tb, 0)
	})
	return reg, arrow
}

func fixture(n int) plog.Logs {
	ld := plog.NewLogs()
	for i := 0; i < n; {
		rl := ld.ResourceLogs().AppendEmpty()
		rl.SetSchemaUrl("https://example.com/resource")
		rl.Resource().Attributes().PutStr("service.name", fmt.Sprintf("service-%d", i))
		rl.Resource().SetDroppedAttributesCount(1)
		sl := rl.ScopeLogs().AppendEmpty()
		sl.SetSchemaUrl("https://example.com/scope")
		sl.Scope().SetName("prototype")
		sl.Scope().SetVersion("1")
		sl.Scope().Attributes().PutBool("enabled", true)
		sl.Scope().SetDroppedAttributesCount(2)
		end := min(i+max(1, n/2), n)
		for ; i < end; i++ {
			lr := sl.LogRecords().AppendEmpty()
			lr.SetTimestamp(pcommon.Timestamp(1_000_000 + i))
			lr.SetObservedTimestamp(pcommon.Timestamp(2_000_000 + i))
			lr.SetSeverityNumber(plog.SeverityNumberInfo)
			lr.SetSeverityText("INFO")
			lr.SetEventName("test.event")
			lr.SetTraceID(pcommon.TraceID{1, 2, 3})
			lr.SetSpanID(pcommon.SpanID{4, 5, 6})
			lr.SetFlags(plog.LogRecordFlags(1))
			lr.SetDroppedAttributesCount(3)
			lr.Attributes().PutInt("event.index", int64(i))
			lr.Attributes().PutStr("key", fmt.Sprintf("value-%d", i%8))
			lr.Attributes().PutEmptySlice("array").AppendEmpty().SetInt(7)
			switch i % 4 {
			case 0:
				lr.Body().SetStr("representative log body with some repetitive text")
			case 1:
				lr.Body().SetEmptyMap().PutStr("message", "structured body")
			case 2:
				lr.Body().SetEmptyBytes().FromRaw([]byte{0, 1, 2, 255})
			case 3:
				lr.Body().SetInt(42)
			}
		}
	}
	return ld
}

// Ignore representation-dependent ordering, but compare every populated field,
// including resource/scope associations and nested attribute/body values.
func normalized(ld plog.Logs) []map[string]any {
	out := make([]map[string]any, 0, ld.LogRecordCount())
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			for k := 0; k < sl.LogRecords().Len(); k++ {
				lr := sl.LogRecords().At(k)
				out = append(out, map[string]any{
					"resource": rl.Resource().Attributes().AsRaw(), "resource_schema": rl.SchemaUrl(),
					"resource_dropped": rl.Resource().DroppedAttributesCount(),
					"scope":            sl.Scope().Attributes().AsRaw(), "scope_schema": sl.SchemaUrl(),
					"scope_name": sl.Scope().Name(), "scope_version": sl.Scope().Version(),
					"scope_dropped": sl.Scope().DroppedAttributesCount(),
					"attributes":    lr.Attributes().AsRaw(), "body": lr.Body().AsRaw(),
					"time": lr.Timestamp(), "observed": lr.ObservedTimestamp(),
					"severity": lr.SeverityNumber(), "severity_text": lr.SeverityText(),
					"event": lr.EventName(), "trace": lr.TraceID(), "span": lr.SpanID(),
					"flags": lr.Flags(), "dropped": lr.DroppedAttributesCount(),
				})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a := out[i]["attributes"].(map[string]any)["event.index"].(int64)
		b := out[j]["attributes"].(map[string]any)["event.index"].(int64)
		return a < b
	})
	return out
}

func TestConversions(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, 1, 16, 128} {
		for _, format := range []payload.Format{ObjectsFormat, ProtoFormat, ArrowFormat} {
			t.Run(fmt.Sprintf("%s/%d", format, n), func(t *testing.T) {
				t.Parallel()
				reg, arrow := testRegistry(t)
				want := fixture(n)
				var p *payload.Payload
				var err error
				switch format {
				case ObjectsFormat:
					p, err = NewFromLogs(reg, want)
				case ProtoFormat:
					buf, marshalErr := plogotlp.NewExportRequestFromLogs(want).MarshalProto()
					require.NoError(t, marshalErr)
					p, err = NewFromProto(reg, buf)
				case ArrowFormat:
					rep, encodeErr := arrow.Codec().Encode(newObjects(want))
					require.NoError(t, encodeErr)
					p, err = reg.New(rep)
				}
				require.NoError(t, err)
				defer p.Release()
				require.Equal(t, n, mustCount(t, p))
				got, err := ReadOnlyLogs(p)
				require.NoError(t, err)
				assert.True(t, got.IsReadOnly())
				assert.Equal(t, normalized(want), normalized(got))
				buf, err := ProtoBytes(p)
				require.NoError(t, err)
				req := plogotlp.NewExportRequest()
				require.NoError(t, req.UnmarshalProto(buf))
				assert.Equal(t, normalized(want), normalized(req.Logs()))
				rep, err := p.As(ArrowFormat)
				require.NoError(t, err)
				decoded, err := decodeRecords(rep)
				require.NoError(t, err)
				defer decoded.Release()
				assert.Equal(t, normalized(want), normalized(decoded.(*Objects).Logs()))
			})
		}
	}
}

func TestNativeMerge(t *testing.T) {
	t.Parallel()
	for _, format := range []payload.Format{ProtoFormat, ArrowFormat, ObjectsFormat} {
		t.Run(string(format), func(t *testing.T) {
			t.Parallel()
			reg, arrow := testRegistry(t)
			var inputs []*payload.Payload
			var expected []map[string]any
			for i := range 2 {
				ld := fixture(8)
				// Independent groups deliberately reuse record IDs with different values.
				ld.ResourceLogs().At(0).Resource().Attributes().PutStr("origin", strconv.Itoa(i))
				expected = append(expected, normalized(ld)...)
				src := newObjects(ld)
				var rep payload.Representation = src
				var err error
				switch format {
				case ProtoFormat:
					rep, err = ProtoCodec().Encode(src)
				case ArrowFormat:
					rep, err = arrow.Codec().Encode(src)
				}
				require.NoError(t, err)
				p, err := reg.New(rep)
				require.NoError(t, err)
				inputs = append(inputs, p)
			}
			merged, err := reg.Merge(inputs...)
			require.NoError(t, err)
			defer merged.Release()
			assert.Equal(t, 16, mustCount(t, merged))
			if format == ArrowFormat {
				b, asErr := merged.As(ArrowFormat)
				require.NoError(t, asErr)
				require.Len(t, b.(*Records).Batches(), 1)
				assert.Equal(t, int64(16), b.(*Records).Batches()[0][0].Record().NumRows())
			}
			for _, p := range inputs {
				p.Release()
			}
			// The merged records must also outlive the encoder and its dictionaries.
			require.NoError(t, arrow.Close())
			got, err := ReadOnlyLogs(merged)
			require.NoError(t, err)
			assert.ElementsMatch(t, expected, normalized(got))
		})
	}
}

func TestProtoOwnershipMutationAndPersistence(t *testing.T) {
	t.Parallel()
	reg, _ := testRegistry(t)
	buf, err := plogotlp.NewExportRequestFromLogs(fixture(8)).MarshalProto()
	require.NoError(t, err)
	buf = protowire.AppendTag(buf, 100, protowire.BytesType)
	buf = protowire.AppendString(buf, "unknown-field-preserved")
	original := slices.Clone(buf)
	p, err := NewFromProto(reg, buf)
	require.NoError(t, err)
	defer p.Release()
	mutable, err := MutableLogs(p)
	require.NoError(t, err)
	mutable.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().SetStr("changed")
	updated, err := NewFromLogs(reg, mutable)
	require.NoError(t, err)
	defer updated.Release()
	updatedBytes, err := ProtoBytes(updated)
	require.NoError(t, err)
	assert.NotEqual(t, original, updatedBytes)
	stored, err := PersistentBytes(p)
	require.NoError(t, err)
	assert.Equal(t, original, stored)
	assert.Same(t, &buf[0], &stored[0])
	restored, err := NewFromProto(reg, slices.Clone(stored))
	require.NoError(t, err)
	defer restored.Release()
	assert.Equal(t, mustCount(t, p), mustCount(t, restored))
	merged, err := reg.Merge(p, restored)
	require.NoError(t, err)
	defer merged.Release()
	batched, err := ProtoBytes(merged)
	require.NoError(t, err)
	assert.Equal(t, append(slices.Clone(original), original...), batched)
	var debug bytes.Buffer
	require.NoError(t, WriteDebug(&debug, p))
	assert.Contains(t, debug.String(), "representative log body")
	sentinel := errors.New("writer failed")
	require.ErrorIs(t, WriteDebug(errorWriter{err: sentinel}, p), sentinel)
	require.ErrorIs(t, WriteDebug(errorWriter{}, p), io.ErrShortWrite)
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestMalformedAndUnsupported(t *testing.T) {
	t.Parallel()
	reg, arrow := testRegistry(t)
	for _, buf := range [][]byte{{0xff}, {0x0a, 4}, {8, 1}, {0x0a, 2, 0x10, 1}, {0}} {
		p, err := NewFromProto(reg, buf)
		require.NoError(t, err)
		_, err = p.ItemsCount()
		require.Error(t, err)
		p.Release()
	}
	// Valid request/resource/scope envelopes containing an invalid LogRecord.
	p, err := NewFromProto(reg, []byte{0x0a, 5, 0x12, 3, 0x12, 1, 0xff})
	require.NoError(t, err)
	defer p.Release()
	_, err = p.ItemsCount()
	require.Error(t, err)
	_, err = ReadOnlyLogs(p)
	require.Error(t, err)
	_, err = NewFromRecords(reg, [][]*record_message.RecordMessage{{nil}})
	require.Error(t, err)
	_, err = NewFromRecords(reg, [][]*record_message.RecordMessage{{}})
	require.Error(t, err)
	rep, err := arrow.Codec().Encode(newObjects(fixture(2)))
	require.NoError(t, err)
	ap, err := reg.New(rep)
	require.NoError(t, err)
	defer ap.Release()
	_, err = ProtoBytes(ap)
	require.NoError(t, err)
	_, err = PersistentBytes(ap)
	require.ErrorContains(t, err, "unsupported")
	_, err = reg.Merge(p, ap)
	require.Error(t, err)
	require.NoError(t, arrow.Close())
	_, err = arrow.Codec().Encode(newObjects(fixture(1)))
	require.ErrorContains(t, err, "closed")
}

func mustCount(tb testing.TB, p interface{ ItemsCount() (int, error) }) int {
	count, err := p.ItemsCount()
	if err != nil {
		tb.Fatal(err)
	}
	return count
}

func TestEmptyGroupsAreNotSilentlyDropped(t *testing.T) {
	t.Parallel()
	for _, scope := range []bool{false, true} {
		t.Run(strconv.FormatBool(scope), func(t *testing.T) {
			t.Parallel()
			reg, _ := testRegistry(t)
			ld := plog.NewLogs()
			rl := ld.ResourceLogs().AppendEmpty()
			rl.Resource().Attributes().PutStr("service.name", "empty")
			if scope {
				rl.ScopeLogs().AppendEmpty().Scope().SetName("empty")
			}
			p, err := NewFromLogs(reg, ld)
			require.NoError(t, err)
			defer p.Release()
			_, err = ProtoBytes(p)
			require.NoError(t, err)
			_, err = p.As(ArrowFormat)
			require.ErrorContains(t, err, "cannot preserve")
			original, err := ReadOnlyLogs(p)
			require.NoError(t, err)
			assert.Equal(t, 1, original.ResourceLogs().Len())
		})
	}
}

func FuzzProtoCount(f *testing.F) {
	buf, err := plogotlp.NewExportRequestFromLogs(fixture(8)).MarshalProto()
	require.NoError(f, err)
	f.Add(buf)
	f.Add([]byte{})
	f.Add([]byte{0x0a, 5, 0x12, 3, 0x12, 1, 0xff})
	f.Fuzz(func(t *testing.T, buf []byte) {
		count, scanErr := countProto(buf)
		req := plogotlp.NewExportRequest()
		if decodeErr := req.UnmarshalProto(buf); scanErr == nil && decodeErr == nil {
			require.Equal(t, req.Logs().LogRecordCount(), count)
		}
	})
}
