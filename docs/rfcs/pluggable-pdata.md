# Pluggable pipeline data for Collector v2

**Status:** Draft with an integrated, opt-in logs prototype. Not an accepted RFC
or a stable API. Enable the prototype with `--feature-gates=service.PluggableLogs`.

## Proposal

Keep `plog.Logs` as the OTLP object API, and add a representation-neutral payload
and read-only views beside it. Pipeline components should request objects only
when they actually need mutable object access. A payload can carry protobuf
bytes, real OTAP Arrow records, or another registered representation.

This branch now implements the necessary operations rather than treating them as
prerequisites for another prototype:

- The real OTLP receiver admits protobuf logs from HTTP and gRPC without building
  `plog.Logs`.
- The actual service graph preserves the native interface through capabilities,
  fan-out, telemetry, reference-management, and connector-router wrappers.
- The real debug exporter reads resource, scope, attribute, and log fields through
  native views; it does not first materialize objects.
- The prototype distribution's `view_route` connector partitions individual logs
  using resource, scope, or log attributes, with native views and native slices.
- The real exporterhelper queue, batcher, retry sender, and persistent queue
  accept native requests. Item and byte limits, partial retry subsets, context
  persistence, and ownership are implemented.
- OTLP HTTP/protobuf and gRPC exporters send retained wire bytes, or transcode
  Arrow through views, without going through the object model.
- Arrow merge physically coalesces main and related records, reconciles input
  schemas/dictionaries, and rebases IDs. Arrow split produces independently
  decodable records, not a hidden selection into the unsplit input.

Existing behavior is the default: the feature gate is alpha and disabled.
Traces, metrics, and profiles are unchanged. Legacy logs components still work
through explicit object adapters. The prototype distribution is a real
`otelcol.Collector`, using the repository's component factories and service graph,
not an imitation pipeline.

See [the runnable example](../../internal/pdataprototype/README.md).

## Package and dependency boundary

| Package | Responsibility |
| --- | --- |
| `pdata/xpdata/payload` | Neutral ownership, codec registry, logs views, native merge/split, attribute partitioning, partial retry ranges |
| `pdata/xpdata/plogpayload` | Object and OTLP protobuf adapters, wire validation/traversal, direct view-to-protobuf marshaling |
| `pdata/xpdata/plogpayload/otap` | Optional real Arrow records, native column views, physical coalescing and ID-correct slicing |
| `consumer` | Native dispatch plus the legacy `plog.Logs` compatibility boundary |
| `exporter/exporterhelper` | Adapter into the existing queue/retry/persistence machinery |
| `internal/pdataprototype` | Runnable distribution, view-routing connector, whole-Collector tests and benchmarks |

The neutral package imports only the standard library. The protobuf adapter
does not import Arrow. Object and Arrow libraries are implementation details of
their adapters; a neutral algorithm needs neither.

Current multi-signal component packages still contain their legacy object APIs.
The prototype demonstrates the package boundary needed for a future
native-only component interface; it does not claim that a binary which supports
legacy components and other signals contains no generated messages.

There is no global codec registration. Registries copy their definitions at
construction and are immutable thereafter. Formats identify signal, representation,
and version. Built-ins are `pdata/logs`, `otlp/protobuf/logs/v1`, and
`otap/arrow/logs/v1`. Additional codecs can supply native views, merge, slice,
size, canonical conversion, and direct representation-to-representation conversion.

## Counts before and after decoding

Predecoded counting is explicitly fallible:

```go
count, err := p.ItemsCount()
if err != nil {
    return err
}
```

Construction does not force a count or a decode. The protobuf implementation
validates/counts wire fields lazily, without allocating message objects. Arrow
uses main-record row counts. The result is cached.

An arbitrary codec may be unable to supply a count until it materializes
objects. A failed predecode count does not prevent that materialization:
successful object decoding replaces the unknown/error count with the known
count. The object API remains infallible:

```go
logs, err := plogpayload.ReadOnlyLogs(p)
if err != nil {
    return err
}
count := logs.LogRecordCount()
```

Exporterhelper's existing `Request.ItemsCount() int` and `BytesSize() int` need
not return placeholder values. `NewPayloadRequest` resolves fallible native
measurements **before queue admission**, then stores known integer measurements.
Errors are returned permanently before any request is enqueued.

The byte sizer measures logical OTLP wire bytes, matching the existing queue's
signal-oriented sizing, not process RSS. Arrow measures its direct view-to-OTLP
encoding. Retained memory and dictionary sharing are different quantities;
this byte sizer must not be advertised as exact heap accounting.

## Native views

`LogsView.Resources` visits resource groups; each group exposes scope groups,
and each scope exposes log records. Scalar fields are read from native storage.
Attribute values and bodies are borrowed `Value` objects with a fallible
`Read()` method. Attribute lookup can read a selected value without decoding
unrelated bodies into an object tree.

The protobuf view walks wire fields and borrows length-delimited data. It handles
resources, scopes, all current log fields, nested values, and resource entity
references. The Arrow view reads columns directly, indexes related attributes by
their parent IDs, and interprets dictionaries and delta IDs. Schema/column
lookups are amortized across rows rather than repeated for every scalar.
Complex Arrow values use their existing CBOR representation; accessing one
decodes that value, not an enclosing `plog.Logs`.

The view interfaces themselves are representation-neutral. There is no
`ReadOnlyLogs` call concealed inside a protobuf or Arrow view. Tests install
codecs whose object decoder returns an error and assert that debug rendering,
attribute routing, slicing, merging, and wire transcoding still succeed.

The actual debug exporter uses these views when the gate is enabled. Basic
verbosity retains its count summary. Normal/detailed native output uses
resource/scope/log JSON lines, with explicit field names and hexadecimal IDs.
This experimental output format differs from the legacy text format. Nested
values, empty groups, nonfinite doubles, and entity references are handled.
The logger receives rendered output only after traversal succeeds, so a decode
failure cannot masquerade as a successful debug export.

The interfaces and logs traversal are handwritten in this prototype. The
production implementation should derive per-signal field definitions and views
from `pdatagen`'s model, rather than independently maintaining another field
catalog for every signal.

## Native routing, batching, and splitting

The runnable `view_route` connector uses `Attributes.Lookup` and
`Payload.Partition`. Both matching and nonmatching records preserve their
representation. A resource/log attribute route is not an implicit conversion
boundary. The connector routes to real downstream Collector pipelines.

### OTLP

The protobuf merger concatenates uncompressed `ExportLogsServiceRequest` bodies:
`resource_logs` is repeated field 1. It does not concatenate gRPC frames,
compressed bodies, or JSON.

Slicing rewrites request/resource/scope envelopes while copying selected
LogRecord wire fields untouched. Resource and scope metadata are preserved,
including empty groups and unknown fields. Empty groups are assigned once,
not duplicated across adjacent splits. Counts are derived from validated input
counts and selected ranges; already-validated records are not revalidated after
every merge.

### Arrow

Same-payload forwarding retains the actual Arrow buffers and dictionaries.
Merging independent inputs physically writes canonical Arrow columns, combines
related attribute records, and rebases resource, scope, log, and attribute-parent
IDs. This is no longer just a list of independent input groups.

The prototype canonical output schema is nondictionary-encoded. Inputs may use
different adaptive schemas/dictionaries. One output group is produced when it
fits; larger results use additional groups at the OTAP uint16 identifier limit.
Unknown columns that the coalescer cannot preserve produce an explicit error.

Splitting borrows/slices ordinary columns and rebuilds the initial delta IDs for
each main-record slice. Related data remains referenced by its original absolute
IDs. Every output slice can be independently decoded by the existing OTel-Arrow
consumer. Keeping unused related attributes in a slice trades memory efficiency
for simpler ownership; views and logical wire sizing expose only referenced
attributes.

### Limits and error behavior

`MergeSplit` handles both item and byte limits. Byte-limited splitting chooses
native record ranges based on their exact logical wire size. Returned requests
meet the bound, and the smallest remainder is last as exporterhelper requires.
A single record or empty metadata envelope larger than the byte limit returns
an error. Inputs remain unchanged and failures return no partially mutated
output. There is no “silently exceed the limit” or object-decoding fallback.

Registries may merge compatible formats from different receivers; codec methods
validate concrete representations. Mixed-format logs select protobuf as their
common representation: Arrow is transcoded through native views and already
materialized objects are encoded normally. Both input orders are tested with
object decoders forbidden. Homogeneous Arrow batches remain Arrow and physically
coalesce; homogeneous protobuf batches retain their wire representation.

These implementations prioritize correctness over optimal selection complexity.
Attribute partitioning currently uses contiguous native slices; highly
fragmented selections can be optimized without changing the view or ownership
contracts.

## Ownership, mutation, queues, and retry

Payloads own their original representation and lazily cached conversions.
Consumers borrow a payload for the call and retain a reference for asynchronous
use. Final release releases all cached representations. Returned views and
values are borrowed for that lifetime.

Canonical objects are marked read-only. Mutating legacy consumers receive an
independent writable copy; they cannot mutate cached objects and then forward
stale bytes. The native-to-legacy adapter owns the copy's lifetime.

Object references use `xpdata/pref`, not merely Go pointer reachability.
Otherwise the existing pdata pooling feature could reset an object still held
by a native queue. Legacy-to-native entry points retain their own pdata
reference, and native-owned canonical objects are marked pipeline-owned.
A test enables `pdata.useProtoPooling`, releases the original producer reference,
and verifies that the asynchronous native consumer still receives the data.

The existing exporterhelper asynchronous queue retains/releases native payloads.
Native merge/split results have separate ownership. The existing batcher releases
them after replacement or final consumption; the original input's queue
reference remains managed by the queue. The same paths cover timer flush,
shutdown flush, and forwarding to the existing retry sender.

`payload.PartialError` identifies ordered, nonoverlapping retry ranges. The
request adapter validates and selects only those records, without objects.
The retry sender owns and releases replacement requests, propagating selection
errors rather than retrying an incorrect whole request. Legacy
`consumererror.Logs` subsets are also adapted. Existing non-native requests keep
their existing error-handler behavior.

Request context remains separate from signal representation. Existing batch
partitioning and context merging continue to apply; users must configure
metadata partitions when different tenants/authentication contexts must not mix.

## Persistence

The storage extension already stores bytes. The changed layer is the
exporterhelper queue encoding, not the storage backend.

The native logs envelope contains a versioned magic header, a bounded context
length, an integrity checksum, the existing selected request-context encoding,
and the original OTLP bytes. Restoring it copies storage-owned bytes and creates
a lazy payload. Counting/validation failures, corrupt envelopes, and unknown
versions return errors. Cancellation and authentication objects are not serialized
as replayable credentials; context fields follow the existing request codec.

Existing object/context and raw OTLP queue entries can be migrated on restore.
Only that old-format migration needs the old object decoder.

Tests exercise the **real persistent queue** across shutdown and restart using
the repository's storage-extension fixture, then batch/split the restored native
request and check its context and contents. This is stronger than only testing
a byte snapshot helper; it is not a filesystem-fsync benchmark.

As specified in the assignment, OTAP-to-bytes persistence is not implemented.
Attempting to persist an Arrow source fails explicitly, even if a network
transcoding result is cached. A [Quiver/WASM][quiver] durable-buffer integration
is a separate storage design.

## Collector integration and compatibility

The OTLP gRPC receiver registers a native logs service descriptor under the same
OTLP method name when enabled. Its request implements the existing pdata gRPC
wire codec contract and copies pooled incoming buffers before retention.
Existing server options, interceptors, limits, and method names remain in use.
The HTTP receiver creates the same payload before protobuf unmarshaling.

Native admission validates known protobuf wire types, nested values, UTF-8,
identifier lengths, and nesting bounds without constructing pdata. Invalid
requests receive `InvalidArgument`/HTTP 400. Unknown protobuf fields remain
opaque and are preserved by forwarding and native splitting.

The HTTP/protobuf exporter uses the original bytes when possible. The gRPC
exporter invokes the existing OTLP method with a wire message, retaining the
existing timeout/retry/transport setup and response/partial-success handling.
HTTP JSON retains its existing object-codec boundary; this branch's byte
pass-through representation is OTLP protobuf.

Consumers may implement `ConsumeLogsPayload` beside `ConsumeLogs`. Compatible
service wrappers preserve this method. A legacy-only component is an explicit
adapter boundary, not a reason to make all earlier components decode.
The prototype router demonstrates actual pipeline integration without changing
Contrib repositories.

Contrib's [Arrow receiver][contrib-receiver] currently calls `LogsFrom`, and its
[exporter][contrib-exporter] accepts `plog.Logs`. Their native entry/exit points
should pass the decoded IPC records to/from the new payload API. Stream-specific
IPC schema/dictionary state still belongs at transport boundaries: arbitrarily
relaying serialized IPC fragments between unrelated streams is not valid.

The original v2 motivation remains: arbitrary byte codecs
([open-telemetry/otel-arrow#3452][arbitrary-bytes]), other Arrow representations
([open-telemetry/otel-arrow#3875][alternative-arrow]), and negotiated profile
versions ([open-telemetry/opentelemetry-proto#857][profile-version]).
DFE's [dual-format payload][dfe-payload] and [native views][dfe-views] are
architectural precedents, not substitutes for a Go implementation.

## Measurements

Linux/amd64, Intel Core Ultra 7 165H, Go 1.26.7, `-cpu=1`, 500 ms per sample,
five samples per case. Numbers below are rounded medians; CPU affinity/frequency
and unrelated host activity were not controlled. OTel-Arrow Go is pinned at
`v0.56.0`, Arrow Go at `v18.6.0`, with local Collector modules.

The first prototype's much lower OTLP scan cost and cheap grouped-Arrow batch
numbers no longer describe this implementation. These measurements include
known-field wire validation and physical Arrow coalescing.

### Real Collector relay

128 logs/request, two HTTP hops, the actual service graph and exporterhelper
queue, `wait_for_result: true`, no compression or batching. Both modes use the
same distribution, fixture, endpoints, and settings; only the feature gate changes.

| Mode | ns/request | B/request allocated | allocations/request |
| --- | ---: | ---: | ---: |
| Existing eager Collector | 205,898 | 126,201 | 1,594 |
| Native payload Collector | 183,598 | 67,863 | 302 |

This run shows about 11% lower request time, 46% fewer allocated bytes, and 81%
fewer allocations. It is a sequential loopback relay, not a saturation or
production throughput result. The backend drains bytes; separate correctness
tests decode and validate deliveries. Latency varied materially between runs on
this shared, unpinned host; the allocation reduction is more repeatable.

### Local representation operations

| Operation | Eager ns/op | Native ns/op | Eager B/op | Native B/op | Eager/native allocations |
| --- | ---: | ---: | ---: | ---: | ---: |
| OTLP forwarding, 128 logs | 118,630 | 54,052 | 102,104 | 608 | 2,443 / 4 |
| OTLP forwarding, 1024 logs | 907,927 | 399,499 | 812,248 | 608 | 19,249 / 4 |
| Arrow forwarding, 128 logs | 728,814 | 518 | 440,941 | 632 | 10,227 / 6 |
| Arrow forwarding, 1024 logs | 5,237,912 | 489 | 3,172,167 | 632 | 77,096 / 6 |
| OTLP merge, 8 x 128 logs | 906,185 | 25,485 | 817,161 | 164,576 | 19,544 / 6 |
| Arrow coalescing, 8 x 128 logs | 5,796,319 | 5,384,221 | 3,183,563 | 3,198,435 | 77,156 / 76,986 |

Arrow forwarding starts after IPC decoding and ends before IPC encoding. Its
large microbenchmark ratio must not be used as a Collector throughput multiplier.
Arrow coalescing is real work: the native implementation is modestly faster in
this run, with approximately the same allocation volume, not allocation-free.
Its canonical nondictionary output also differs physically from the adaptive
producer baseline; network/compression costs are not compared here.

Read-only materialization cases are included in the raw output. They demonstrate
that requesting objects still costs time and memory: OTLP materialization can be
slower than eager processing after accounting for validation and payload overhead.
This design does not claim every pipeline becomes faster.

[Codec results](../../internal/pdataprototype/benchmark-results.txt),
[Collector results](../../internal/pdataprototype/collector-benchmark-results.txt),
and [reproduction commands](../../internal/pdataprototype/README.md) are included.

## Evidence and next architectural decisions

The integration test starts a real Collector with HTTP and gRPC ingress, the
native attribute-routing connector, fan-out to debug and both OTLP exporters,
and item-limited queues. It checks delivered counts, hard limits, actual native
debug output, and unknown-field preservation across the complete path. Preserving
an unknown wire field is an independent check that forwarding did not secretly
decode/re-encode pdata.

Additional tests forbid object conversion during actual debug-factory calls for
both protobuf and real Arrow input, compare native views against the object
model, decode physically coalesced/split Arrow records with the existing consumer,
exercise malformed data and count transitions, test byte bounds and atomic
failures, verify pool-safe asynchronous ownership, persist/replay requests,
and retry only selected native records.

A full DFE-versus-Collector comparison has not been run. DFE's
[performance harness][perf-harness] provides matched OTLP/OTAP pipeline templates.
Normalize acknowledgment mode explicitly: the inspected Collector template
defaults `wait_for_result` true while the DFE OTAP template defaults false.
Pin datasets/builds and match cores, queues, batching, compression, TLS/auth,
concurrency, retries, and delivery guarantees. Report CPU/log, delivered logs/s,
RSS, latency distributions, and semantic output validation at multiple loads.

The remaining architectural decisions are about stabilizing the public
signal/view APIs, generated implementations, retained-memory accounting/cache
policy, native-only factories, other signals/codecs, and rollout. Native debug
views, attribute routing, fallible predecoded counts, and actual merge/split are
implemented here; they are not deferred requirements.

[contrib-receiver]: https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/5185014bb71caedc116322fb7290c76cf3673dd4/receiver/otelarrowreceiver/internal/arrow/arrow.go
[contrib-exporter]: https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/5185014bb71caedc116322fb7290c76cf3673dd4/exporter/otelarrowexporter/otelarrow.go
[dfe-payload]: https://github.com/open-telemetry/otel-arrow/blob/bcb2362ee6ede4b31ce29bb7b59c033c5292084a/rust/otap-dataflow/crates/pdata/src/payload.rs
[dfe-views]: https://github.com/open-telemetry/otel-arrow/tree/bcb2362ee6ede4b31ce29bb7b59c033c5292084a/rust/otap-dataflow/crates/pdata-views
[perf-harness]: https://github.com/open-telemetry/otel-arrow/tree/bcb2362ee6ede4b31ce29bb7b59c033c5292084a/tools/pipeline_perf_test
[arbitrary-bytes]: https://github.com/open-telemetry/otel-arrow/issues/3452
[alternative-arrow]: https://github.com/open-telemetry/otel-arrow/issues/3875
[profile-version]: https://github.com/open-telemetry/opentelemetry-proto/pull/857
[quiver]: https://github.com/open-telemetry/otel-arrow/tree/bcb2362ee6ede4b31ce29bb7b59c033c5292084a/rust/otap-dataflow/crates/quiver
