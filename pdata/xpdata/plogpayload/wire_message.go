// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package plogpayload

import "bytes"

// WireMessage implements pdata's existing gRPC codec contract without message
// objects. Marshal borrows Body; Unmarshal copies the codec's pooled input.
type WireMessage struct{ Body []byte }

func (m *WireMessage) SizeProto() int              { return len(m.Body) }
func (m *WireMessage) MarshalProto(dst []byte) int { return copy(dst, m.Body) }
func (m *WireMessage) UnmarshalProto(src []byte) error {
	m.Body = bytes.Clone(src)
	return nil
}
