// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otap

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

func TestNativeViewsRoutingAndSplit(t *testing.T) {
	t.Parallel()
	for _, format := range []payload.Format{ProtoFormat, ArrowFormat, ObjectsFormat} {
		t.Run(string(format), func(t *testing.T) {
			t.Parallel()
			_, encoder := testRegistry(t)
			source := fixture(16)
			var rep payload.Representation = newObjects(source)
			var err error
			switch format {
			case ProtoFormat:
				rep, err = ProtoCodec().Encode(rep)
			case ArrowFormat:
				rep, err = encoder.Codec().Encode(rep)
			}
			require.NoError(t, err)
			protoCodec, arrowCodec := ProtoCodec(), encoder.Codec()
			forbidden := func(payload.Representation) (payload.Representation, error) {
				t.Error("native operation attempted object conversion")
				return nil, errors.New("object conversion forbidden")
			}
			protoCodec.Decode, arrowCodec.Decode = forbidden, forbidden
			reg, err := payload.NewRegistry(ObjectsCodec(), protoCodec, arrowCodec)
			require.NoError(t, err)
			p, err := reg.New(rep)
			require.NoError(t, err)
			defer p.Release()
			var debug bytes.Buffer
			require.NoError(t, WriteDebug(&debug, p))
			assert.Contains(t, debug.String(), "structured body")
			assert.Contains(t, debug.String(), "prototype")
			view, err := p.View()
			require.NoError(t, err)
			wire, err := MarshalView(view)
			require.NoError(t, err)
			req := plogotlp.NewExportRequest()
			require.NoError(t, req.UnmarshalProto(wire))
			assert.Equal(t, normalized(source), normalized(req.Logs()))

			yes, no, err := p.Partition(func(r payload.ResourceLogs, _ payload.ScopeLogs, l payload.LogRecord) (bool, error) {
				name, found, attrErr := r.Attributes.Lookup("service.name")
				require.NoError(t, attrErr)
				require.True(t, found)
				assert.NotEmpty(t, name)
				index, found, attrErr := l.Attributes.Lookup("event.index")
				require.NoError(t, attrErr)
				require.True(t, found)
				return index.(int64)%2 == 0, nil
			})
			require.NoError(t, err)
			require.Equal(t, 8, mustCount(t, yes))
			require.Equal(t, 8, mustCount(t, no))
			defer yes.Release()
			defer no.Release()
			for _, selected := range []*payload.Payload{yes, no} {
				selectedView, selectedErr := selected.View()
				require.NoError(t, selectedErr)
				buf, selectedErr := MarshalView(selectedView)
				require.NoError(t, selectedErr)
				decoded := plogotlp.NewExportRequest()
				require.NoError(t, decoded.UnmarshalProto(buf))
				require.Equal(t, 8, decoded.Logs().LogRecordCount())
			}
			maxSingle := 0
			for i := range 16 {
				one, sliceErr := p.Slice(i, i+1)
				require.NoError(t, sliceErr)
				size, sliceErr := one.BytesSize()
				require.NoError(t, sliceErr)
				maxSingle = max(maxSingle, size)
				one.Release()
			}
			for _, tc := range []struct {
				sizer payload.Sizer
				limit int
			}{{payload.SizerItems, 3}, {payload.SizerBytes, maxSingle + 10}} {
				parts, batchErr := p.MergeSplit(tc.limit, tc.sizer, p)
				require.NoError(t, batchErr)
				count := 0
				var actual []map[string]any
				for _, part := range parts {
					count += mustCount(t, part)
					size := mustCount(t, part)
					if tc.sizer == payload.SizerBytes {
						var sizeErr error
						size, sizeErr = part.BytesSize()
						require.NoError(t, sizeErr)
					}
					require.LessOrEqual(t, size, tc.limit)
					partView, viewErr := part.View()
					require.NoError(t, viewErr)
					buf, viewErr := MarshalView(partView)
					require.NoError(t, viewErr)
					decoded := plogotlp.NewExportRequest()
					require.NoError(t, decoded.UnmarshalProto(buf))
					actual = append(actual, normalized(decoded.Logs())...)
					if format == ArrowFormat {
						native, nativeErr := part.As(ArrowFormat)
						require.NoError(t, nativeErr)
						objects, nativeErr := decodeRecords(native)
						require.NoError(t, nativeErr)
						assert.Equal(t, normalized(decoded.Logs()), normalized(objects.(*Objects).Logs()))
					}
					part.Release()
				}
				assert.Equal(t, 32, count)
				assert.ElementsMatch(t, append(normalized(source), normalized(source)...), actual)
			}
			parts, err := p.MergeSplit(1, payload.SizerBytes, nil)
			require.Error(t, err)
			assert.Nil(t, parts)
			assert.Equal(t, 16, mustCount(t, p))
		})
	}
}

func TestLazyCountErrorAndEmptyGroups(t *testing.T) {
	t.Parallel()
	reg, _ := testRegistry(t)
	p, err := NewFromProto(reg, []byte{0xff})
	require.NoError(t, err)
	defer p.Release()
	_, err = p.ItemsCount()
	require.Error(t, err)
	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().Scope().SetName("empty-scope")
	buf, err := plogotlp.NewExportRequestFromLogs(ld).MarshalProto()
	require.NoError(t, err)
	empty, err := NewFromProto(reg, buf)
	require.NoError(t, err)
	defer empty.Release()
	var out bytes.Buffer
	require.NoError(t, WriteDebug(&out, empty))
	assert.Contains(t, out.String(), "empty-scope")
}
