// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package plogpayload supplies object and protobuf adapters for native logs payloads.
package plogpayload

import (
	"errors"
	"fmt"
	"io"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
	"go.opentelemetry.io/collector/pdata/xpdata/pref"
)

const (
	ObjectsFormat payload.Format = "pdata/logs"
	ProtoFormat   payload.Format = "otlp/protobuf/logs/v1"
	ArrowFormat   payload.Format = "otap/arrow/logs/v1"
)

// Objects wraps read-only pdata. Mutators must use MutableLogs instead.
type Objects struct {
	data plog.Logs
}

func (*Objects) Format() payload.Format     { return ObjectsFormat }
func (o *Objects) ItemsCount() (int, error) { return o.data.LogRecordCount(), nil }
func (o *Objects) Release()                 { pref.UnrefLogs(o.data) }

// Logs returns borrowed, read-only objects.
func (o *Objects) Logs() plog.Logs { return o.data }

// NewObjects transfers data into a read-only representation for codec adapters.
func NewObjects(data plog.Logs) *Objects { return newObjects(data) }

func newObjects(data plog.Logs) *Objects {
	if !data.IsReadOnly() {
		data.MarkReadOnly()
	}
	pref.MarkPipelineOwnedLogs(data)
	return &Objects{data: data}
}

func ObjectsCodec() payload.Codec {
	return payload.Codec{
		Format: ObjectsFormat, View: objectView, Slice: sliceObjects, MergeFormat: ProtoFormat,
		Size: func(rep payload.Representation) (int, error) {
			src, ok := rep.(*Objects)
			if !ok {
				return 0, fmt.Errorf("expected objects, got %T", rep)
			}
			return (&plog.ProtoMarshaler{}).LogsSize(src.data), nil
		}, Merge: func(inputs []payload.Representation) (payload.Representation, error) {
			dst := plog.NewLogs()
			for _, input := range inputs {
				src, ok := input.(*Objects)
				if !ok {
					pref.UnrefLogs(dst)
					return nil, fmt.Errorf("expected objects, got %T", input)
				}
				appendLogs(src.data, dst)
			}
			return newObjects(dst), nil
		},
	}
}

func appendLogs(src, dst plog.Logs) {
	for i := 0; i < src.ResourceLogs().Len(); i++ {
		src.ResourceLogs().At(i).CopyTo(dst.ResourceLogs().AppendEmpty())
	}
}

// NewFromLogs transfers ownership on success, marking data read-only.
func NewFromLogs(reg *payload.Registry, data plog.Logs) (*payload.Payload, error) {
	// Register before marking read-only so a failed registration leaves data alone.
	rep := &Objects{data: data}
	p, err := reg.New(rep)
	if err != nil {
		return nil, err
	}
	if !data.IsReadOnly() {
		data.MarkReadOnly()
	}
	pref.MarkPipelineOwnedLogs(data)
	return p, nil
}

func ReadOnlyLogs(p *payload.Payload) (plog.Logs, error) {
	rep, err := p.As(ObjectsFormat)
	if err != nil {
		return plog.Logs{}, err
	}
	objects, ok := rep.(*Objects)
	if !ok {
		return plog.Logs{}, fmt.Errorf("expected objects, got %T", rep)
	}
	return objects.data, nil
}

// MutableLogs returns an independent copy, never invalidating cached bytes/records.
func MutableLogs(p *payload.Payload) (plog.Logs, error) {
	src, err := ReadOnlyLogs(p)
	if err != nil {
		return plog.Logs{}, err
	}
	dst := plog.NewLogs()
	src.CopyTo(dst)
	return dst, nil
}

// WriteDebug renders the source representation's native view without requesting objects.
func WriteDebug(w io.Writer, p *payload.Payload) error {
	view, err := p.View()
	if err != nil {
		return err
	}
	return payload.WriteDebug(w, view)
}

// NewRegistry registers the built-in object and protobuf codecs. Distributions
// can explicitly add Arrow or other codecs using payload.NewRegistry.
func NewRegistry() (*payload.Registry, error) {
	return payload.NewRegistry(ObjectsCodec(), ProtoCodec())
}

// PersistentBytes returns borrowed OTLP bytes for the queue's durable envelope.
// Arrow persistence is explicitly unsupported, even if bytes were cached.
func PersistentBytes(p *payload.Payload) ([]byte, error) {
	if p.Format() == ArrowFormat {
		return nil, errors.New("arrow persistence is unsupported")
	}
	return ProtoBytes(p)
}
