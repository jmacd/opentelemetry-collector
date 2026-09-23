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
	"github.com/fxamacker/cbor/v2"
	arrowpb "github.com/open-telemetry/otel-arrow/go/api/experimental/arrow/v1"
	"github.com/open-telemetry/otel-arrow/go/pkg/record_message"

	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

func arrowCell(col arrow.Array, row int) (any, error) {
	if row < 0 || row >= col.Len() {
		return nil, errors.New("arrow row outside column bounds")
	}
	if col.IsNull(row) {
		return nil, nil
	}
	switch a := col.(type) {
	case *array.Dictionary:
		return arrowCell(a.Dictionary(), a.GetValueIndex(row))
	case *array.String:
		return a.Value(row), nil
	case *array.Binary:
		return a.Value(row), nil
	case *array.FixedSizeBinary:
		return a.Value(row), nil
	case *array.Uint8:
		return uint64(a.Value(row)), nil
	case *array.Uint16:
		return uint64(a.Value(row)), nil
	case *array.Uint32:
		return uint64(a.Value(row)), nil
	case *array.Uint64:
		return a.Value(row), nil
	case *array.Int32:
		return int64(a.Value(row)), nil
	case *array.Int64:
		return a.Value(row), nil
	case *array.Timestamp:
		return uint64(a.Value(row)), nil
	case *array.Float64:
		return a.Value(row), nil
	case *array.Boolean:
		return a.Value(row), nil
	default:
		return nil, fmt.Errorf("unsupported arrow scalar type %s", col.DataType())
	}
}

type arrowReader struct {
	columns *arrowColumns
	row     int
	err     error
}

type arrowColumns struct {
	values  map[string]arrow.Array
	structs map[string]*arrowColumns
	parent  *array.Struct
}

func indexColumns(fields []arrow.Field, columns []arrow.Array) *arrowColumns {
	out := &arrowColumns{values: make(map[string]arrow.Array, len(fields)), structs: map[string]*arrowColumns{}}
	for i, field := range fields {
		out.values[field.Name] = columns[i]
		if s, ok := columns[i].(*array.Struct); ok {
			nested := s.DataType().(*arrow.StructType).Fields()
			children := make([]arrow.Array, len(nested))
			for j := range children {
				children[j] = s.Field(j)
			}
			child := indexColumns(nested, children)
			child.parent = s
			out.structs[field.Name] = child
		}
	}
	return out
}

func (r *arrowReader) value(path ...string) any {
	if r.err != nil {
		return nil
	}
	columns := r.columns
	for i, name := range path {
		if columns == nil || (columns.parent != nil && columns.parent.IsNull(r.row)) {
			return nil
		}
		col := columns.values[name]
		if col == nil {
			return nil
		}
		if col.IsNull(r.row) {
			return nil
		}
		if i == len(path)-1 {
			value, err := arrowCell(col, r.row)
			r.err = err
			return value
		}
		child := columns.structs[name]
		if child == nil {
			r.err = fmt.Errorf("arrow field %s must be a struct", name)
			return nil
		}
		columns = child
	}
	return nil
}

func (r *arrowReader) number(path ...string) uint64 {
	switch v := r.value(path...).(type) {
	case nil:
		return 0
	case uint64:
		return v
	case int64:
		return uint64(v)
	default:
		r.err = fmt.Errorf("arrow field %v must be an integer, got %T", path, v)
		return 0
	}
}

func (r *arrowReader) text(path ...string) string {
	v := r.value(path...)
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		r.err = fmt.Errorf("arrow field %v must be a string", path)
	}
	return s
}

func (r *arrowReader) binary(path ...string) []byte {
	v := r.value(path...)
	if v == nil {
		return nil
	}
	b, ok := v.([]byte)
	if !ok {
		r.err = fmt.Errorf("arrow field %v must be bytes", path)
	}
	return b
}

func arrowValue(columns *arrowColumns, row int, prefix ...string) payload.Value {
	for _, name := range prefix {
		if columns != nil {
			columns = columns.structs[name]
		}
	}
	return payload.ValueFunc(func() (any, error) {
		r := arrowReader{columns: columns, row: row}
		kind := r.number("type")
		var out any
		switch kind {
		case 0:
		case 1:
			out = r.text("str")
		case 2:
			out = int64(r.number("int"))
		case 3:
			out = float64(0)
			if v := r.value("double"); v != nil {
				var ok bool
				out, ok = v.(float64)
				if !ok {
					r.err = errors.New("arrow double value has incorrect type")
				}
			}
		case 4:
			out = false
			if v := r.value("bool"); v != nil {
				var ok bool
				out, ok = v.(bool)
				if !ok {
					r.err = errors.New("arrow boolean value has incorrect type")
				}
			}
		case 5, 6:
			buf := r.binary("ser")
			if r.err != nil {
				return nil, r.err
			}
			if err := cbor.Unmarshal(buf, &out); err != nil {
				return nil, err
			}
			var err error
			out, err = normalizeCBOR(out)
			if err != nil {
				return nil, err
			}
		case 7:
			out = r.binary("bytes")
		default:
			return nil, fmt.Errorf("unknown arrow value type %d", kind)
		}
		return out, r.err
	})
}

func normalizeCBOR(v any) (any, error) {
	switch v := v.(type) {
	case uint64:
		if v > math.MaxInt64 {
			return nil, errors.New("CBOR integer exceeds int64")
		}
		return int64(v), nil
	case map[any]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			s, ok := key.(string)
			if !ok {
				return nil, errors.New("CBOR map key must be a string")
			}
			item, err := normalizeCBOR(value)
			if err != nil {
				return nil, err
			}
			out[s] = item
		}
		return out, nil
	case []any:
		for i, value := range v {
			item, err := normalizeCBOR(value)
			if err != nil {
				return nil, err
			}
			v[i] = item
		}
		return v, nil
	default:
		return v, nil
	}
}

type attributeRow struct {
	key   string
	value payload.Value
}
type attributeIndex map[uint16][]attributeRow

func (idx attributeIndex) attributes(id uint16) payload.Attributes {
	return func(yield func(string, payload.Value) error) error {
		for _, a := range idx[id] {
			if err := yield(a.key, a.value); err != nil {
				return err
			}
		}
		return nil
	}
}

func indexAttributes(rec arrow.RecordBatch) (attributeIndex, error) {
	idx := attributeIndex{}
	columns := indexColumns(rec.Schema().Fields(), rec.Columns())
	var prevKey string
	var prevValue any
	var prevKind, prevID uint64
	for i := 0; i < int(rec.NumRows()); i++ {
		r := arrowReader{columns: columns, row: i}
		key, kind, id := r.text("key"), r.number("type"), r.number("parent_id")
		if r.err != nil {
			return nil, r.err
		}
		value := arrowValue(columns, i)
		var scalar any
		// Complex and empty values never use parent delta encoding in OTAP.
		if kind != 0 && kind != 5 && kind != 6 {
			var err error
			scalar, err = value.Read()
			if err != nil {
				return nil, err
			}
			equal := reflect.DeepEqual(scalar, prevValue)
			if b, ok := scalar.([]byte); ok {
				prev, _ := prevValue.([]byte)
				equal = bytes.Equal(b, prev)
			}
			if i > 0 && key == prevKey && kind == prevKind && equal {
				id += prevID
			}
		}
		if id > math.MaxUint16 {
			return nil, errors.New("arrow attribute parent ID overflow")
		}
		idx[uint16(id)] = append(idx[uint16(id)], attributeRow{key: key, value: value})
		prevKey, prevKind, prevValue, prevID = key, kind, scalar, id
	}
	return idx, nil
}

type rowIDs struct {
	resource, scope, log uint16
	hasLog               bool
}

func mainIDs(rec arrow.RecordBatch) ([]rowIDs, error) {
	out := make([]rowIDs, int(rec.NumRows()))
	columns := indexColumns(rec.Schema().Fields(), rec.Columns())
	var resource, scope, log uint64
	for i := range out {
		r := arrowReader{columns: columns, row: i}
		resource += r.number("resource", "id")
		scope += r.number("scope", "id")
		id := r.value("id")
		if id != nil {
			log += r.number("id")
		}
		if r.err != nil {
			return nil, r.err
		}
		if max(resource, scope, log) > math.MaxUint16 {
			return nil, errors.New("arrow main ID overflow")
		}
		out[i] = rowIDs{uint16(resource), uint16(scope), uint16(log), id != nil}
	}
	return out, nil
}

func arrowView(rep payload.Representation) (payload.LogsView, error) {
	src, ok := rep.(*Records)
	if !ok {
		return nil, fmt.Errorf("expected arrow records, got %T", rep)
	}
	return payload.LogsViewFunc(func(yield func(payload.ResourceLogs) error) error {
		for _, batch := range src.batches {
			if err := visitArrowBatch(batch, yield); err != nil {
				return err
			}
		}
		return nil
	}), nil
}

func visitArrowBatch(batch []*record_message.RecordMessage, yield func(payload.ResourceLogs) error) error {
	var main arrow.RecordBatch
	resourceAttrs, scopeAttrs, logAttrs := attributeIndex{}, attributeIndex{}, attributeIndex{}
	for _, rec := range batch {
		if rec.PayloadType() == arrowpb.ArrowPayloadType_LOGS {
			if main != nil {
				return errors.New("multiple main logs records")
			}
			main = rec.Record()
			continue
		}
		idx, err := indexAttributes(rec.Record())
		if err != nil {
			return err
		}
		switch rec.PayloadType() {
		case arrowpb.ArrowPayloadType_RESOURCE_ATTRS:
			resourceAttrs = idx
		case arrowpb.ArrowPayloadType_SCOPE_ATTRS:
			scopeAttrs = idx
		case arrowpb.ArrowPayloadType_LOG_ATTRS:
			logAttrs = idx
		default:
			return errors.New("unexpected related logs record")
		}
	}
	if main == nil {
		return errors.New("missing main logs record")
	}
	ids, err := mainIDs(main)
	if err != nil {
		return err
	}
	columns := indexColumns(main.Schema().Fields(), main.Columns())
	for start := 0; start < len(ids); {
		end := start + 1
		for end < len(ids) && ids[end].resource == ids[start].resource {
			end++
		}
		r := arrowReader{columns: columns, row: start}
		resource := payload.ResourceLogs{
			SchemaURL: r.text("resource", "schema_url"), DroppedAttributes: uint32(r.number("resource", "dropped_attributes_count")),
			Attributes: resourceAttrs.attributes(ids[start].resource),
		}
		if r.err != nil {
			return r.err
		}
		first, last := start, end
		resource.Scopes = func(yieldScope func(payload.ScopeLogs) error) error {
			for s := first; s < last; {
				e := s + 1
				for e < last && ids[e].scope == ids[s].scope {
					e++
				}
				r := arrowReader{columns: columns, row: s}
				scope := payload.ScopeLogs{
					Name: r.text("scope", "name"), Version: r.text("scope", "version"),
					SchemaURL: r.text("schema_url"), DroppedAttributes: uint32(r.number("scope", "dropped_attributes_count")),
					Attributes: scopeAttrs.attributes(ids[s].scope),
				}
				if r.err != nil {
					return r.err
				}
				lo, hi := s, e
				scope.Records = func(yieldLog func(payload.LogRecord) error) error {
					for i := lo; i < hi; i++ {
						r := arrowReader{columns: columns, row: i}
						attrs := attributeIndex(nil).attributes(0)
						if ids[i].hasLog {
							attrs = logAttrs.attributes(ids[i].log)
						}
						log := payload.LogRecord{
							Timestamp: r.number("time_unix_nano"), ObservedTimestamp: r.number("observed_time_unix_nano"),
							SeverityNumber: int32(r.number("severity_number")), SeverityText: r.text("severity_text"),
							EventName: r.text("event_name"), Flags: uint32(r.number("flags")),
							DroppedAttributes: uint32(r.number("dropped_attributes_count")),
							TraceID:           r.binary("trace_id"), SpanID: r.binary("span_id"),
							Body: arrowValue(columns, i, "body"), Attributes: attrs,
						}
						if r.err != nil {
							return r.err
						}
						if err := yieldLog(log); err != nil {
							return err
						}
					}
					return nil
				}
				if err := yieldScope(scope); err != nil {
					return err
				}
				s = e
			}
			return nil
		}
		if err := yield(resource); err != nil {
			return err
		}
		start = end
	}
	return nil
}
