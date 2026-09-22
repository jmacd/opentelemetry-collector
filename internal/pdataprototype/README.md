# Pluggable pdata prototype

This unreleased module accompanies the
[Collector v2 RFC](../../docs/rfcs/pluggable-pdata.md). It is an isolated logs
experiment, not a component or a replacement Collector distribution.
Production APIs, factories, and pipeline behavior are unchanged.

`payload` has only standard-library dependencies. It provides explicit codec
registration, immutable shared ownership, lazy cached conversions, and native
same-format merge. `logs` supplies OTLP bytes, read-only pdata, and actual OTAP
Arrow record-group implementations. Arrow/protobuf dependencies are confined to
the adapters, although they are dependencies of this experimental module.

## Run

From this directory, using the repository's Go toolchain:

```sh
go test -race ./...
go test ./logs -run '^$' -fuzz '^FuzzProtoCount$' -fuzztime=10s -parallel=2
go test ./logs -run '^$' -bench '^Benchmark(OTLP|Arrow|Batch)$' \
  -benchmem -benchtime=500ms -count=5 -cpu=1
go tool -modfile ../tools/go.mod golangci-lint run ./...
```

The tests are the runnable integration harness: they exercise bytes/records
through forwarding ownership, batching, lazy conversion, copy-before-mutation,
debug output, and an OTLP byte snapshot/restore boundary. There is no network
server or production queue in this prototype.

Inspect the neutral package's dependency boundary:

```sh
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./payload
```

The only nonblank result should be
`go.opentelemetry.io/collector/internal/pdataprototype/payload`.

## API sketch

```go
arrow := logs.NewArrowCodec()
reg, err := payload.NewRegistry(logs.ObjectsCodec(), logs.ProtoCodec(), arrow.Codec())
// Handle err. Close arrow after encoding has finished.

p, err := logs.NewFromProto(reg, ownedUncompressedOTLPBytes)
// Handle err. On success, p owns the bytes: do not mutate/reuse the input.
// Retain p for each asynchronous owner; Release once for each owner.

wire, err := logs.ProtoBytes(p) // Borrow original bytes, no pdata decode.
objects, err := logs.ReadOnlyLogs(p) // Decode once, cache read-only objects.
mutable, err := logs.MutableLogs(p) // Independent copy for a mutator.
// After mutation, wrap mutable in a NEW payload before encoding it.
// Handle every error; all borrowed values require a live owning reference.
```

The sketch omits error handling and cleanup only to show the API relationships;
the tests contain executable usage with both.

Converters and merge callbacks must return a separately owned representation on
success, and clean up their partial results on error. Native merge preserves all
inputs. A registry cannot mix formats or silently materialize to satisfy merge.
Converters must be deterministic and perform no I/O because failures are cached.

OTLP admission counts resource/scope/log envelopes; it does not validate each
log's contents. A malformed log can be forwarded untouched until object access
reports a decode error. Do not use this as a production validation boundary.

Arrow inputs own real, decoded main/related records. A logical batch preserves
each original group's dictionary and ID namespace. It does not coalesce groups
into one Arrow record or claim to reduce the number of network messages.
Native pass-through bypasses object conversion, not inbound/outbound IPC.
Object-to-Arrow conversion rejects empty resource/scope groups rather than
silently dropping their metadata.
`PersistentBytes` rejects Arrow sources: Arrow persistence is not implemented.

## Benchmarks

[Recorded output](benchmark-results.txt) contains five samples per case. The RFC
reports medians and host/toolchain details.

- `BenchmarkOTLP`: protobuf decode/encode versus envelope scanning and original
  byte forwarding; also measures lazy materialization with unchanged forwarding.
- `BenchmarkArrow`: record-to-pdata-to-record reconstruction versus retaining and
  forwarding actual records; also measures read-only materialization.
- `BenchmarkBatch`: eight pre-admitted 128-log inputs, comparing decode/merge/encode
  against native byte concatenation or retaining independent Arrow groups.

Fixtures are prepared outside timed loops. OTLP paths share immutable input
bytes; Arrow paths acquire equivalent ownership of prebuilt records. Encoders
are reused. These are local allocation/codec experiments, not full Collector or
DFE throughput measurements. Network/IPC/compression, queue scheduling,
backpressure, durable I/O, and hard-limit splitting are not measured.
Timing has no fixed test threshold; round-trip semantics and ownership do.

## Unreleased change log

Initial isolated logs payload prototype and RFC: registered lazy codecs,
OTLP/Arrow native forwarding and logical batching, ownership/error checks, and
reproducible local benchmarks. No production APIs or components change.

This module is excluded from Collector release sets. Its change note is kept
here rather than adding a production release entry with an invented tracking
issue; a published feature will need its own Collector tracking issue and entry.
