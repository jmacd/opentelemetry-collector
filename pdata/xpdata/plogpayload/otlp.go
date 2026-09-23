// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package plogpayload

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"

	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
	"go.opentelemetry.io/collector/pdata/xpdata/pref"
)

// Proto owns an uncompressed ExportLogsServiceRequest wire message.
type Proto struct {
	data      []byte
	countOnce sync.Once
	count     int
	countErr  error
}

func (*Proto) Format() payload.Format { return ProtoFormat }
func (p *Proto) ItemsCount() (int, error) {
	p.countOnce.Do(func() { p.count, p.countErr = countLogs(p.data, 0) })
	return p.count, p.countErr
}
func (*Proto) Release() {}

// Bytes returns the borrowed immutable wire body.
func (p *Proto) Bytes() []byte { return p.data }

// NewProtoRepresentation wraps an owned body without forcing a count or decode.
func NewProtoRepresentation(buf []byte) *Proto { return &Proto{data: buf} }

func newCountedProto(buf []byte, count int) *Proto {
	p := &Proto{data: buf}
	p.countOnce.Do(func() { p.count = count })
	return p
}

func CountProto(buf []byte) (int, error) { return countLogs(buf, 0) }

// NewFromProto transfers immutable ownership of buf on success. The caller must
// copy transport-owned/reused buffers before calling. No decode or count is
// forced. ItemsCount lazily validates/counts known wire fields without objects.
func NewFromProto(reg *payload.Registry, buf []byte) (*payload.Payload, error) {
	return reg.New(&Proto{data: buf})
}

func ProtoBytes(p *payload.Payload) ([]byte, error) {
	rep, err := p.As(ProtoFormat)
	if err != nil {
		return nil, err
	}
	bytes, ok := rep.(*Proto)
	if !ok {
		return nil, fmt.Errorf("expected OTLP bytes, got %T", rep)
	}
	return bytes.data, nil
}

func ProtoCodec() payload.Codec {
	return payload.Codec{
		Format: ProtoFormat,
		View:   protoView,
		Slice:  sliceProto,
		Size: func(rep payload.Representation) (int, error) {
			src, ok := rep.(*Proto)
			if !ok {
				return 0, fmt.Errorf("expected proto representation, got %T", rep)
			}
			return len(src.data), nil
		},
		Decode: func(input payload.Representation) (payload.Representation, error) {
			src, ok := input.(*Proto)
			if !ok {
				return nil, fmt.Errorf("expected OTLP bytes, got %T", input)
			}
			req := plogotlp.NewExportRequest()
			if err := req.UnmarshalProto(src.data); err != nil {
				pref.UnrefLogs(req.Logs())
				return nil, err
			}
			return newObjects(req.Logs()), nil
		},
		Encode: func(input payload.Representation) (payload.Representation, error) {
			src, ok := input.(*Objects)
			if !ok {
				return nil, fmt.Errorf("expected objects, got %T", input)
			}
			buf, err := plogotlp.NewExportRequestFromLogs(src.data).MarshalProto()
			if err != nil {
				return nil, err
			}
			return newCountedProto(buf, src.data.LogRecordCount()), nil
		},
		Merge: mergeProto,
	}
}

func mergeProto(inputs []payload.Representation) (payload.Representation, error) {
	size, count := 0, 0
	for _, input := range inputs {
		src, ok := input.(*Proto)
		if !ok {
			return nil, fmt.Errorf("expected OTLP bytes, got %T", input)
		}
		if len(src.data) > math.MaxInt-size {
			return nil, errors.New("merged OTLP size/count overflows int")
		}
		size += len(src.data)
		items, err := src.ItemsCount()
		if err != nil {
			return nil, err
		}
		if items > math.MaxInt-count {
			return nil, errors.New("merged OTLP count overflows int")
		}
		count += items
	}
	buf := make([]byte, 0, size)
	for _, input := range inputs {
		buf = append(buf, input.(*Proto).data...)
	}
	// ExportLogsServiceRequest has repeated resource_logs at field 1, so
	// concatenation is protobuf merge (not concatenation of gRPC/HTTP frames).
	return newCountedProto(buf, count), nil
}

func countLogs(buf []byte, depth int) (int, error) {
	// ExportRequest.resource_logs=1, ResourceLogs.scope_logs=2,
	// ScopeLogs.log_records=2. Do not recurse into LogRecord fields.
	field := protowire.Number(2)
	if depth == 0 {
		field = 1
	}
	count := 0
	for len(buf) > 0 {
		num, typ, n := protowire.ConsumeTag(buf)
		if n < 0 {
			return 0, protowire.ParseError(n)
		}
		buf = buf[n:]
		if num != field {
			if num == 3 && depth > 0 {
				value, consumed := protowire.ConsumeBytes(buf)
				if typ != protowire.BytesType || consumed < 0 || !utf8.Valid(value) {
					return 0, errors.New("invalid schema URL")
				}
			}
			if num == 1 && depth > 0 {
				child, consumed := protowire.ConsumeBytes(buf)
				if typ != protowire.BytesType || consumed < 0 {
					return 0, errors.New("invalid resource/scope envelope")
				}
				name := "resource"
				if depth == 2 {
					name = "scope"
				}
				if err := validateMessage(child, name, 0); err != nil {
					return 0, err
				}
			}
			n = protowire.ConsumeFieldValue(num, typ, buf)
			if n < 0 {
				return 0, protowire.ParseError(n)
			}
			buf = buf[n:]
			continue
		}
		if typ != protowire.BytesType {
			return 0, fmt.Errorf("logs envelope field %d at depth %d is not length-delimited", num, depth)
		}
		child, n := protowire.ConsumeBytes(buf)
		if n < 0 {
			return 0, protowire.ParseError(n)
		}
		buf = buf[n:]
		if depth == 2 {
			if err := validateMessage(child, "log", 0); err != nil {
				return 0, err
			}
			count++
		} else {
			childCount, err := countLogs(child, depth+1)
			if err != nil {
				return 0, err
			}
			count += childCount
		}
	}
	return count, nil
}
