// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package plogpayload

import (
	"fmt"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/xpdata/entity"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

func objectAttributes(attrs pcommon.Map) payload.Attributes {
	return func(yield func(string, payload.Value) error) error {
		var err error
		attrs.Range(func(k string, v pcommon.Value) bool {
			err = yield(k, payload.ValueFunc(func() (any, error) { return v.AsRaw(), nil }))
			return err == nil
		})
		return err
	}
}

func objectView(rep payload.Representation) (payload.LogsView, error) {
	src, ok := rep.(*Objects)
	if !ok {
		return nil, fmt.Errorf("expected objects, got %T", rep)
	}
	return payload.LogsViewFunc(func(yield func(payload.ResourceLogs) error) error {
		for i := 0; i < src.data.ResourceLogs().Len(); i++ {
			rl := src.data.ResourceLogs().At(i)
			err := yield(payload.ResourceLogs{
				SchemaURL: rl.SchemaUrl(), Attributes: objectAttributes(rl.Resource().Attributes()),
				DroppedAttributes: rl.Resource().DroppedAttributesCount(),
				EntityRefs: func(yieldEntity func(payload.EntityRef) error) error {
					refs := entity.ResourceEntityRefs(rl.Resource())
					for i := 0; i < refs.Len(); i++ {
						ref := refs.At(i)
						if err := yieldEntity(payload.EntityRef{
							SchemaURL: ref.SchemaUrl(), Type: ref.Type(),
							IDKeys: ref.IdKeys().AsRaw(), DescriptionKeys: ref.DescriptionKeys().AsRaw(),
						}); err != nil {
							return err
						}
					}
					return nil
				},
				Scopes: func(yieldScope func(payload.ScopeLogs) error) error {
					for j := 0; j < rl.ScopeLogs().Len(); j++ {
						sl := rl.ScopeLogs().At(j)
						err := yieldScope(payload.ScopeLogs{
							Name: sl.Scope().Name(), Version: sl.Scope().Version(), SchemaURL: sl.SchemaUrl(),
							Attributes: objectAttributes(sl.Scope().Attributes()), DroppedAttributes: sl.Scope().DroppedAttributesCount(),
							Records: func(yieldLog func(payload.LogRecord) error) error {
								for k := 0; k < sl.LogRecords().Len(); k++ {
									lr := sl.LogRecords().At(k)
									tid, sid := lr.TraceID(), lr.SpanID()
									if err := yieldLog(payload.LogRecord{
										Timestamp: uint64(lr.Timestamp()), ObservedTimestamp: uint64(lr.ObservedTimestamp()),
										SeverityNumber: int32(lr.SeverityNumber()), SeverityText: lr.SeverityText(),
										EventName: lr.EventName(), Flags: uint32(lr.Flags()), DroppedAttributes: lr.DroppedAttributesCount(),
										TraceID: tid[:], SpanID: sid[:], Attributes: objectAttributes(lr.Attributes()),
										Body: payload.ValueFunc(func() (any, error) { return lr.Body().AsRaw(), nil }),
									}); err != nil {
										return err
									}
								}
								return nil
							},
						})
						if err != nil {
							return err
						}
					}
					return nil
				},
			})
			if err != nil {
				return err
			}
		}
		return nil
	}), nil
}

func sliceObjects(rep payload.Representation, start, end int) (payload.Representation, error) {
	src, ok := rep.(*Objects)
	if !ok {
		return nil, fmt.Errorf("expected objects, got %T", rep)
	}
	dst := plog.NewLogs()
	src.data.CopyTo(dst)
	total := src.data.LogRecordCount()
	belongs := func(index int) bool { return index >= start && (index < end || index == total && end == total) }
	index := 0
	dst.ResourceLogs().RemoveIf(func(rl plog.ResourceLogs) bool {
		if rl.ScopeLogs().Len() == 0 {
			return !belongs(index)
		}
		rl.ScopeLogs().RemoveIf(func(sl plog.ScopeLogs) bool {
			original := sl.LogRecords().Len()
			if original == 0 {
				return !belongs(index)
			}
			sl.LogRecords().RemoveIf(func(plog.LogRecord) bool {
				keep := index >= start && index < end
				index++
				return !keep
			})
			return original > 0 && sl.LogRecords().Len() == 0
		})
		return rl.ScopeLogs().Len() == 0
	})
	return newObjects(dst), nil
}
