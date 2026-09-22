// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"errors"
	"fmt"
	"math"
	"sync"

	arrowpb "github.com/open-telemetry/otel-arrow/go/api/experimental/arrow/v1"
	"github.com/open-telemetry/otel-arrow/go/pkg/config"
	arrowrecord "github.com/open-telemetry/otel-arrow/go/pkg/otel/arrow_record"
	"github.com/open-telemetry/otel-arrow/go/pkg/otel/common/schema"
	logsotlp "github.com/open-telemetry/otel-arrow/go/pkg/otel/logs/otlp"
	"github.com/open-telemetry/otel-arrow/go/pkg/record_message"

	"go.opentelemetry.io/collector/internal/pdataprototype/payload"
	"go.opentelemetry.io/collector/pdata/plog"
)

// Records owns groups of real OTAP main/related Arrow records, not IPC bytes.
// Group boundaries preserve independent attribute/resource IDs and dictionaries.
type Records struct {
	batches [][]*record_message.RecordMessage
	items   int
}

func (*Records) Format() payload.Format { return ArrowFormat }
func (r *Records) ItemsCount() int      { return r.items }

// Batches returns borrowed immutable records, valid for the payload's lifetime.
func (r *Records) Batches() [][]*record_message.RecordMessage { return r.batches }

func (r *Records) Release() {
	for _, batch := range r.batches {
		for _, rec := range batch {
			rec.Record().Release()
		}
	}
}

// NewFromRecords transfers ownership only on success. Slices, message metadata,
// and record buffers must not subsequently be mutated by the caller.
func NewFromRecords(reg *payload.Registry, batches [][]*record_message.RecordMessage) (*payload.Payload, error) {
	count := 0
	for _, batch := range batches {
		mainRecords := 0
		for _, rec := range batch {
			if rec == nil || rec.Record() == nil {
				return nil, errors.New("nil Arrow record")
			}
			switch rec.PayloadType() {
			case arrowpb.ArrowPayloadType_LOGS:
				mainRecords++
				rows := rec.Record().NumRows()
				if rows < 0 || rows > int64(math.MaxInt-count) {
					return nil, errors.New("arrow item count overflows int")
				}
				count += int(rows)
			case arrowpb.ArrowPayloadType_RESOURCE_ATTRS, arrowpb.ArrowPayloadType_SCOPE_ATTRS, arrowpb.ArrowPayloadType_LOG_ATTRS:
			default:
				return nil, fmt.Errorf("unexpected logs record type %v", rec.PayloadType())
			}
		}
		if mainRecords != 1 {
			return nil, fmt.Errorf("expected one main logs record per group, got %d", mainRecords)
		}
	}
	return reg.New(&Records{batches: batches, items: count})
}

// ArrowCodec owns a reusable encoder. Close after all encoding work is done.
// Already-created records retain their buffers independently of this encoder.
type ArrowCodec struct {
	mu       sync.Mutex
	producer *arrowrecord.Producer
	options  []config.Option
	closed   bool
}

func NewArrowCodec(options ...config.Option) *ArrowCodec {
	return &ArrowCodec{options: options}
}

func (c *ArrowCodec) Codec() payload.Codec {
	return payload.Codec{Format: ArrowFormat, Decode: decodeRecords, Encode: c.encode, Merge: mergeRecords}
}

func (c *ArrowCodec) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.producer == nil {
		return nil
	}
	err := c.producer.Close()
	c.producer = nil
	return err
}

func decodeRecords(input payload.Representation) (payload.Representation, error) {
	src, ok := input.(*Records)
	if !ok {
		return nil, fmt.Errorf("expected Arrow records, got %T", input)
	}
	ld, err := decodeArrow(src)
	if err != nil {
		return nil, err
	}
	return newObjects(ld), nil
}

func decodeArrow(src *Records) (plog.Logs, error) {
	dst := plog.NewLogs()
	for _, batch := range src.batches {
		// RelatedDataFrom consumes a reference to every record, including the
		// main record. Keep the payload's references alive through LogsFrom.
		for _, rec := range batch {
			rec.Record().Retain()
		}
		related, main, err := logsotlp.RelatedDataFrom(batch)
		if err != nil {
			return plog.Logs{}, err
		}
		if main == nil {
			return plog.Logs{}, errors.New("missing main logs record")
		}
		ld, err := logsotlp.LogsFrom(main.Record(), related)
		if err != nil {
			return plog.Logs{}, err
		}
		ld.ResourceLogs().MoveAndAppendTo(dst.ResourceLogs())
	}
	return dst, nil
}

func (c *ArrowCodec) encode(input payload.Representation) (out payload.Representation, err error) {
	src, ok := input.(*Objects)
	if !ok {
		return nil, fmt.Errorf("expected objects, got %T", input)
	}
	for i := 0; i < src.data.ResourceLogs().Len(); i++ {
		rl := src.data.ResourceLogs().At(i)
		if rl.ScopeLogs().Len() == 0 {
			return nil, errors.New("arrow encoding cannot preserve an empty resource group")
		}
		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			if rl.ScopeLogs().At(j).LogRecords().Len() == 0 {
				return nil, errors.New("arrow encoding cannot preserve an empty scope group")
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("arrow encoder is closed")
	}
	if c.producer == nil {
		c.producer = arrowrecord.NewProducerWithOptions(c.options...)
	}
	defer func() {
		if err != nil {
			// Failed builders may contain partial rows. Never reuse that state.
			err = errors.Join(err, c.producer.Close())
			c.producer = nil
		}
	}()
	builder := c.producer.LogsBuilder()
	for range 6 {
		builder.RelatedData().Reset()
		if appendErr := builder.Append(src.data); appendErr != nil {
			return nil, appendErr
		}
		main, buildErr := builder.Build()
		if buildErr != nil {
			if main != nil {
				main.Release()
			}
			if errors.Is(buildErr, schema.ErrSchemaNotUpToDate) {
				continue
			}
			return nil, buildErr
		}
		related, buildErr := builder.RelatedData().BuildRecordMessages()
		if buildErr != nil {
			main.Release()
			for _, rec := range related {
				rec.Record().Release()
			}
			return nil, buildErr
		}
		batch := append([]*record_message.RecordMessage{
			record_message.NewLogsMessage(c.producer.LogsRecordBuilderExt().SchemaID(), main),
		}, related...)
		return &Records{batches: [][]*record_message.RecordMessage{batch}, items: src.ItemsCount()}, nil
	}
	return nil, errors.New("arrow schema did not stabilize after six attempts")
}

func mergeRecords(inputs []payload.Representation) (payload.Representation, error) {
	dst := &Records{}
	for _, input := range inputs {
		src, ok := input.(*Records)
		if !ok {
			dst.Release()
			return nil, fmt.Errorf("expected Arrow records, got %T", input)
		}
		if src.items > math.MaxInt-dst.items {
			dst.Release()
			return nil, errors.New("merged Arrow item count overflows int")
		}
		for _, batch := range src.batches {
			for _, rec := range batch {
				rec.Record().Retain()
			}
			dst.batches = append(dst.batches, batch)
		}
		dst.items += src.items
	}
	return dst, nil
}
