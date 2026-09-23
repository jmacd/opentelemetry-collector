// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package plogpayload

import (
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"

	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

type wireField struct {
	number     protowire.Number
	kind       protowire.Type
	raw, bytes []byte
	bits       uint64
}

func walkWire(buf []byte, yield func(wireField) error) error {
	for len(buf) > 0 {
		start := buf
		num, typ, tag := protowire.ConsumeTag(buf)
		if tag < 0 {
			return protowire.ParseError(tag)
		}
		buf = buf[tag:]
		f := wireField{number: num, kind: typ}
		n := 0
		switch typ {
		case protowire.BytesType:
			f.bytes, n = protowire.ConsumeBytes(buf)
		case protowire.VarintType:
			f.bits, n = protowire.ConsumeVarint(buf)
		case protowire.Fixed64Type:
			f.bits, n = protowire.ConsumeFixed64(buf)
		case protowire.Fixed32Type:
			var bits uint32
			bits, n = protowire.ConsumeFixed32(buf)
			f.bits = uint64(bits)
		default:
			n = protowire.ConsumeFieldValue(num, typ, buf)
		}
		if n < 0 {
			return protowire.ParseError(n)
		}
		f.raw = start[:tag+n]
		if err := yield(f); err != nil {
			return err
		}
		buf = buf[n:]
	}
	return nil
}

func fields(buf []byte, num protowire.Number, yield func([]byte) error) error {
	return walkWire(buf, func(f wireField) error {
		if f.number != num {
			return nil
		}
		if f.kind != protowire.BytesType {
			return fmt.Errorf("field %d must be length-delimited", num)
		}
		return yield(f.bytes)
	})
}

func message(buf []byte, num protowire.Number) ([]byte, error) {
	var out []byte
	first := true
	err := fields(buf, num, func(part []byte) error {
		if first {
			out, first = part, false
		} else {
			out = append(append([]byte(nil), out...), part...)
		}
		return nil
	})
	return out, err
}

func scalar(buf []byte, num protowire.Number, kind protowire.Type) (wireField, error) {
	var out wireField
	err := walkWire(buf, func(f wireField) error {
		if f.number == num {
			if f.kind != kind {
				return fmt.Errorf("incorrect wire type for field %d", num)
			}
			out = f
		}
		return nil
	})
	return out, err
}

func text(buf []byte, num protowire.Number) (string, error) {
	f, err := scalar(buf, num, protowire.BytesType)
	if err == nil && !utf8.Valid(f.bytes) {
		err = errors.New("invalid UTF-8 string")
	}
	return string(f.bytes), err
}

func attributes(buf []byte, num protowire.Number) payload.Attributes {
	return func(yield func(string, payload.Value) error) error {
		return fields(buf, num, func(kv []byte) error {
			key, err := text(kv, 1)
			if err != nil {
				return err
			}
			value, err := message(kv, 2)
			if err != nil {
				return err
			}
			return yield(key, wireValue(value))
		})
	}
}

type wireValue []byte

func (v wireValue) Read() (any, error) {
	return readWireValue(v, 0)
}

func readWireValue(v []byte, depth int) (any, error) {
	if depth > 100 {
		return nil, errors.New("value nesting exceeds 100")
	}
	var last wireField
	err := walkWire(v, func(f wireField) error {
		if f.number >= 1 && f.number <= 7 {
			if f.number == last.number && (f.number == 5 || f.number == 6) {
				f.bytes = append(append([]byte(nil), last.bytes...), f.bytes...)
			}
			last = f
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if last.number == 0 {
		return nil, nil
	}
	switch last.number {
	case 1, 5, 6, 7:
		if last.kind != protowire.BytesType {
			return nil, errors.New("incorrect AnyValue wire type")
		}
	case 2, 3:
		if last.kind != protowire.VarintType {
			return nil, errors.New("incorrect AnyValue wire type")
		}
	case 4:
		if last.kind != protowire.Fixed64Type {
			return nil, errors.New("incorrect AnyValue wire type")
		}
	}
	switch last.number {
	case 1:
		if !utf8.Valid(last.bytes) {
			return nil, errors.New("invalid UTF-8 value")
		}
		return string(last.bytes), nil
	case 2:
		return last.bits != 0, nil
	case 3:
		return int64(last.bits), nil
	case 4:
		return math.Float64frombits(last.bits), nil
	case 5:
		out := []any{}
		err = fields(last.bytes, 1, func(child []byte) error {
			value, childErr := readWireValue(child, depth+1)
			if childErr == nil {
				out = append(out, value)
			}
			return childErr
		})
		return out, err
	case 6:
		out := map[string]any{}
		err = fields(last.bytes, 1, func(kv []byte) error {
			key, keyErr := text(kv, 1)
			if keyErr != nil {
				return keyErr
			}
			value, valueErr := message(kv, 2)
			if valueErr != nil {
				return valueErr
			}
			out[key], valueErr = readWireValue(value, depth+1)
			return valueErr
		})
		return out, err
	default:
		return last.bytes, nil
	}
}

func protoView(rep payload.Representation) (payload.LogsView, error) {
	src, ok := rep.(*Proto)
	if !ok {
		return nil, fmt.Errorf("expected proto representation, got %T", rep)
	}
	return payload.LogsViewFunc(func(yield func(payload.ResourceLogs) error) error {
		return fields(src.data, 1, func(rl []byte) error {
			resource, err := message(rl, 1)
			if err != nil {
				return err
			}
			schema, err := text(rl, 3)
			if err != nil {
				return err
			}
			dropped, err := scalar(resource, 2, protowire.VarintType)
			if err != nil {
				return err
			}
			return yield(payload.ResourceLogs{
				SchemaURL: schema, Attributes: attributes(resource, 1), DroppedAttributes: uint32(dropped.bits),
				EntityRefs: func(yieldEntity func(payload.EntityRef) error) error {
					return fields(resource, 3, func(buf []byte) error {
						ref := payload.EntityRef{}
						schema, entityErr := text(buf, 1)
						if entityErr != nil {
							return entityErr
						}
						kind, entityErr := text(buf, 2)
						if entityErr != nil {
							return entityErr
						}
						ref.SchemaURL, ref.Type = schema, kind
						if keysErr := fields(buf, 3, func(key []byte) error { ref.IDKeys = append(ref.IDKeys, string(key)); return nil }); keysErr != nil {
							return keysErr
						}
						if keysErr := fields(buf, 4, func(key []byte) error { ref.DescriptionKeys = append(ref.DescriptionKeys, string(key)); return nil }); keysErr != nil {
							return keysErr
						}
						return yieldEntity(ref)
					})
				},
				Scopes: func(yieldScope func(payload.ScopeLogs) error) error {
					return fields(rl, 2, func(sl []byte) error {
						scope, scopeErr := message(sl, 1)
						if scopeErr != nil {
							return scopeErr
						}
						name, scopeErr := text(scope, 1)
						if scopeErr != nil {
							return scopeErr
						}
						version, scopeErr := text(scope, 2)
						if scopeErr != nil {
							return scopeErr
						}
						scopeSchema, scopeErr := text(sl, 3)
						if scopeErr != nil {
							return scopeErr
						}
						scopeDropped, scopeErr := scalar(scope, 4, protowire.VarintType)
						if scopeErr != nil {
							return scopeErr
						}
						return yieldScope(payload.ScopeLogs{
							Name: name, Version: version, SchemaURL: scopeSchema,
							Attributes: attributes(scope, 3), DroppedAttributes: uint32(scopeDropped.bits),
							Records: func(yieldLog func(payload.LogRecord) error) error {
								return fields(sl, 2, func(lr []byte) error {
									view, logErr := logView(lr)
									if logErr != nil {
										return logErr
									}
									return yieldLog(view)
								})
							},
						})
					})
				},
			})
		})
	}), nil
}

func logView(buf []byte) (payload.LogRecord, error) {
	body, err := message(buf, 5)
	if err != nil {
		return payload.LogRecord{}, err
	}
	l := payload.LogRecord{Attributes: attributes(buf, 6), Body: wireValue(body)}
	err = walkWire(buf, func(f wireField) error {
		var expected protowire.Type
		switch f.number {
		case 1, 11:
			expected = protowire.Fixed64Type
		case 2, 7:
			expected = protowire.VarintType
		case 8:
			expected = protowire.Fixed32Type
		case 3, 5, 6, 9, 10, 12:
			expected = protowire.BytesType
		default:
			return nil
		}
		if f.kind != expected {
			return fmt.Errorf("incorrect LogRecord wire type for field %d", f.number)
		}
		switch f.number {
		case 1:
			l.Timestamp = f.bits
		case 2:
			l.SeverityNumber = int32(f.bits)
		case 3:
			l.SeverityText = string(f.bytes)
		case 7:
			l.DroppedAttributes = uint32(f.bits)
		case 8:
			l.Flags = uint32(f.bits)
		case 9:
			if len(f.bytes) != 0 && len(f.bytes) != 16 {
				return errors.New("trace ID must be 16 bytes")
			}
			l.TraceID = f.bytes
		case 10:
			if len(f.bytes) != 0 && len(f.bytes) != 8 {
				return errors.New("span ID must be 8 bytes")
			}
			l.SpanID = f.bytes
		case 11:
			l.ObservedTimestamp = f.bits
		case 12:
			l.EventName = string(f.bytes)
		}
		return nil
	})
	return l, err
}

func sliceProto(rep payload.Representation, start, end int) (payload.Representation, error) {
	src, ok := rep.(*Proto)
	if !ok {
		return nil, fmt.Errorf("expected proto representation, got %T", rep)
	}
	index := 0
	total, err := src.ItemsCount()
	if err != nil {
		return nil, err
	}
	out, _, err := sliceWire(src.data, 0, start, end, total, &index)
	if err != nil {
		return nil, err
	}
	return newCountedProto(out, end-start), nil
}

func sliceWire(buf []byte, depth, start, end, total int, index *int) ([]byte, bool, error) {
	field := protowire.Number(2)
	if depth == 0 {
		field = 1
	}
	var out []byte
	kept, hadChild := false, false
	err := walkWire(buf, func(f wireField) error {
		if f.number != field {
			out = append(out, f.raw...)
			return nil
		}
		if f.kind != protowire.BytesType {
			return errors.New("incorrect logs envelope wire type")
		}
		hadChild = true
		if depth == 2 {
			if *index >= start && *index < end {
				out = append(out, f.raw...)
				kept = true
			}
			*index++
			return nil
		}
		child, keep, childErr := sliceWire(f.bytes, depth+1, start, end, total, index)
		if childErr != nil {
			return childErr
		}
		if keep {
			out = protowire.AppendTag(out, field, protowire.BytesType)
			out = protowire.AppendBytes(out, child)
			kept = true
		}
		return nil
	})
	if !hadChild && *index >= start && (*index < end || *index == total && end == total) {
		kept = true
	}
	return out, kept, err
}
