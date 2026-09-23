# Integrated pluggable logs prototype

This distribution runs the real Collector with native logs payloads enabled by
`service.PluggableLogs`. It accompanies the [v2 RFC](../../docs/rfcs/pluggable-pdata.md).
The feature is experimental and disabled by default.

## Run it

From this directory:

```sh
go run ./cmd/collector --feature-gates=service.PluggableLogs --config=collector.yaml
```

The example listens on loopback ports **14317** (gRPC) and **14318** (HTTP).
It routes protobuf logs by the resource attribute `service.name`. Logs with
`service.name=checkout` go to `debug/checkout`, whose real exporterhelper queue
batches/splits at three records. Other logs go to `debug/other`. Detailed debug
output traverses native views without constructing `plog.Logs`.

Use a normal OTLP protobuf sender. HTTP JSON remains the existing object-codec
ingress path. Stop the Collector normally to drain queued data.

`view_route` is a distribution-local experimental connector. It accepts
`source: resource`, `scope`, or `log`, a string attribute `key`/`value`, and
`match`/`other` output pipeline lists. Nonstring matching attributes are reported
as errors; missing attributes select `other`.

The normal `otlp_http` and `otlp_grpc` exporters are also included. With the gate
enabled, their protobuf path accepts native payloads through the actual service
wrappers, exporterhelper queue, retry sender, and persistent queue.

## Where to read the implementation

| Path | What it demonstrates |
| --- | --- |
| `pdata/xpdata/payload` | Protobuf-free registry, fallible counts, ownership, view interfaces, partitioning, merge/split |
| `pdata/xpdata/plogpayload` | Object adapter and native protobuf validation/views/slicing |
| `pdata/xpdata/plogpayload/otap` | Optional Arrow views, physical coalescing and independently decodable slices |
| `consumer/logs_payload.go` | Native dispatch and explicit legacy mutation boundaries |
| `exporter/debugexporter/logs_payload.go` | Actual debug exporter consuming views |
| `exporter/exporterhelper/internal/queuebatch/payload*.go` | Native requests, sizing, reference ownership and durable encoding |
| `viewrouter/router.go` | Actual connector routing on native attributes |
| `integration_test.go` | Full service-graph exercise over HTTP and gRPC |

Paths outside this distribution directory are relative to the repository root.
The formerly isolated codec packages have been promoted into experimental
`xpdata` packages. Arrow is a separate adapter: the protobuf adapter does not
import it, and the neutral payload package imports only the standard library.

## Verify the behavior

```sh
# Real Collector: HTTP/gRPC ingress, routing, fan-out, debug, HTTP/gRPC exporters,
# hard queue batch limits, and unknown-field preservation end to end.
go test -race ./...

# Native views, counts, physical Arrow merge/split, wire fidelity and ownership.
cd ../../pdata/xpdata
go test -race ./payload ./plogpayload/... ./request
go test ./plogpayload/otap -run '^$' -fuzz '^FuzzProtoCount$' -fuzztime=10s -parallel=2

# Actual debug factory with object decoders deliberately forbidden.
cd ../../exporter/debugexporter
go test -race -run '^TestFactoryNativeDebugNeverMaterializes$' .

# Real queue restart, pool-safe async object ownership and partial retry.
cd ../exporterhelper
go test -race -run 'TestNative|TestPayload' ./...
```

The persistent-queue restart test uses the repository's storage-extension fixture.
The prototype implements a versioned, checksummed context+OTLP envelope and legacy
entry migration; Arrow persistence remains intentionally unsupported.

With the feature enabled, normal/detailed logs debug output uses native
resource/scope/log JSON lines rather than the legacy text layout. Nonfinite
numbers and entity references are handled. Other signals and gate-disabled
behavior are unchanged.

## Reproduce the measurements

From this directory:

```sh
go test . -run '^$' -bench '^BenchmarkCollectorRelay$' \
  -benchmem -benchtime=500ms -count=5 -cpu=1

cd ../../pdata/xpdata
go test ./plogpayload/otap -run '^$' -bench '^Benchmark(OTLP|Arrow|Batch)$' \
  -benchmem -benchtime=500ms -count=5 -cpu=1
```

[Collector results](collector-benchmark-results.txt) compare the same real
128-log HTTP relay with the gate disabled and enabled. Both include two network
hops and the actual queue with `wait_for_result: true`. Setup/teardown is outside
the timed loop; compression and batching are disabled for this comparison.

[Codec results](benchmark-results.txt) include strict known-field protobuf
validation, retained record forwarding, optional object materialization, and
physical Arrow coalescing. Native Arrow coalescing uses a canonical nondictionary
schema, whereas the eager baseline uses the existing adaptive producer.
Arrow IPC/network compression is not included in those microbenchmarks.

The RFC reports medians and caveats. The earlier isolated prototype's cheap
envelope-only scan and grouped-Arrow batch timings are **not** the current
implementation's results.

## Unreleased change log

Expanded the initial isolated experiment into opt-in Collector integration:
native views in debug and attribute routing, fallible predecode counts,
real item/byte splitting and Arrow coalescing, queue/persistence/retry wiring,
pool-safe ownership, and executable service-level examples and measurements.
No stable API replacement or feature enablement by default is proposed.
