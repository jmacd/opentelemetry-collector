// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package queuebatch

import (
	"context"
	"errors"

	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/request"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
	"go.opentelemetry.io/collector/pdata/xpdata/pref"
)

type PayloadRequest struct {
	Data         *payload.Payload
	items, bytes int
}

// NewPayloadRequest resolves fallible native measurements before admission to
// exporterhelper's infallible Request sizing API. It borrows p.
func NewPayloadRequest(p *payload.Payload) (*PayloadRequest, error) {
	items, err := p.ItemsCount()
	if err != nil {
		return nil, err
	}
	bytes, err := p.BytesSize()
	if err != nil {
		return nil, err
	}
	return &PayloadRequest{Data: p, items: items, bytes: bytes}, nil
}

func (r *PayloadRequest) ItemsCount() int { return r.items }
func (r *PayloadRequest) BytesSize() int  { return r.bytes }
func (r *PayloadRequest) Release()        { r.Data.Release() }

func (r *PayloadRequest) HandleError(err error) (request.Request, error) {
	var partial *payload.PartialError
	var subset *payload.Payload
	var legacy consumererror.Logs
	switch {
	case errors.As(err, &partial):
		subset, err = r.Data.RetrySubset(partial.Retry)
	case errors.As(err, &legacy):
		if !pref.MarkPipelineOwnedLogs(legacy.Data()) {
			pref.RefLogs(legacy.Data())
		}
		subset, err = plogpayload.NewFromLogs(r.Data.Registry(), legacy.Data())
		if err != nil {
			pref.UnrefLogs(legacy.Data())
		}
	default:
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	retry, err := NewPayloadRequest(subset)
	if err != nil {
		subset.Release()
		return nil, err
	}
	return retry, nil
}

func (r *PayloadRequest) MergeSplit(_ context.Context, limit int, sizer request.SizerType, other request.Request) ([]request.Request, error) {
	var next *payload.Payload
	if other != nil {
		o, ok := other.(*PayloadRequest)
		if !ok {
			return nil, errors.New("incompatible payload request")
		}
		next = o.Data
	}
	var measure payload.Sizer
	switch sizer {
	case request.SizerTypeItems:
		measure = payload.SizerItems
	case request.SizerTypeBytes:
		measure = payload.SizerBytes
	default:
		return nil, errors.New("unsupported payload batch sizer")
	}
	parts, err := r.Data.MergeSplit(limit, measure, next)
	if err != nil {
		return nil, consumererror.NewPermanent(err)
	}
	out := make([]request.Request, 0, len(parts))
	for _, part := range parts {
		req, err := NewPayloadRequest(part)
		if err != nil {
			for _, p := range parts {
				p.Release()
			}
			return nil, err
		}
		out = append(out, req)
	}
	return out, nil
}

type payloadReferences struct{}

func (payloadReferences) Ref(req request.Request) {
	if err := req.(*PayloadRequest).Data.Retain(); err != nil {
		panic(err)
	}
}
func (payloadReferences) Unref(req request.Request) { req.(*PayloadRequest).Data.Release() }

func NewPayloadQueueBatchSettings() Settings[request.Request] {
	return Settings[request.Request]{ReferenceCounter: payloadReferences{}, Encoding: payloadEncoding{}}
}
