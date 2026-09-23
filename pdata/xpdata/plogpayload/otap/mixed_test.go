// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otap

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

func TestMixedFormatsMergeWithoutObjects(t *testing.T) {
	t.Parallel()
	_, encoder := testRegistry(t)
	input := newObjects(fixture(8))
	defer input.Release()
	proto, err := ProtoCodec().Encode(input)
	require.NoError(t, err)
	arrow, err := encoder.Codec().Encode(input)
	require.NoError(t, err)
	protoCodec, arrowCodec := ProtoCodec(), encoder.Codec()
	forbidden := func(payload.Representation) (payload.Representation, error) {
		t.Error("mixed-format batching decoded objects")
		return nil, errors.New("objects forbidden")
	}
	protoCodec.Decode, arrowCodec.Decode = forbidden, forbidden
	reg, err := payload.NewRegistry(ObjectsCodec(), protoCodec, arrowCodec)
	require.NoError(t, err)
	a, err := reg.New(proto)
	require.NoError(t, err)
	defer a.Release()
	b, err := reg.New(arrow)
	require.NoError(t, err)
	defer b.Release()
	for _, order := range [][]*payload.Payload{{a, b}, {b, a}} {
		parts, err := order[0].MergeSplit(3, payload.SizerItems, order[1])
		require.NoError(t, err)
		count := 0
		for _, part := range parts {
			assert.Equal(t, ProtoFormat, part.Format())
			wire, err := ProtoBytes(part)
			require.NoError(t, err)
			decoded := plogotlp.NewExportRequest()
			require.NoError(t, decoded.UnmarshalProto(wire))
			require.LessOrEqual(t, decoded.Logs().LogRecordCount(), 3)
			count += decoded.Logs().LogRecordCount()
			part.Release()
		}
		assert.Equal(t, 16, count)
	}
}
