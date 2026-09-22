// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"errors"
	"fmt"
	"math"

	"google.golang.org/protobuf/encoding/protowire"

	"go.opentelemetry.io/collector/internal/pdataprototype/payload"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
)

// Proto owns an uncompressed ExportLogsServiceRequest wire message.
type Proto struct {
	data  []byte
	items int
}

func (*Proto) Format() payload.Format { return ProtoFormat }
func (p *Proto) ItemsCount() int      { return p.items }
func (*Proto) Release()               {}

// NewFromProto transfers immutable ownership of buf on success. The caller must
// copy transport-owned/reused buffers before calling. It scans message envelopes
// to count logs; validation inside each LogRecord is deferred to object decoding.
func NewFromProto(reg *payload.Registry, buf []byte) (*payload.Payload, error) {
	count, err := countLogs(buf, 0)
	if err != nil {
		return nil, err
	}
	return reg.New(&Proto{data: buf, items: count})
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
		Decode: func(input payload.Representation) (payload.Representation, error) {
			src, ok := input.(*Proto)
			if !ok {
				return nil, fmt.Errorf("expected OTLP bytes, got %T", input)
			}
			req := plogotlp.NewExportRequest()
			if err := req.UnmarshalProto(src.data); err != nil {
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
			return &Proto{data: buf, items: src.ItemsCount()}, nil
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
		if len(src.data) > math.MaxInt-size || src.items > math.MaxInt-count {
			return nil, errors.New("merged OTLP size/count overflows int")
		}
		size += len(src.data)
		count += src.items
	}
	buf := make([]byte, 0, size)
	for _, input := range inputs {
		buf = append(buf, input.(*Proto).data...)
	}
	// ExportLogsServiceRequest has repeated resource_logs at field 1, so
	// concatenation is protobuf merge (not concatenation of gRPC/HTTP frames).
	return &Proto{data: buf, items: count}, nil
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
