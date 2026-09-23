// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package queuebatch

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"

	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/request"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
	pdatareq "go.opentelemetry.io/collector/pdata/xpdata/request"
)

var payloadMagic = []byte{'P', 'L', 'D', 1}

type payloadEncoding struct{}

func (payloadEncoding) Marshal(ctx context.Context, req request.Request) ([]byte, error) {
	native, ok := req.(*PayloadRequest)
	if !ok {
		return nil, errors.New("unexpected native logs request type")
	}
	wire, err := plogpayload.PersistentBytes(native.Data)
	if err != nil {
		return nil, err
	}
	contextBytes := pdatareq.MarshalContext(ctx)
	if uint64(len(contextBytes)) > math.MaxUint32 {
		return nil, errors.New("durable context exceeds uint32")
	}
	out := append([]byte(nil), payloadMagic...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(contextBytes)))
	out = binary.LittleEndian.AppendUint32(out, 0)
	out = append(out, contextBytes...)
	out = append(out, wire...)
	binary.LittleEndian.PutUint32(out[8:12], crc32.ChecksumIEEE(out[12:]))
	return out, nil
}

func (payloadEncoding) Unmarshal(buf []byte) (context.Context, request.Request, error) {
	var ctx context.Context
	var wire []byte
	if bytes.HasPrefix(buf, payloadMagic[:3]) {
		if len(buf) < 12 || !bytes.Equal(buf[:4], payloadMagic) {
			return nil, nil, errors.New("invalid native queue envelope version/header")
		}
		n := uint64(binary.LittleEndian.Uint32(buf[4:8]))
		if n > uint64(len(buf)-12) || binary.LittleEndian.Uint32(buf[8:12]) != crc32.ChecksumIEEE(buf[12:]) {
			return nil, nil, errors.New("corrupt native queue envelope")
		}
		var err error
		ctx, err = pdatareq.UnmarshalContext(buf[12 : 12+int(n)])
		if err != nil {
			return nil, nil, err
		}
		wire = bytes.Clone(buf[12+int(n):])
	} else {
		// Existing durable queues contain the old context+logs envelope or raw
		// OTLP. Only their one-time migration uses message objects.
		oldCtx, oldReq, err := (logsEncoding{}).Unmarshal(buf)
		if err != nil {
			return nil, nil, err
		}
		wire, err = plogotlp.NewExportRequestFromLogs(oldReq.(*logsRequest).ld).MarshalProto()
		if err != nil {
			return nil, nil, err
		}
		ctx = oldCtx
	}
	reg, err := plogpayload.NewRegistry()
	if err != nil {
		return nil, nil, err
	}
	p, err := plogpayload.NewFromProto(reg, wire)
	if err != nil {
		return nil, nil, err
	}
	req, err := NewPayloadRequest(p)
	if err != nil {
		p.Release()
		return nil, nil, err
	}
	return ctx, req, nil
}
