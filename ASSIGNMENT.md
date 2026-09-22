We aim to improve OpenTelemetry Collector by implementing pass-through
service for bytes encodings paired with on-demand automatic
encoding/decoding into/from PData objects.

With these changes the Collector will improve performance for simple
tasks that do not require decoding, for example routing, batching,
retry, and persistent queue features, all which can be implemented
without access to signal data.

This feature will be a major version upgrade for the Collector. We
will explore this in the core repository, which fortunately has few
builtin components itself. Within the core repository, the major 
areas that will require our attention are:

- exporterhelper: queue batches the OTLP payload
- storageextension: stores and retrieves OTLP payloads
- debugexporter: prints the OTLP payload

This support can be modeled on the [OTel-Arrow
DFE](https://github.com/open-telemetry/otel-arrow/blob/main/rust/otap-dataflow/README.md)
which includes a dual-format data path supporting OTLP bytes or OTAP
Arrow records. As noted in
https://github.com/open-telemetry/otel-arrow/issues/3452, we see
reason to extend this to arbitrary bytes encodings, so that we can
pass-through arbitrary signal data including syslog, to support
deferred decoding in general through a Codec registration system.

This idea will become mainstream in OpenTelemetry, I think, see for
example the proposal to pass OTLP Profiles version information in 
gRPC/HTTP headers. https://github.com/open-telemetry/opentelemetry-proto/pull/857

As we have compared Collector with DFE, a second consideration is that
we may want pluggable representations in general.

The OTel-Arrow project maintains a Collector-Contrib exporter/receiver
pair that supports dual-format receiver/exporter. We could pass OTAP
arrow-records batches through Collector, so considering use of "any"
specific request the way Exporterhelper does. We could show Collector
acting as an OTAP pass-through, we should be able to do this via a
Codec API for any underlying data that is round-trip compatible with
OTLP.

For example
https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/receiver/otelarrowreceiver/README.md
is the equivalent to OTel-Arrow DFE's `otap:receiver` and likewise
`otelarrowexporter` matches DFE's `otap:exporter`.

Our objective primarily is to write an RFC describing this "v2"
vision, and to make it realistic we need a prototype and some
benchmarks showing how much we save by not decoding/encoding OTLP
objects.

OTel-Arrow DFE implements "Views" APIs for each signal for both its
main Codecs; we can view OTLP bytes or OTAP record-batches through the
same interfaces. The OTLP-to-OTAP and OTAP-to-OTLP converters
translate in both directions using these views, so we see these in use
in the (OTLP, OTAP) x (Exporter, Receiver) for on-demand translation.
https://github.com/open-telemetry/otel-arrow/tree/main/rust/otap-dataflow/crates/pdata-views

Note there are reasons to consider pluggable Arrow-records
representations in addition to pluggable bytes representations,
proposed for flight-recorder and OS-native observability data formats.
https://github.com/open-telemetry/otel-arrow/issues/3875, again
anything that can round-trip to OTLP bytes can be lazily
encoded/decoded.

## Exporterhelper

Exporterhelper already uses an any Request type, essentially the
general-purpose Codec support we need. We can probably elevate this
type instead of inventing a new one, to allow data that passes-through
and converts to the correct signal on-demand.

Since batching support is built-in to exporterhelper, take note that
OTAP and OTLP batching support will be required to fully compare
Collector with DFE. OTAP batching is complex, OTLP batching is simple.
For the prototype we will only implement the logs signal in our
prototype.

## Storage extension

We will not support OTAP-to-bytes, but we may in the future look
forward to WASM integration with OTAP-DFE's `durable_buffer:processor`
as a synchronous processor.
https://github.com/open-telemetry/otel-arrow/tree/main/rust/otap-dataflow/crates/quiver

## Debug exporter

This should be a straightforward application of the Views API.

## Detailed implementation

This repo already uses a `pdatagen` program to generate its protobuf
and JSON code; we can leverage this pattern. Divide the components
into those which use PData internals and the ones that consequently do
not require PData protocol message objects.

See how `pdata/plog/plogotlp` represent an adapater for the protocol
message encoded protocol message; this will be extended and/or modified
to support building OTLP value from bytes, the Marshal/Unmarshal will
be registered as Codecs for retrieving message objects on demand.

Translating from OTLP bytes to `plogotlp.ExportRequest` will use the
underlying protobuf decoder, and so on. OTel-Arrow DFE implements this
(in Rust) using a {Context, Payload}, payload is enum {
OtapArrowRecords, OtlpProtoBytes }, each of those is an enum by signal
type; we can do similar in this code base. See e.g.,
https://github.com/open-telemetry/otel-arrow/blob/main/rust/otap-dataflow/crates/otap/src/pdata.rs

With two considerations:

- Components that do not use OTLP message objects should not take a
  dependency on the generated protobuf impl.
- Existing code should change as little as possible. 

Unclear whether existing packages `plog.Logs`, `pmetric.Metrics` etc
should become generic/pluggable or whether those packages should
remain for the message object users; either way a new set of types
will be needed to represent bytes and/or Arrow-records batches. We will
use the Collector-Contrib repository to determine the best approach.

## Acceptance requirements

The OTel-Arrow DFE has benchmarks comparing itself with Collector. We
will test our modified code which should be able to pass through OTAP
arrow record batches and OTLP bytes batches much better than today's
performance for pipelines that do not require OTLP message objects.


