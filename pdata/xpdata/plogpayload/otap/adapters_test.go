// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otap

import "go.opentelemetry.io/collector/pdata/xpdata/plogpayload"

// Shared codec tests exercise all representations without coupling the built-in
// protobuf adapter (and Collector components) to the optional Arrow adapter.
type (
	Objects = plogpayload.Objects
	Proto   = plogpayload.Proto
)

const (
	ObjectsFormat = plogpayload.ObjectsFormat
	ProtoFormat   = plogpayload.ProtoFormat
	ArrowFormat   = plogpayload.ArrowFormat
)

var (
	ObjectsCodec    = plogpayload.ObjectsCodec
	ProtoCodec      = plogpayload.ProtoCodec
	NewFromLogs     = plogpayload.NewFromLogs
	NewFromProto    = plogpayload.NewFromProto
	ReadOnlyLogs    = plogpayload.ReadOnlyLogs
	MutableLogs     = plogpayload.MutableLogs
	ProtoBytes      = plogpayload.ProtoBytes
	WriteDebug      = plogpayload.WriteDebug
	PersistentBytes = plogpayload.PersistentBytes
	MarshalView     = plogpayload.MarshalView
	newObjects      = plogpayload.NewObjects
	countProto      = plogpayload.CountProto
)
