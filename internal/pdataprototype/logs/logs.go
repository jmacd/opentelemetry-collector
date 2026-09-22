// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package logs supplies logs codecs for the isolated payload prototype.
package logs

import (
	"errors"
	"fmt"
	"io"

	"go.opentelemetry.io/collector/internal/pdataprototype/payload"
	"go.opentelemetry.io/collector/pdata/plog"
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

func (*Objects) Format() payload.Format { return ObjectsFormat }
func (o *Objects) ItemsCount() int      { return o.data.LogRecordCount() }
func (*Objects) Release()               {}

func newObjects(data plog.Logs) *Objects {
	data.MarkReadOnly()
	return &Objects{data: data}
}

func ObjectsCodec() payload.Codec {
	return payload.Codec{Format: ObjectsFormat, Merge: func(inputs []payload.Representation) (payload.Representation, error) {
		dst := plog.NewLogs()
		for _, input := range inputs {
			src, ok := input.(*Objects)
			if !ok {
				return nil, fmt.Errorf("expected objects, got %T", input)
			}
			appendLogs(src.data, dst)
		}
		return newObjects(dst), nil
	}}
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
	data.MarkReadOnly()
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

// WriteDebug demonstrates the object/view boundary, not a native Arrow/bytes view.
func WriteDebug(w io.Writer, p *payload.Payload) error {
	data, err := ReadOnlyLogs(p)
	if err != nil {
		return err
	}
	buf, err := (&plog.JSONMarshaler{}).MarshalLogs(data)
	if err != nil {
		return err
	}
	n, err := w.Write(buf)
	if err == nil && n != len(buf) {
		return io.ErrShortWrite
	}
	return err
}

// PersistentBytes returns borrowed OTLP bytes for a storage adapter. It is not a
// durable queue envelope: versioning and context persistence remain follow-ups.
// Arrow persistence is explicitly unsupported, even if bytes were cached.
func PersistentBytes(p *payload.Payload) ([]byte, error) {
	if p.Format() == ArrowFormat {
		return nil, errors.New("arrow persistence is unsupported")
	}
	return ProtoBytes(p)
}
