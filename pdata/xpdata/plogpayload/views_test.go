// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package plogpayload

import (
	"bytes"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/entity"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

func TestNativeViewsEntityRefsAndNonfiniteValues(t *testing.T) {
	t.Parallel()
	reg, err := NewRegistry()
	require.NoError(t, err)
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	ref := entity.ResourceEntityRefs(rl.Resource()).AppendEmpty()
	ref.SetType("service")
	ref.SetSchemaUrl("https://example.com/schema")
	ref.IdKeys().Append("service.name")
	ref.DescriptionKeys().Append("service.version")
	log := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	log.Body().SetDouble(math.NaN())
	log.Attributes().PutDouble("infinity", math.Inf(1))
	encoded, err := plogotlp.NewExportRequestFromLogs(ld).MarshalProto()
	require.NoError(t, err)
	p, err := NewFromProto(reg, encoded)
	require.NoError(t, err)
	defer p.Release()
	var out bytes.Buffer
	require.NoError(t, WriteDebug(&out, p))
	assert.Contains(t, out.String(), "service.name")
	assert.Contains(t, out.String(), "NaN")
	assert.Contains(t, out.String(), "Infinity")
	view, err := p.View()
	require.NoError(t, err)
	wire, err := MarshalView(view)
	require.NoError(t, err)
	decoded := plogotlp.NewExportRequest()
	require.NoError(t, decoded.UnmarshalProto(wire))
	refs := entity.ResourceEntityRefs(decoded.Logs().ResourceLogs().At(0).Resource())
	assert.Equal(t, 1, refs.Len())
	assert.Equal(t, "service", refs.At(0).Type())
	assert.Equal(t, []string{"service.name"}, refs.At(0).IdKeys().AsRaw())
}

func TestSplitPreservesEmptyGroupsOnce(t *testing.T) {
	t.Parallel()
	reg, err := NewRegistry()
	require.NoError(t, err)
	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().SetSchemaUrl("before")
	for _, name := range []string{"first", "second"} {
		resource := ld.ResourceLogs().AppendEmpty()
		resource.SetSchemaUrl(name)
		resource.ScopeLogs().AppendEmpty().Scope().SetName("empty")
		resource.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr(name)
	}
	ld.ResourceLogs().AppendEmpty().SetSchemaUrl("after")
	wire, err := plogotlp.NewExportRequestFromLogs(ld).MarshalProto()
	require.NoError(t, err)
	p, err := NewFromProto(reg, wire)
	require.NoError(t, err)
	defer p.Release()
	parts, err := p.MergeSplit(1, payload.SizerItems, nil)
	require.NoError(t, err)
	var resources []string
	for _, part := range parts {
		view, err := part.View()
		require.NoError(t, err)
		require.NoError(t, view.Resources(func(r payload.ResourceLogs) error {
			resources = append(resources, r.SchemaURL)
			return nil
		}))
		part.Release()
	}
	assert.Equal(t, []string{"before", "first", "second", "after"}, resources)
}
