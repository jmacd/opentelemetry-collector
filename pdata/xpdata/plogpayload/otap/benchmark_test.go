// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otap

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
)

var benchmarkCount int

func benchmarkRegistry(b *testing.B) (*payload.Registry, *ArrowCodec) {
	b.Helper()
	arrow := NewArrowCodec()
	reg, err := payload.NewRegistry(ObjectsCodec(), ProtoCodec(), arrow.Codec())
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, arrow.Close()) })
	return reg, arrow
}

func BenchmarkOTLP(b *testing.B) {
	for _, size := range []int{1, 128, 1024} {
		for _, mode := range []string{"eager", "passthrough", "materialize"} {
			b.Run(fmt.Sprintf("%d/%s", size, mode), func(b *testing.B) {
				reg, _ := benchmarkRegistry(b)
				buf, err := plogotlp.NewExportRequestFromLogs(fixture(size)).MarshalProto()
				require.NoError(b, err)
				b.ReportAllocs()
				b.SetBytes(int64(len(buf)))
				b.ResetTimer()
				for b.Loop() {
					if mode == "eager" {
						req := plogotlp.NewExportRequest()
						if err := req.UnmarshalProto(buf); err != nil {
							b.Fatal(err)
						}
						out, err := req.MarshalProto()
						if err != nil {
							b.Fatal(err)
						}
						benchmarkCount = req.Logs().LogRecordCount() + len(out)
						continue
					}
					p, err := NewFromProto(reg, buf)
					if err != nil {
						b.Fatal(err)
					}
					if retainErr := p.Retain(); retainErr != nil {
						b.Fatal(retainErr)
					}
					// Model a retained asynchronous consumer ownership, not a
					// network, scheduling, or retry implementation.
					p.Release()
					if mode == "materialize" {
						if _, readErr := ReadOnlyLogs(p); readErr != nil {
							b.Fatal(readErr)
						}
					}
					out, err := ProtoBytes(p)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkCount = mustCount(b, p) + len(out)
					p.Release()
				}
				if benchmarkCount != size+len(buf) {
					b.Fatal("incorrect output")
				}
			})
		}
	}
}

func BenchmarkArrow(b *testing.B) {
	for _, size := range []int{1, 128, 1024} {
		for _, mode := range []string{"eager", "passthrough", "materialize"} {
			b.Run(fmt.Sprintf("%d/%s", size, mode), func(b *testing.B) {
				reg, codec := benchmarkRegistry(b)
				src, err := codec.Codec().Encode(newObjects(fixture(size)))
				require.NoError(b, err)
				defer src.Release()
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					// Both paths acquire equivalent ownership of pre-decoded IPC
					// records. IPC and network work are excluded from both.
					owned, err := mergeRecords([]payload.Representation{src})
					if err != nil {
						b.Fatal(err)
					}
					if mode == "eager" {
						objects, decodeErr := decodeRecords(owned)
						if decodeErr != nil {
							b.Fatal(decodeErr)
						}
						out, encodeErr := codec.Codec().Encode(objects)
						if encodeErr != nil {
							b.Fatal(encodeErr)
						}
						benchmarkCount = mustCount(b, out)
						out.Release()
						objects.Release()
						owned.Release()
						continue
					}
					p, err := NewFromRecords(reg, owned.(*Records).batches)
					if err != nil {
						b.Fatal(err)
					}
					// NewFromRecords now owns these references; owned must not release them.
					if retainErr := p.Retain(); retainErr != nil {
						b.Fatal(retainErr)
					}
					p.Release()
					if mode == "materialize" {
						if _, readErr := ReadOnlyLogs(p); readErr != nil {
							b.Fatal(readErr)
						}
					}
					out, err := p.As(ArrowFormat)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkCount = mustCount(b, out)
					p.Release()
				}
				if benchmarkCount != size {
					b.Fatal("incorrect output")
				}
			})
		}
	}
}

func BenchmarkBatch(b *testing.B) {
	for _, format := range []payload.Format{ProtoFormat, ArrowFormat} {
		for _, mode := range []string{"eager", "native"} {
			b.Run(fmt.Sprintf("%s/%s", format, mode), func(b *testing.B) {
				reg, codec := benchmarkRegistry(b)
				var inputs []*payload.Payload
				for range 8 {
					var rep payload.Representation
					var err error
					objects := newObjects(fixture(128))
					if format == ProtoFormat {
						rep, err = ProtoCodec().Encode(objects)
					} else {
						rep, err = codec.Codec().Encode(objects)
					}
					require.NoError(b, err)
					p, err := reg.New(rep)
					require.NoError(b, err)
					defer p.Release()
					inputs = append(inputs, p)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if mode == "native" {
						p, err := reg.Merge(inputs...)
						if err != nil {
							b.Fatal(err)
						}
						benchmarkCount = mustCount(b, p)
						p.Release()
						continue
					}
					dst := plog.NewLogs()
					for _, input := range inputs {
						rep, err := input.As(format)
						if err != nil {
							b.Fatal(err)
						}
						var decoded plog.Logs
						if format == ProtoFormat {
							req := plogotlp.NewExportRequest()
							err = req.UnmarshalProto(rep.(*Proto).Bytes())
							decoded = req.Logs()
						} else {
							decoded, err = decodeArrow(rep.(*Records))
						}
						if err != nil {
							b.Fatal(err)
						}
						decoded.ResourceLogs().MoveAndAppendTo(dst.ResourceLogs())
					}
					var out payload.Representation
					var err error
					if format == ProtoFormat {
						out, err = ProtoCodec().Encode(newObjects(dst))
					} else {
						out, err = codec.Codec().Encode(newObjects(dst))
					}
					if err != nil {
						b.Fatal(err)
					}
					benchmarkCount = mustCount(b, out)
					out.Release()
				}
				if benchmarkCount != 1024 {
					b.Fatal("incorrect batch output")
				}
			})
		}
	}
}
