# Pluggable pipeline data for Collector v2

**Status:** Draft for discussion, with an isolated logs-only prototype. This is
not an accepted RFC, a production feature, or a proposal to change OTLP.

## Summary and decision

Introduce a representation-neutral pipeline payload alongside the existing
object APIs. Keep `plog.Logs`, `pmetric.Metrics`, and related packages as the
mutable OTLP object model. Move the obligation to construct those objects from
every receiver to the first component that needs them.

A payload can own OTLP protobuf bytes, OTAP Arrow record groups, or another
registered representation. Routing by request metadata, whole-request retry,
queueing, and supported native batching operate without constructing pdata
objects. A codec registry materializes another representation on demand.
Generated protobuf implementations and Arrow libraries belong behind adapters,
not in representation-neutral components.

Changing the pipeline-wide consumer contract is a major-version project. The
accompanying [prototype](../../internal/pdataprototype/README.md) deliberately
does **not** change stable APIs, component factories, service graphs, transport
implementations, or the default Collector binary. Its module is excluded from
release sets and is not a Builder component.

Approval of the architectural direction would authorize staged experimental
work, not stabilize the prototype API, promise a release date, or assert that
native Go views and full Collector/DFE parity already exist.

## Motivation and evidence from existing implementations

Today, `consumer.Logs.ConsumeLogs` takes `plog.Logs`. The OTLP receiver therefore
receives already-decoded objects, and `plogotlp.ExportRequest` wraps those same
objects. The exporterhelper request abstraction comes too late to avoid the
receiver-side decode.

Exporterhelper already provides most of the relevant *operations*: an opaque
request, item/byte sizing, merge/split, partial-error handling, reference
counting, and persistence encoding. It is not literally an unconstrained `any`:
its current `Request` interface requires `ItemsCount`, `BytesSize`, and
`MergeSplit`. Its custom logs exporter still accepts a converter from
`plog.Logs`. Elevating the operations and payload boundary is preferable to
creating a second independent queue/retry framework.

The [Contrib Arrow receiver][contrib-receiver] calls `LogsFrom` to construct
`plog.Logs`, then calls `ConsumeLogs`. The [Arrow exporter][contrib-exporter]
receives `plog.Logs` and constructs Arrow data again. A pipeline with no
object-level processing still pays for that round trip.

The [DFE payload][dfe-payload] owns either OTLP bytes or OTAP records and caches
measurements. Its request context is separate from the payload. Its
[pdata views][dfe-views] support accessing those representations without a
mandatory object-model conversion. These are architectural precedents, not
evidence that the same performance automatically transfers to Go.

Relevant explorations include arbitrary byte codecs in
[open-telemetry/otel-arrow#3452][arbitrary-bytes], alternative Arrow formats in
[open-telemetry/otel-arrow#3875][alternative-arrow], and profile version
negotiation in
[open-telemetry/opentelemetry-proto#857][profile-version].

## Scope

The v2 direction covers all signals and registered byte/columnar formats.
Implementation here covers logs, with three representations:

| Representation | Contents | Role |
| --- | --- | --- |
| `pdata/logs` | Read-only `plog.Logs` | Canonical conversion hub and legacy adapter |
| `otlp/protobuf/logs/v1` | Uncompressed `ExportLogsServiceRequest` bytes | Byte-preserving forwarding and concatenation |
| `otap/arrow/logs/v1` | Real main/related Arrow records, grouped by original batch | Columnar forwarding and grouped batching |

The format strings are prototype identifiers, not standardized media types.
Production identifiers must distinguish signal, encoding, and schema/protocol
version. Compression and transport framing are separate concerns.

Out of scope for this iteration: a production receiver/exporter, service graph
wiring, transport benchmarks, generated views, hard-limit splitting, native
Arrow coalescing, a durable queue envelope, Arrow persistence, a syslog codec,
and a WASM/Quiver integration. The full DFE comparison is a subsequent milestone.

## Proposed architecture

### Payload and codecs

The prototype's `payload` package has only standard-library dependencies. Its
representation interface exposes format, item count, and lifetime management;
the concrete bytes, objects, or Arrow records remain in the adapter package.
This is analogous to exporterhelper's custom requests without importing an
exporter helper into receivers and processors.

A distribution constructs an immutable registry before serving traffic. Each
noncanonical codec registers conversion to/from the canonical object format,
plus optional native merge. Registration rejects duplicate and incomplete
codecs. Registration is explicit rather than via global `init` side effects.
No automatic package discovery or arbitrary plugin loading is proposed.

`Payload.As(format)` returns the original representation directly when possible.
Otherwise it lazily converts through the canonical format and caches successful
results and deterministic errors for that payload's lifetime. Conversions are
serialized per payload; a codec may be invoked concurrently for different
payloads. The prototype Arrow encoder serializes its reusable builder.

This small conversion hub is intentionally not a graph search framework.
Direct OTLP-to-OTAP conversion through native views can be added as a fast path
without making object conversion mandatory for ordinary forwarding. Codec
version/fidelity checks must reject conversions that cannot represent the
source; format compatibility must not be inferred from “both are logs.”
For example, the prototype rejects encoding empty resource/scope groups into
Arrow rather than silently losing their metadata in a row-based representation.

Representation-neutral consumers should eventually receive a signal-typed
payload with request context:

```go
// Illustrative v2 contract, not a stable API added by this prototype.
ConsumeLogs(context.Context, *LogsPayload) error
```

The prototype specializes format identifiers to logs rather than choosing a
final Go generics design for every signal. It demonstrates the dependency
boundary and operations, not the final names.

### Ownership, immutability, and mutation

A newly constructed payload has one owner. Native data ownership transfers only
on successful construction. Borrowed transport buffers must be copied or retained
before transfer; passing a buffer that the transport will reuse is invalid.

An asynchronous queue or fan-out branch retains a payload and releases exactly
one reference after its last use. Final release releases all cached
representations. Arrow references retain the actual buffers and dictionaries,
not just Go pointers. The tests check that merged records outlive both their
source payloads and the encoder.

Representations returned by `As` are borrowed and immutable. Read-only
`plog.Logs` enforce this with `MarkReadOnly`; Go byte slices and Arrow message
wrappers rely on the documented borrowing contract. A public API may choose
less permissive accessors than the prototype.

A mutator obtains an independent writable object copy and constructs a new
payload after mutation. It must **not** mutate a cached object and then reuse the
old bytes or records. The prototype's `MutableLogs` models this conservative
boundary. Unique-owner mutation/encoding-cache invalidation is a possible later
optimization, not a prerequisite.

Retain/borrow-after-release returns an error. Double release is a programming
error and panics. Invalid wire data returns errors, not lifetime panics.

### Context, routing, counts, and sizing

Context remains independent of the representation: cancellation, request
metadata, authorization, and retry state do not belong in signal data.
Conversions must not change it. Merge is legal only after the existing batching
partitioner has established compatible routing/authentication/metadata context.
The prototype has no context partitioner and does not model cross-request
context merging.

OTLP item counting scans the request/resource/scope message envelopes, without
decoding log records, attribute maps, or bodies. It is O(number of envelopes),
not O(1). Invalid envelope lengths/wire types fail admission. Arrow counts come
from main-record row counts. Counts are checked after conversion and merge.

This scan does **not** validate fields within every LogRecord or every nested
attribute. A structurally countable request can fail materialization later;
there is an explicit test for this. A production receiver must specify its
validation contract: strict validation at admission, or clearly documented
deferred validation with permanent-error propagation. Avoiding object
allocation does not justify silently changing error/acknowledgment semantics.

Representation size is not one universal number. Production APIs need separate
wire size, retained-memory size, item count, and request count. Arrow retained
buffers, dictionary sharing, and slices make an OTLP `BytesSize` estimate a poor
memory limit. Converted caches increase live memory; the prototype retains all
conversions until final release and does not implement a memory limiter.

### Batching and exporterhelper

OTLP `ExportLogsServiceRequest` currently has repeated `resource_logs` at field
1. Concatenating **uncompressed protobuf message bodies** merges these lists
without decoding. This does not concatenate gRPC frames, compressed bodies, or
JSON. Unknown wire fields survive the pass-through path and native byte merge;
object conversion has the current decoder's unknown-field/migration behavior.
Future protocol versions with different merge semantics require different codecs.

Arrow batching retains a list of independent main/related record groups. It
does not concatenate columns whose resource/attribute IDs happen to have the
same numeric values. Each group is decoded with its own related-data stores.
This avoids ID collisions and retains dictionaries without rewriting columns.
It is a logical batch, **not** proof of a larger single outbound Arrow batch or
fewer network requests. Physical coalescing must rebase IDs and reconcile schemas
and dictionaries.

The prototype supports same-format native merge only. Mixed formats, registries,
and unsupported operations return errors. It preserves caller-owned inputs.
Its eager batching baseline moves decoded resource logs into the output rather
than charging the baseline an unnecessary deep copy.

Production integration should extract or elevate exporterhelper's neutral
request operations and adapt the existing queue/retry/timeout/telemetry machinery.
It should not fork that machinery. Important differences to resolve:

- `ItemsCount()` cannot return decode errors: establish a valid count before
  admission, or revise that contract.
- Current `MergeSplit` has hard-size and output-order requirements. Whole-group
  batching alone cannot satisfy arbitrary item/byte splits. Implement native
  splitting, explicitly opt into object fallback, or reject incompatible
  configuration at startup; never quietly exceed the configured limit.
- Partial retry errors must identify the retained subset or carry a new
  representation-neutral payload. Retrying the complete input when only a
  subset is eligible can duplicate accepted data.
- Native no-op routing can use request context. Routing on resource/log
  attributes still requires a native view or object materialization.

### Persistence and storage extensions

The storage extension already stores `[]byte`; it is not the layer that chooses
how pdata is serialized. The current exporterhelper logs encoding uses
`pdata/xpdata/request` to encode data together with selected request context.
That encoding and its restore path need an adapter; a storage backend need not
import generated OTLP messages.

A future durable envelope needs an explicit format/version, payload length,
supported context fields, and integrity/framing validation. Restore must preserve
byte ownership and leave payload decoding lazy. Unknown codecs/versions and
corrupt entries must fail visibly; they must not be counted as delivered.
Old queue entries need an explicit upgrade/drain or compatibility strategy.
Process-local cancellation and authentication objects must not be blindly
serialized and replayed as current credentials.

The prototype's `PersistentBytes` only supplies borrowed OTLP bytes. Its tests
copy those bytes before restoring them and verify byte identity. It is neither a
queue envelope nor a crash-recovery implementation. It explicitly rejects Arrow
source payloads, even when a network conversion to OTLP has been cached.

Arrow-to-bytes persistence is not part of this work. A future synchronous WASM
adapter to DFE's [Quiver durable buffer][quiver] is a separate investigation with
its own memory, ownership, failure, and acknowledgment contract.

### Debug exporter, views, and code generation

Basic counts can use the neutral payload metadata. Detailed debug output needs
signal fields. Initially, an adapter can materialize `plog.Logs` on demand and
reuse the existing debug marshalers. The prototype demonstrates this boundary
with `WriteDebug`, returning decode and writer errors; it does not modify the
production debug exporter or promise identical debug output formatting.

The longer-term design is a generated read-only views API implemented for
objects, OTLP wire data, and OTAP records. Native views could let detailed debug
and read-only processors avoid complete object materialization. The Rust DFE
views are prior art, not a Go implementation included here.

Use `pdatagen`'s existing model as the source for object adapters, signal-specific
view contracts, field traversal, and potentially wire counting. Generated
protobuf/object implementations stay in object/codec packages. Neutral queues,
metadata routers, and service wiring must not depend on them. The prototype's
small logs-envelope scanner is handwritten; copying it separately for every
signal is not the proposed production maintenance strategy.

## Transport and Contrib migration

OTLP gRPC currently decodes into `plogotlp.ExportRequest` before the receiver
handler runs. Changing only exporterhelper cannot eliminate that decode. An
experimental transport path must capture the uncompressed request bytes at the
gRPC codec boundary, or before HTTP's protobuf unmarshaler, while retaining size
limits, authorization, status mapping, partial success, and reference ownership.
HTTP JSON requires a separate codec and merge strategy.

For Contrib Arrow, use the lower-level consumer operation that produces real
records, rather than `LogsFrom`. Preserve records across the pipeline and
serialize them into the outbound stream without a pdata round trip. Arrow IPC
dictionary/schema state is stream-specific: forwarding serialized IPC fragments
from one stream into another is not generally valid. Native forwarding still
does IPC work at both transport boundaries.

During migration, adapters surround legacy consumers/processors. Only
object-dependent components materialize. Mutating branches must obtain isolated
copies; fan-out to neutral branches should retain the original format.
Capability declarations should identify native formats, read-only views,
mutation, merge/split, and durable encoding support. Pipeline construction should
reject unsupported combinations rather than discovering them after buffering.

Classify and migrate surfaces separately:

| Surface | Neutral work | Work that still needs signal access |
| --- | --- | --- |
| Receivers/exporters | Byte/record ownership and transport | Validation, conversion, partial success |
| exporterhelper | Queue, retry, timeout, request-context partitioning | Native codecs, sizing, split, partial retry |
| Storage extension | Store/retrieve envelope bytes | Queue-level envelope codec/upgrade logic |
| Service/fan-out/connectors | Context and retained payload references | Mutating-branch copies and legacy adapters |
| Batch processors | Supported native merge | Splits and mixed-format policy |
| Debug exporter/processors | Basic metadata | Native views or lazy objects |
| Memory limiter/telemetry | Request and item accounting | Accurate retained-buffer/cache accounting |

The final package/module split should ensure that importing a neutral component
does not pull in `pdata/internal` or any generated OTLP implementation. Keeping
`plog.Logs` unchanged avoids forcing the much larger Contrib object-processing
ecosystem to rewrite every field accessor.

## Prototype and measurements

The prototype uses the local Collector pdata implementation at base commit
`5626c6b5127d5ac8a45d55913a6bb1f07739f0b9`, OTel-Arrow Go `v0.56.0`, and Arrow Go
`v18.6.0`. It uses actual logs builders and main/related Arrow records, not a
stand-in Arrow struct. The encoder uses the existing exported builder APIs;
production should establish a supported records-only producer API upstream.

On this run: Linux/amd64, Intel Core Ultra 7 165H, Go 1.26.7, `-cpu=1`,
500 ms per sample, five samples per case. Results below are rounded medians, not confidence
intervals. CPU affinity/frequency and other host activity were not controlled.
[Raw output](../../internal/pdataprototype/benchmark-results.txt) and
[reproduction commands](../../internal/pdataprototype/README.md) are included.

The generated fixture includes resource/scope metadata, attributes with nested
values, string/map/bytes/integer bodies, timestamps, IDs, severity, flags, event
names, and dropped-attribute counts. It is a synthetic workload, not a claim
about a production distribution of log sizes.

### Request paths

| Format / logs | Eager ns/op | Pass-through ns/op | Materialize ns/op | Eager B/op | Pass-through B/op | Eager / pass allocations |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| OTLP / 1 | 1,677 | 390 | 2,087 | 1,448 | 560 | 34 / 4 |
| OTLP / 128 | 121,457 | 1,277 | 108,223 | 102,104 | 560 | 2,443 / 4 |
| OTLP / 1024 | 988,885 | 8,298 | 907,920 | 812,248 | 560 | 19,249 / 4 |
| Arrow / 1 | 62,132 | 516 | 6,243 | 54,531 | 616 | 710 / 6 |
| Arrow / 128 | 783,592 | 532 | 355,880 | 440,941 | 616 | 10,227 / 6 |
| Arrow / 1024 | 6,280,571 | 561 | 2,877,384 | 3,172,168 | 616 | 77,096 / 6 |

OTLP eager decodes and re-encodes a message. Pass-through includes envelope
scanning, a new payload, retain/release handoff accounting, and retrieval of the
original bytes. Both borrow an immutable prebuilt input fixture; neither pays
for transport ownership copies. Materialize also constructs objects but still
forwards the original representation, modeling read-only processing.

Arrow eager converts records to objects and reconstructs records using a reused
encoder. Both paths start after IPC decoding and acquire equivalent record
ownership from a retained fixture. Pass-through creates a payload and retrieves
the same records. It does not traverse every row or serialize IPC. Materialize
decodes objects but forwards the unchanged records.

Avoiding the codec round trip greatly reduces these costs. This is **not** an
equivalent multiplier for Collector throughput: network, IPC, compression, auth,
scheduling, queue contention, acknowledgments, and storage are absent. The small
OTLP materialization case is slower than eager processing, exposing the payload
machinery's overhead rather than implying every pipeline improves.

### Batching eight 128-log inputs

| Format | Eager ns/op | Native ns/op | Eager B/op | Native B/op | Eager / native allocations |
| --- | ---: | ---: | ---: | ---: | ---: |
| OTLP | 1,103,527 | 31,192 | 817,129 | 164,528 | 19,544 / 6 |
| Arrow | 5,975,465 | 1,338 | 3,183,564 | 1,048 | 77,156 / 9 |

Inputs are already admitted; native batching does not repeat the ingress count
scan. Eager batching decodes all eight inputs, moves their resource logs, and
encodes the result. OTLP native merge copies serialized bytes into one output
message. Arrow native merge retains eight independent groups, whereas eager
produces one reconstructed group. These are logically equivalent log contents,
but **different physical/network batching outcomes**. This benchmark does not
establish the cost of Arrow ID/schema/dictionary coalescing.

Correctness checks cover populated-field round trips, unknown OTLP fields on
byte-preserving paths, native batch content/counts, independent Arrow ID
namespaces, copy-before-mutation, conversion caching/errors, malformed envelopes,
deferred malformed-record errors, unsupported persistence, writer errors,
empty-group fidelity rejection, concurrent conversion, and Arrow buffer release.
The neutral package's dependency list contains only itself and standard-library
packages. Timing is not asserted in tests.

## End-to-end acceptance: subsequent work

The assignment's original full-system acceptance criterion is **not yet met**.
This iteration establishes the architecture and local codec savings, following
the agreed isolated-prototype scope.

Use DFE's [pipeline performance harness][perf-harness], especially the
`comparison_dashboard` Collector and engine OTLP/OTAP pass-through templates, for
the integrated milestone. Pin both revisions, Collector distributions, datasets,
and container/build settings. Compare unmodified Collector, the experimental
Collector, and DFE, not these Go microbenchmarks against published Rust numbers.

The current templates differ in acknowledgment defaults: Collector
`wait_for_result` defaults true while the DFE OTAP template defaults false.
Normalize this explicitly, along with channel/queue capacity, concurrency,
compression, batch thresholds, retries, TLS/auth, and core affinity. Compare
like-for-like resource budgets and report actual CPU usage rather than treating
Go's `GOMAXPROCS` as equivalent to every DFE thread configuration.

Run OTLP-to-OTLP and OTAP-to-OTAP, with and without batching; then add read-only
processing, mutation, retry/failure, fan-out, and OTLP durable-queue scenarios.
Use logs per second, CPU time per delivered log, allocations, retained/RSS memory,
latency distributions, accepted/delivered/dropped counts, and backend semantic
validation. Verify zero unwanted object conversions with instrumentation, and
verify native record splitting/coalescing separately from logical batching.
Repeat at multiple batch sizes and offered loads with identical delivery
guarantees. Record confidence/variance, not only the best result.

Before a production proposal, also require malformed/oversized/decompression
tests, cancellation and shutdown/drain behavior, backpressure, partial failures,
memory limits under cached multi-format fan-out, durable replay/upgrade behavior,
and a compatibility inventory for Contrib components. Logs success alone does
not establish metric/traces/profiles correctness.

## Alternatives and unresolved decisions

Making `plog.Logs` itself pluggable preserves the consumer signature, but couples
neutral users to object packages and leaves no error channel on ordinary field
accessors for a failed deferred decode. It also complicates mutation and
reference semantics. Keeping object types and using explicit adapters makes
those boundaries visible.

A bytes-only fast path is smaller but cannot carry real Arrow records or
alternative in-memory formats. An unrestricted `any` payload without a lifetime,
count, and capability contract moves type/ownership mistakes into components.
A global conversion graph adds policy and path-selection complexity not needed
to test the central hypothesis.

Open design questions include the final package/generic signal layout, native
views' traversal and error APIs, retained-memory accounting and cache policy,
whether converters may be lossy with explicit opt-in, exact native split
contracts, protocol/schema negotiation, and staged legacy adapters. These need
maintainer agreement before stable Collector interfaces change.

[contrib-receiver]: https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/5185014bb71caedc116322fb7290c76cf3673dd4/receiver/otelarrowreceiver/internal/arrow/arrow.go
[contrib-exporter]: https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/5185014bb71caedc116322fb7290c76cf3673dd4/exporter/otelarrowexporter/otelarrow.go
[dfe-payload]: https://github.com/open-telemetry/otel-arrow/blob/bcb2362ee6ede4b31ce29bb7b59c033c5292084a/rust/otap-dataflow/crates/pdata/src/payload.rs
[dfe-views]: https://github.com/open-telemetry/otel-arrow/tree/bcb2362ee6ede4b31ce29bb7b59c033c5292084a/rust/otap-dataflow/crates/pdata-views
[perf-harness]: https://github.com/open-telemetry/otel-arrow/tree/bcb2362ee6ede4b31ce29bb7b59c033c5292084a/tools/pipeline_perf_test
[arbitrary-bytes]: https://github.com/open-telemetry/otel-arrow/issues/3452
[alternative-arrow]: https://github.com/open-telemetry/otel-arrow/issues/3875
[profile-version]: https://github.com/open-telemetry/opentelemetry-proto/pull/857
[quiver]: https://github.com/open-telemetry/otel-arrow/tree/bcb2362ee6ede4b31ce29bb7b59c033c5292084a/rust/otap-dataflow/crates/quiver
