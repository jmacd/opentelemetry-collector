// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otap

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/fxamacker/cbor/v2"
	arrowpb "github.com/open-telemetry/otel-arrow/go/api/experimental/arrow/v1"
	"github.com/open-telemetry/otel-arrow/go/pkg/record_message"

	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

var nativeValueFields = []arrow.Field{
	{Name: "type", Type: arrow.PrimitiveTypes.Uint8, Nullable: true},
	{Name: "str", Type: arrow.BinaryTypes.String, Nullable: true},
	{Name: "int", Type: arrow.PrimitiveTypes.Int64, Nullable: true},
	{Name: "double", Type: arrow.PrimitiveTypes.Float64, Nullable: true},
	{Name: "bool", Type: arrow.FixedWidthTypes.Boolean, Nullable: true},
	{Name: "bytes", Type: arrow.BinaryTypes.Binary, Nullable: true},
	{Name: "ser", Type: arrow.BinaryTypes.Binary, Nullable: true},
}

func nativeMainSchema() *arrow.Schema {
	resource := arrow.StructOf(
		arrow.Field{Name: "id", Type: arrow.PrimitiveTypes.Uint16, Nullable: true},
		arrow.Field{Name: "schema_url", Type: arrow.BinaryTypes.String, Nullable: true},
		arrow.Field{Name: "dropped_attributes_count", Type: arrow.PrimitiveTypes.Uint32, Nullable: true},
	)
	scope := arrow.StructOf(
		arrow.Field{Name: "id", Type: arrow.PrimitiveTypes.Uint16, Nullable: true},
		arrow.Field{Name: "name", Type: arrow.BinaryTypes.String, Nullable: true},
		arrow.Field{Name: "version", Type: arrow.BinaryTypes.String, Nullable: true},
		arrow.Field{Name: "dropped_attributes_count", Type: arrow.PrimitiveTypes.Uint32, Nullable: true},
	)
	return arrow.NewSchema([]arrow.Field{
		{Name: "id", Type: arrow.PrimitiveTypes.Uint16, Nullable: true},
		{Name: "resource", Type: resource, Nullable: true},
		{Name: "scope", Type: scope, Nullable: true},
		{Name: "schema_url", Type: arrow.BinaryTypes.String, Nullable: true},
		{Name: "time_unix_nano", Type: arrow.FixedWidthTypes.Timestamp_ns, Nullable: true},
		{Name: "observed_time_unix_nano", Type: arrow.FixedWidthTypes.Timestamp_ns, Nullable: true},
		{Name: "trace_id", Type: &arrow.FixedSizeBinaryType{ByteWidth: 16}, Nullable: true},
		{Name: "span_id", Type: &arrow.FixedSizeBinaryType{ByteWidth: 8}, Nullable: true},
		{Name: "severity_number", Type: arrow.PrimitiveTypes.Int32, Nullable: true},
		{Name: "severity_text", Type: arrow.BinaryTypes.String, Nullable: true},
		{Name: "body", Type: arrow.StructOf(nativeValueFields...), Nullable: true},
		{Name: "dropped_attributes_count", Type: arrow.PrimitiveTypes.Uint32, Nullable: true},
		{Name: "flags", Type: arrow.PrimitiveTypes.Uint32, Nullable: true},
		{Name: "event_name", Type: arrow.BinaryTypes.String, Nullable: true},
	}, nil)
}

type nativeAttrsBuilder struct {
	builder    *array.RecordBuilder
	lastKey    string
	lastValue  any
	lastParent uint16
}

func (b *nativeAttrsBuilder) append(parent uint16, attrs payload.Attributes) (bool, error) {
	found := false
	err := attrs(func(key string, value payload.Value) error {
		raw, err := value.Read()
		if err != nil {
			return err
		}
		id := parent
		composite := false
		switch raw.(type) {
		case map[string]any, []any, nil:
			composite = true
		}
		equal := reflect.DeepEqual(raw, b.lastValue)
		if value, ok := raw.([]byte); ok {
			if previous, ok := b.lastValue.([]byte); ok {
				equal = bytes.Equal(value, previous)
			}
		}
		if !composite && b.lastKey == key && b.lastValue != nil && equal {
			id -= b.lastParent
		}
		b.builder.Field(0).(*array.Uint16Builder).Append(id)
		b.builder.Field(1).(*array.StringBuilder).Append(key)
		if err := appendArrowValue(b.builder.Fields()[2:], raw); err != nil {
			return err
		}
		b.lastKey, b.lastValue, b.lastParent = key, raw, parent
		found = true
		return nil
	})
	return found, err
}

func appendArrowValue(fields []array.Builder, raw any) error {
	kind, active := uint8(0), -1
	var serialized []byte
	switch raw.(type) {
	case nil:
	case string:
		kind, active = 1, 1
	case int64:
		kind, active = 2, 2
	case float64:
		kind, active = 3, 3
	case bool:
		kind, active = 4, 4
	case []byte:
		kind, active = 7, 5
	case map[string]any:
		kind, active = 5, 6
	case []any:
		kind, active = 6, 6
	default:
		return fmt.Errorf("unsupported native Arrow value %T", raw)
	}
	if active == 6 {
		var err error
		serialized, err = cbor.Marshal(raw)
		if err != nil {
			return err
		}
	}
	fields[0].(*array.Uint8Builder).Append(kind)
	for i := 1; i < len(fields); i++ {
		if i != active {
			fields[i].AppendNull()
			continue
		}
		switch i {
		case 1:
			fields[i].(*array.StringBuilder).Append(raw.(string))
		case 2:
			fields[i].(*array.Int64Builder).Append(raw.(int64))
		case 3:
			fields[i].(*array.Float64Builder).Append(raw.(float64))
		case 4:
			fields[i].(*array.BooleanBuilder).Append(raw.(bool))
		case 5:
			fields[i].(*array.BinaryBuilder).Append(raw.([]byte))
		case 6:
			fields[i].(*array.BinaryBuilder).Append(serialized)
		}
	}
	return nil
}

type nativeLogsBuilder struct {
	main                             *array.RecordBuilder
	attrs                            [3]nativeAttrsBuilder
	rows, resource, scope            int
	lastResource, lastScope, lastLog uint16
}

func newNativeLogsBuilder() *nativeLogsBuilder {
	b := &nativeLogsBuilder{main: array.NewRecordBuilder(memory.NewGoAllocator(), nativeMainSchema()), resource: -1, scope: -1}
	fields := append([]arrow.Field{
		{Name: "parent_id", Type: arrow.PrimitiveTypes.Uint16, Nullable: true},
		{Name: "key", Type: arrow.BinaryTypes.String, Nullable: true},
	}, nativeValueFields...)
	for i := range b.attrs {
		b.attrs[i].builder = array.NewRecordBuilder(memory.NewGoAllocator(), arrow.NewSchema(fields, nil))
	}
	return b
}

func (b *nativeLogsBuilder) release() {
	b.main.Release()
	for i := range b.attrs {
		b.attrs[i].builder.Release()
	}
}

func (b *nativeLogsBuilder) finish() []*record_message.RecordMessage {
	out := []*record_message.RecordMessage{record_message.NewLogsMessage("native-logs-v1", b.main.NewRecordBatch())}
	for i, typ := range []arrowpb.ArrowPayloadType{arrowpb.ArrowPayloadType_RESOURCE_ATTRS, arrowpb.ArrowPayloadType_SCOPE_ATTRS, arrowpb.ArrowPayloadType_LOG_ATTRS} {
		if b.attrs[i].builder.Field(0).Len() > 0 {
			out = append(out, record_message.NewRelatedDataMessage(fmt.Sprintf("native-attrs-%d-v1", typ), b.attrs[i].builder.NewRecordBatch(), typ))
		}
	}
	return out
}

func (b *nativeLogsBuilder) append(r payload.ResourceLogs, s payload.ScopeLogs, l payload.LogRecord, newResource, newScope bool) error {
	if newResource {
		b.resource++
		if _, err := b.attrs[0].append(uint16(b.resource), r.Attributes); err != nil {
			return err
		}
	}
	if newScope {
		b.scope++
		if _, err := b.attrs[1].append(uint16(b.scope), s.Attributes); err != nil {
			return err
		}
	}
	hasAttrs, err := b.attrs[2].append(uint16(b.rows), l.Attributes)
	if err != nil {
		return err
	}
	if hasAttrs {
		b.main.Field(0).(*array.Uint16Builder).Append(uint16(b.rows) - b.lastLog)
		b.lastLog = uint16(b.rows)
	} else {
		b.main.Field(0).AppendNull()
	}
	res := b.main.Field(1).(*array.StructBuilder)
	res.Append(true)
	res.FieldBuilder(0).(*array.Uint16Builder).Append(uint16(b.resource) - b.lastResource)
	res.FieldBuilder(1).(*array.StringBuilder).Append(r.SchemaURL)
	res.FieldBuilder(2).(*array.Uint32Builder).Append(r.DroppedAttributes)
	b.lastResource = uint16(b.resource)
	scope := b.main.Field(2).(*array.StructBuilder)
	scope.Append(true)
	scope.FieldBuilder(0).(*array.Uint16Builder).Append(uint16(b.scope) - b.lastScope)
	scope.FieldBuilder(1).(*array.StringBuilder).Append(s.Name)
	scope.FieldBuilder(2).(*array.StringBuilder).Append(s.Version)
	scope.FieldBuilder(3).(*array.Uint32Builder).Append(s.DroppedAttributes)
	b.lastScope = uint16(b.scope)
	b.main.Field(3).(*array.StringBuilder).Append(s.SchemaURL)
	b.main.Field(4).(*array.TimestampBuilder).Append(arrow.Timestamp(l.Timestamp))
	b.main.Field(5).(*array.TimestampBuilder).Append(arrow.Timestamp(l.ObservedTimestamp))
	for i, id := range [][]byte{l.TraceID, l.SpanID} {
		column := b.main.Field(6 + i).(*array.FixedSizeBinaryBuilder)
		switch {
		case len(id) == 0:
			column.AppendNull()
		case len(id) != column.Type().(*arrow.FixedSizeBinaryType).ByteWidth:
			return errors.New("incorrect native log identifier width")
		default:
			column.Append(id)
		}
	}
	b.main.Field(8).(*array.Int32Builder).Append(l.SeverityNumber)
	b.main.Field(9).(*array.StringBuilder).Append(l.SeverityText)
	body, err := l.Body.Read()
	if err != nil {
		return err
	}
	value := b.main.Field(10).(*array.StructBuilder)
	value.Append(true)
	columns := make([]array.Builder, len(nativeValueFields))
	for i := range columns {
		columns[i] = value.FieldBuilder(i)
	}
	if err := appendArrowValue(columns, body); err != nil {
		return err
	}
	b.main.Field(11).(*array.Uint32Builder).Append(l.DroppedAttributes)
	b.main.Field(12).(*array.Uint32Builder).Append(l.Flags)
	b.main.Field(13).(*array.StringBuilder).Append(l.EventName)
	b.rows++
	return nil
}

// Coalesce independent OTAP schemas/dictionaries into canonical Arrow columns,
// rebasing main and related IDs. No pdata objects or intermediate OTLP bytes are
// constructed. Each resulting group respects OTAP's uint16 identifier limit.
func coalesceRecords(inputs []payload.Representation) (payload.Representation, error) {
	out := &Records{}
	b := newNativeLogsBuilder()
	defer func() { b.release() }()
	for _, input := range inputs {
		records, ok := input.(*Records)
		if !ok {
			out.Release()
			return nil, fmt.Errorf("expected Arrow records, got %T", input)
		}
		for _, group := range records.batches {
			for _, record := range group {
				allowed := b.attrs[0].builder.Schema().Fields()
				if record.PayloadType() == arrowpb.ArrowPayloadType_LOGS {
					allowed = b.main.Schema().Fields()
				}
				if err := checkCoalescingFields(record.Record().Schema().Fields(), allowed); err != nil {
					out.Release()
					return nil, err
				}
			}
		}
		view, err := arrowView(input)
		if err != nil {
			out.Release()
			return nil, err
		}
		err = view.Resources(func(r payload.ResourceLogs) error {
			newResource := true
			return r.Scopes(func(s payload.ScopeLogs) error {
				newScope := true
				return s.Records(func(l payload.LogRecord) error {
					if b.rows == math.MaxUint16 {
						out.batches = append(out.batches, b.finish())
						b.release()
						b = newNativeLogsBuilder()
						newResource, newScope = true, true
					}
					if appendErr := b.append(r, s, l, newResource, newScope); appendErr != nil {
						return appendErr
					}
					out.items++
					newResource, newScope = false, false
					return nil
				})
			})
		})
		if err != nil {
			out.Release()
			return nil, err
		}
	}
	if b.rows > 0 {
		out.batches = append(out.batches, b.finish())
	}
	return out, nil
}

func checkCoalescingFields(fields, allowed []arrow.Field) error {
	for _, field := range fields {
		var match *arrow.Field
		for i := range allowed {
			if field.Name == allowed[i].Name {
				match = &allowed[i]
				break
			}
		}
		if match == nil {
			return fmt.Errorf("cannot preserve unknown Arrow column %q while coalescing", field.Name)
		}
		if nested, ok := field.Type.(*arrow.StructType); ok {
			expected, ok := match.Type.(*arrow.StructType)
			if !ok {
				return fmt.Errorf("unexpected Arrow struct column %q", field.Name)
			}
			if err := checkCoalescingFields(nested.Fields(), expected.Fields()); err != nil {
				return err
			}
		}
	}
	return nil
}
