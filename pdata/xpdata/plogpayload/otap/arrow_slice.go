// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otap

import (
	"errors"
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/bitutil"
	"github.com/apache/arrow-go/v18/arrow/memory"
	arrowpb "github.com/open-telemetry/otel-arrow/go/api/experimental/arrow/v1"
	"github.com/open-telemetry/otel-arrow/go/pkg/record_message"

	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

func sliceRecords(rep payload.Representation, start, end int) (payload.Representation, error) {
	src, ok := rep.(*Records)
	if !ok {
		return nil, fmt.Errorf("expected arrow records, got %T", rep)
	}
	out := &Records{items: end - start}
	index := 0
	for _, batch := range src.batches {
		var main *record_message.RecordMessage
		for _, rec := range batch {
			if rec.PayloadType() == arrowpb.ArrowPayloadType_LOGS {
				main = rec
			}
		}
		if main == nil {
			out.Release()
			return nil, errors.New("missing main logs record")
		}
		n := int(main.Record().NumRows())
		lo, hi := max(0, start-index), min(n, end-index)
		index += n
		if lo >= hi {
			continue
		}
		sliced, err := sliceMain(main.Record(), lo, hi)
		if err != nil {
			out.Release()
			return nil, err
		}
		group := []*record_message.RecordMessage{record_message.NewLogsMessage(main.SchemaID(), sliced)}
		for _, rec := range batch {
			if rec != main {
				rec.Record().Retain()
				group = append(group, rec)
			}
		}
		out.batches = append(out.batches, group)
	}
	return out, nil
}

func sliceMain(rec arrow.RecordBatch, start, end int) (arrow.RecordBatch, error) {
	ids, err := mainIDs(rec)
	if err != nil {
		return nil, err
	}
	sliced := rec.NewSlice(int64(start), int64(end))
	defer sliced.Release()
	columns := append([]arrow.Array(nil), sliced.Columns()...)
	var owned []arrow.Array
	defer func() {
		for _, col := range owned {
			col.Release()
		}
	}()
	for i, field := range rec.Schema().Fields() {
		var replacement arrow.Array
		switch field.Name {
		case "id":
			replacement, err = rebaseIDs(columns[i], func(row int) uint16 { return ids[start+row].log })
		case "resource", "scope":
			s, ok := columns[i].(*array.Struct)
			if !ok {
				return nil, errors.New("resource/scope must be a struct")
			}
			replacement, err = rebaseStruct(s, func(row int) uint16 {
				if field.Name == "resource" {
					return ids[start+row].resource
				}
				return ids[start+row].scope
			})
		}
		if err != nil {
			return nil, err
		}
		if replacement != nil {
			owned = append(owned, replacement)
			columns[i] = replacement
		}
	}
	return array.NewRecordBatch(rec.Schema(), columns, int64(end-start)), nil
}

func rebaseIDs(col arrow.Array, absolute func(int) uint16) (arrow.Array, error) {
	src, ok := col.(*array.Uint16)
	if !ok {
		return nil, fmt.Errorf("expected uint16 delta IDs, got %s", col.DataType())
	}
	b := array.NewUint16Builder(memory.NewGoAllocator())
	defer b.Release()
	first := true
	for i := 0; i < src.Len(); i++ {
		switch {
		case src.IsNull(i):
			b.AppendNull()
		case first:
			b.Append(absolute(i))
			first = false
		default:
			b.Append(src.Value(i))
		}
	}
	return b.NewArray(), nil
}

func rebaseStruct(src *array.Struct, absolute func(int) uint16) (arrow.Array, error) {
	fields := src.DataType().(*arrow.StructType).Fields()
	children := make([]arrow.ArrayData, len(fields))
	var owned arrow.Array
	for i, field := range fields {
		col := src.Field(i)
		if field.Name == "id" {
			var err error
			owned, err = rebaseIDs(col, absolute)
			if err != nil {
				return nil, err
			}
			defer owned.Release()
			col = owned
		}
		children[i] = col.Data()
	}
	validity := make([]byte, bitutil.BytesForBits(int64(src.Len())))
	for i := 0; i < src.Len(); i++ {
		if src.IsValid(i) {
			bitutil.SetBit(validity, i)
		}
	}
	bitmap := memory.NewBufferBytes(validity)
	defer bitmap.Release()
	data := array.NewData(src.DataType(), src.Len(), []*memory.Buffer{bitmap}, children, src.NullN(), 0)
	defer data.Release()
	return array.MakeFromData(data), nil
}
