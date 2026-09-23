// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package plogpayload

import (
	"fmt"
	"math"

	"google.golang.org/protobuf/encoding/protowire"

	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

func putBytes(dst []byte, field protowire.Number, value []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(dst, field, protowire.BytesType), value)
}

func putInt(dst []byte, field protowire.Number, value uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(dst, field, protowire.VarintType), value)
}

func marshalValue(value any) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case string:
		return putBytes(nil, 1, []byte(v)), nil
	case bool:
		n := uint64(0)
		if v {
			n = 1
		}
		return putInt(nil, 2, n), nil
	case int64:
		return putInt(nil, 3, uint64(v)), nil
	case float64:
		return protowire.AppendFixed64(protowire.AppendTag(nil, 4, protowire.Fixed64Type), math.Float64bits(v)), nil
	case []byte:
		return putBytes(nil, 7, v), nil
	case []any:
		var out []byte
		for _, child := range v {
			buf, err := marshalValue(child)
			if err != nil {
				return nil, err
			}
			out = putBytes(out, 1, buf)
		}
		return putBytes(nil, 5, out), nil
	case map[string]any:
		var out []byte
		for key, child := range v {
			buf, err := marshalValue(child)
			if err != nil {
				return nil, err
			}
			out = putBytes(out, 1, putBytes(putBytes(nil, 1, []byte(key)), 2, buf))
		}
		return putBytes(nil, 6, out), nil
	default:
		return nil, fmt.Errorf("unsupported native value %T", value)
	}
}

func marshalAttributes(dst []byte, field protowire.Number, attrs payload.Attributes) ([]byte, error) {
	err := attrs(func(k string, v payload.Value) error {
		raw, err := v.Read()
		if err != nil {
			return err
		}
		buf, err := marshalValue(raw)
		if err != nil {
			return err
		}
		dst = putBytes(dst, field, putBytes(putBytes(nil, 1, []byte(k)), 2, buf))
		return nil
	})
	return dst, err
}

// MarshalView writes OTLP wire fields directly from a native view.
func MarshalView(view payload.LogsView) ([]byte, error) {
	var out []byte
	err := view.Resources(func(r payload.ResourceLogs) error {
		resource, err := marshalAttributes(nil, 1, r.Attributes)
		if err != nil {
			return err
		}
		resource = putInt(resource, 2, uint64(r.DroppedAttributes))
		if r.EntityRefs != nil {
			if entityErr := r.EntityRefs(func(ref payload.EntityRef) error {
				buf := putBytes(putBytes(nil, 1, []byte(ref.SchemaURL)), 2, []byte(ref.Type))
				for _, key := range ref.IDKeys {
					buf = putBytes(buf, 3, []byte(key))
				}
				for _, key := range ref.DescriptionKeys {
					buf = putBytes(buf, 4, []byte(key))
				}
				resource = putBytes(resource, 3, buf)
				return nil
			}); entityErr != nil {
				return entityErr
			}
		}
		rl := putBytes(putBytes(nil, 1, resource), 3, []byte(r.SchemaURL))
		err = r.Scopes(func(s payload.ScopeLogs) error {
			scope, scopeErr := marshalAttributes(putBytes(putBytes(nil, 1, []byte(s.Name)), 2, []byte(s.Version)), 3, s.Attributes)
			if scopeErr != nil {
				return scopeErr
			}
			scope = putInt(scope, 4, uint64(s.DroppedAttributes))
			sl := putBytes(putBytes(nil, 1, scope), 3, []byte(s.SchemaURL))
			scopeErr = s.Records(func(l payload.LogRecord) error {
				var lr []byte
				lr = protowire.AppendFixed64(protowire.AppendTag(lr, 1, protowire.Fixed64Type), l.Timestamp)
				lr = putBytes(putInt(lr, 2, uint64(l.SeverityNumber)), 3, []byte(l.SeverityText))
				body, bodyErr := l.Body.Read()
				if bodyErr != nil {
					return bodyErr
				}
				bodyBytes, bodyErr := marshalValue(body)
				if bodyErr != nil {
					return bodyErr
				}
				lr, bodyErr = marshalAttributes(putBytes(lr, 5, bodyBytes), 6, l.Attributes)
				if bodyErr != nil {
					return bodyErr
				}
				lr = putInt(lr, 7, uint64(l.DroppedAttributes))
				lr = protowire.AppendFixed32(protowire.AppendTag(lr, 8, protowire.Fixed32Type), l.Flags)
				lr = putBytes(putBytes(lr, 9, l.TraceID), 10, l.SpanID)
				lr = protowire.AppendFixed64(protowire.AppendTag(lr, 11, protowire.Fixed64Type), l.ObservedTimestamp)
				lr = putBytes(lr, 12, []byte(l.EventName))
				sl = putBytes(sl, 2, lr)
				return nil
			})
			if scopeErr == nil {
				rl = putBytes(rl, 2, sl)
			}
			return scopeErr
		})
		if err == nil {
			out = putBytes(out, 1, rl)
		}
		return err
	})
	return out, err
}
