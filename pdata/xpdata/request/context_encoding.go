// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package request

import (
	"context"

	"go.opentelemetry.io/collector/pdata/internal"
)

// MarshalContext encodes the existing durable request context without requiring
// signal message objects.
func MarshalContext(ctx context.Context) []byte {
	rc := encodeContext(ctx)
	buf := make([]byte, rc.SizeProto())
	rc.MarshalProto(buf)
	return buf
}

func UnmarshalContext(buf []byte) (context.Context, error) {
	rc := &internal.RequestContext{}
	if err := rc.UnmarshalProto(buf); err != nil {
		return nil, err
	}
	return decodeContext(context.Background(), rc), nil
}
