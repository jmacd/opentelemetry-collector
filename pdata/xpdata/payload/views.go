// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package payload

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
)

// Value borrows a native value. Read decodes only this value, never its enclosing
// pdata message. Bytes, maps and arrays returned by Read are read-only.
type Value interface {
	Read() (any, error)
}

type ValueFunc func() (any, error)

func (f ValueFunc) Read() (any, error) { return f() }

type Attributes func(func(string, Value) error) error

func (a Attributes) Lookup(key string) (any, bool, error) {
	var value any
	found := false
	err := a(func(k string, v Value) error {
		if k != key {
			return nil
		}
		var err error
		value, err = v.Read()
		found = true
		return err
	})
	return value, found, err
}

func (a Attributes) Read() (map[string]any, error) {
	out := map[string]any{}
	err := a(func(k string, v Value) error {
		value, err := v.Read()
		if err != nil {
			return err
		}
		out[k] = value
		return nil
	})
	return out, err
}

type LogRecord struct {
	Timestamp, ObservedTimestamp uint64
	SeverityNumber               int32
	SeverityText, EventName      string
	TraceID, SpanID              []byte
	Flags, DroppedAttributes     uint32
	Body                         Value
	Attributes                   Attributes
}

type ScopeLogs struct {
	Name, Version, SchemaURL string
	DroppedAttributes        uint32
	Attributes               Attributes
	Records                  func(func(LogRecord) error) error
}

type ResourceLogs struct {
	SchemaURL         string
	DroppedAttributes uint32
	Attributes        Attributes
	Scopes            func(func(ScopeLogs) error) error
	EntityRefs        func(func(EntityRef) error) error
}

type EntityRef struct {
	SchemaURL, Type         string
	IDKeys, DescriptionKeys []string
}

// LogsView borrows resource/scope/log fields from the original representation.
// Visitors may return an error to terminate traversal; all errors propagate.
type LogsView interface {
	Resources(func(ResourceLogs) error) error
}

type LogsViewFunc func(func(ResourceLogs) error) error

func (f LogsViewFunc) Resources(yield func(ResourceLogs) error) error { return f(yield) }

func RangeLogs(view LogsView, yield func(ResourceLogs, ScopeLogs, LogRecord) error) error {
	return view.Resources(func(r ResourceLogs) error {
		return r.Scopes(func(s ScopeLogs) error {
			return s.Records(func(l LogRecord) error { return yield(r, s, l) })
		})
	})
}

// WriteDebug visits native fields, including empty resource/scope groups.
// It never requests the canonical representation.
func WriteDebug(w io.Writer, view LogsView) error {
	write := func(kind string, data map[string]any) error {
		buf, err := json.Marshal(debugValue(data))
		if err != nil {
			return err
		}
		buf = append(append([]byte(kind+": "), buf...), '\n')
		n, err := w.Write(buf)
		if err == nil && n != len(buf) {
			return io.ErrShortWrite
		}
		return err
	}
	return view.Resources(func(r ResourceLogs) error {
		attrs, err := r.Attributes.Read()
		if err != nil {
			return err
		}
		entities := []EntityRef{}
		if r.EntityRefs != nil {
			if entityErr := r.EntityRefs(func(ref EntityRef) error { entities = append(entities, ref); return nil }); entityErr != nil {
				return entityErr
			}
		}
		if err := write("ResourceLogs", map[string]any{"schema_url": r.SchemaURL, "attributes": attrs, "dropped_attributes_count": r.DroppedAttributes, "entity_refs": entities}); err != nil {
			return err
		}

		return r.Scopes(func(s ScopeLogs) error {
			attrs, err := s.Attributes.Read()
			if err != nil {
				return err
			}
			if err := write("ScopeLogs", map[string]any{"name": s.Name, "version": s.Version, "schema_url": s.SchemaURL, "attributes": attrs, "dropped_attributes_count": s.DroppedAttributes}); err != nil {
				return err
			}
			return s.Records(func(l LogRecord) error {
				attrs, err := l.Attributes.Read()
				if err != nil {
					return err
				}
				body, err := l.Body.Read()
				if err != nil {
					return err
				}
				return write("LogRecord", map[string]any{
					"timestamp": l.Timestamp, "observed_timestamp": l.ObservedTimestamp,
					"severity_number": l.SeverityNumber, "severity_text": l.SeverityText,
					"body": body, "attributes": attrs, "event_name": l.EventName,
					"trace_id": hex.EncodeToString(l.TraceID), "span_id": hex.EncodeToString(l.SpanID),
					"flags": l.Flags, "dropped_attributes_count": l.DroppedAttributes,
				})
			})
		})
	})
}

func debugValue(v any) any {
	switch value := v.(type) {
	case float64:
		switch {
		case math.IsNaN(value):
			return "NaN"
		case math.IsInf(value, 1):
			return "Infinity"
		case math.IsInf(value, -1):
			return "-Infinity"
		}
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, child := range value {
			out[k] = debugValue(child)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, child := range value {
			out[i] = debugValue(child)
		}
		return out
	}
	return v
}

// Partition routes individual logs using native resource/scope/log views. It
// preserves each selected record's original representation using native slices.
// Nil outputs denote empty partitions; the caller owns every non-nil output.
func (p *Payload) Partition(match func(ResourceLogs, ScopeLogs, LogRecord) (bool, error)) (yes, no *Payload, err error) {
	view, err := p.View()
	if err != nil {
		return nil, nil, err
	}
	var selected [2][]*Payload
	defer func() {
		for _, group := range selected {
			for _, part := range group {
				part.Release()
			}
		}
		if err != nil && yes != nil {
			yes.Release()
			yes = nil
		}
	}()
	index, start, previous := 0, 0, -1
	flush := func() error {
		if previous < 0 {
			return nil
		}
		part, sliceErr := p.Slice(start, index)
		if sliceErr != nil {
			return sliceErr
		}
		selected[previous] = append(selected[previous], part)
		start = index
		return nil
	}
	err = RangeLogs(view, func(r ResourceLogs, s ScopeLogs, l LogRecord) error {
		matched, matchErr := match(r, s, l)
		if matchErr != nil {
			return matchErr
		}
		group := 0
		if matched {
			group = 1
		}
		if previous != group {
			if flushErr := flush(); flushErr != nil {
				return flushErr
			}
			previous = group
		}
		index++
		return nil
	})
	if err == nil {
		err = flush()
	}
	if err != nil {
		return nil, nil, err
	}
	if len(selected[1]) > 0 {
		yes, err = p.registry.Merge(selected[1]...)
		if err != nil {
			return nil, nil, err
		}
	}
	if len(selected[0]) > 0 {
		no, err = p.registry.Merge(selected[0]...)
	}
	return yes, no, err
}
