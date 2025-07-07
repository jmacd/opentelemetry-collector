# Phase 6: Out-of-Process Fallback with Service Graph Partitioning

## Overview

Phase 6 provides a fallback architecture when direct Go↔️Rust runtime integration (Phases 1-5) cannot be used. Instead of in-process FFI, this phase implements service graph partitioning where the collector spawns a subordinate Rust process and communicates via standard OTLP/gRPC over secure socket pairs.

## Integration with Previous Phases

### Phase 1 Build Process Integration

Phase 6 extends Phase 1's builder configuration with a new `subprocess_mode` option:

```yaml
# Phase 1 + Phase 6: Builder configuration with subprocess fallback
dist:
  name: otelcol-custom
  description: Custom collector with mixed Go/Rust components
  subprocess_mode: auto  # auto | embedded | subprocess
  
receivers:
  - gomod: go.opentelemetry.io/collector/receiver/prometheusreceiver v0.129.0
  - cargo: jaeger_receiver = "0.129.0"  # Phase 1 Rust component specification

processors:
  - gomod: go.opentelemetry.io/collector/processor/batchprocessor v0.129.0  
  - cargo: advanced_filter = "0.129.0"  # Phase 1 Rust component specification

exporters:
  - gomod: go.opentelemetry.io/collector/exporter/otlpexporter v0.129.0
  - cargo: otap_exporter = "0.129.0"    # Phase 1 Rust component specification
```

**Build Mode Selection Logic:**

The builder automatically determines the appropriate mode based on the `subprocess_mode` setting:

- `embedded`: Forces in-process FFI mode (requires CGO)
- `subprocess`: Forces out-of-process mode via OTLP
- `auto`: Automatically detects the best mode based on build constraints (uses subprocess mode when `CGO_ENABLED=0`)

### Phase 5 Context Propagation via OTLP

Phase 6 leverages Phase 5's context propagation patterns through standard OTLP gRPC instead of rust2go FFI:

- **Deadlines**: Automatic via gRPC context deadlines in OTLP calls
- **Cancellation**: Automatic via gRPC stream cancellation
- **Metadata**: Phase 5's `client.Metadata` flows through gRPC headers
- **Tracing**: OpenTelemetry trace context propagates via gRPC metadata
- **Error Classification**: Phase 5's `ProcessingError` → gRPC status codes → Go `consumererror`

**No additional implementation needed:** Standard OTLP receiver/exporter components handle all Phase 5 propagation automatically.

## Core Architecture

### Configuration Example

The system demonstrates transparent configuration handling by automatically partitioning a single user configuration into separate Go and Rust processes when subprocess mode is used.

**User Input Configuration:**

```yaml
# Single configuration works for both embedded and subprocess modes
receivers:
  prometheus:
    scrape_configs:
      - job_name: 'collector'
        static_configs: [targets: ['localhost:8888']]

processors:
  advanced_filter:
    drop_metrics: ["up", "scrape_*"]

exporters:
  otap:
    endpoint: https://backend.example.com:4318

service:
  pipelines:
    metrics:
      receivers: [prometheus]         # Go
      processors: [advanced_filter]   # Go → Rust boundary  
      exporters: [otap]               # Rust
```

**Generated Go Configuration (Parent Process):**

```yaml
# Auto-generated: Go collector processes up to first Rust component
receivers:
  prometheus:
    scrape_configs:
      - job_name: 'collector'
        static_configs: [targets: ['localhost:8888']]

exporters:
  otlp/bridge:  # Single bridge exporter - leverages existing OTLP exporter
    endpoint: unix:///tmp/collector-bridge.sock
    tls:
      insecure: true

service:
  pipelines:
    metrics:
      receivers: [prometheus]
      exporters: [otlp/bridge]
```

**Generated Rust Configuration (Subprocess):**

```yaml
# Auto-generated: Rust collector processes from first Rust component onward
receivers:
  otlp/bridge:  # Single bridge receiver - leverages existing OTLP receiver
    protocols:
      grpc:
        endpoint: unix:///tmp/collector-bridge.sock

processors:
  advanced_filter:
    drop_metrics: ["up", "scrape_*"]

exporters:
  otap:
    endpoint: https://backend.example.com:4318

service:
  pipelines:
    metrics:
      receivers: [otlp/bridge]       # Single bridge routes from prometheus
      processors: [advanced_filter]  
      exporters: [otap]
```

## Service Graph Partitioning

### Partitioning Algorithm

The service graph partitioner automatically analyzes user configuration and splits it into separate Go and Rust sub-graphs:

**Service Graph Analysis:**

The partitioner performs three operations to automatically split mixed Go/Rust configurations:

1. **Component Type Analysis**: Uses Phase 1's component identification where `cargo` fields indicate Rust components, while `gomod` fields or standard collector components are Go components.

2. **Pipeline Dependency Analysis**: Examines `service.pipelines` configuration to build a dependency graph showing data flow between components and identifies boundary points where data crosses from Go to Rust components.

3. **Bridge Insertion**: Inserts OTLP bridge components at boundary points, using a single `otlp/bridge` exporter in the Go configuration and corresponding `otlp/bridge` receiver in the Rust configuration, connected via secure unix sockets.

The result is clean, separate configurations using standard OTLP components that operators already understand, with Rust components in the subprocess using Phase 2's serde-based configuration parsing.

## Process Lifecycle Management

The parent Go collector manages the complete lifecycle of the Rust subprocess using standard process management patterns. During startup, it extracts the embedded Rust binary, generates the partitioned configuration, and spawns the subprocess with proper socket coordination. Graceful shutdown uses SIGTERM with timeout fallback to SIGKILL, while health monitoring provides automatic restart capabilities on failures.

## Build Process Changes

Phase 6 extends Phase 1's builder with minimal changes. The main addition is a `subprocess_mode` field in the distribution configuration supporting three modes: `auto` (automatic detection), `embedded` (force FFI), and `subprocess` (force out-of-process). Auto-detection examines the `CGO_ENABLED` environment variable and build tags to choose the optimal mode.

The builder adapts its behavior based on the selected mode. In embedded mode, it follows Phase 1-5 patterns to generate a single binary with static Rust library linking. In subprocess mode, it builds the Rust collector as a standalone executable, embeds it in the Go binary, and automatically partitions the service graph configuration into separate files for each process. Phase 3's linkme-based factory registration works within the Rust subprocess, while Go uses standard collector factory patterns.

### Binary Packaging

The build process creates a single deployment artifact containing both the Go collector and the embedded Rust binary:

- The Rust collector is built as a standalone executable during the build process
- Go's `embed` directive packages the Rust binary directly into the Go collector executable
- At runtime, the parent collector extracts the embedded binary to a temporary location
- Multiple platform binaries can be embedded for cross-platform distributions
- **Runtime Configuration**: The Rust subprocess uses Phase 4's runtime configuration patterns for executor alongside rust2go settings

## Error Handling and Context Propagation

All Phase 5 features work automatically through standard OTLP:

1. **Context Deadlines**: gRPC context deadlines in OTLP calls
2. **Cancellation**: gRPC stream cancellation propagates across process boundary
3. **Error Classification**: Rust `ProcessingError` → gRPC status → Go `consumererror`
4. **Metadata**: `client.Metadata` flows through gRPC headers
5. **Tracing**: Distributed tracing context via gRPC metadata

Phase 5's structured `ProcessingError` enum maps to gRPC status codes in OTLP responses, which the Go collector translates back to `consumererror` classifications, preserving permanent vs retryable error semantics.

**No additional implementation needed** - standard OTLP receiver/exporter handles all context propagation patterns established in Phase 5.

### Health Monitoring

Health monitoring, logging, and status reporting work seamlessly across process boundaries. The parent collector integrates subprocess logs with its own logging system and exposes combined health status through standard metrics and status endpoints. The system provides graceful degradation when the subprocess is temporarily unavailable.

## Platform Support and Compatibility

### Build Matrix

| Build Mode | Platform | Requirements | Distribution |
|------------|----------|--------------|--------------|
| Embedded | Linux/macOS | CGO + rust2go | Single binary |
| Subprocess | Linux/macOS | Unix sockets | Single binary |
| Subprocess | Windows | Named pipes | Single binary |
| Subprocess | Any | TCP localhost | Single binary |

### User Experience

Configuration remains completely identical between embedded and subprocess modes. Users specify the same YAML configuration regardless of the underlying communication mechanism. The builder automatically chooses the optimal mode based on constraints, and component specifications from Phase 1 require no changes.

The builder supports three operation modes:

- `subprocess_mode: auto` - Builder chooses best mode for platform
- `subprocess_mode: embedded` - Force embedded mode (requires CGO)
- `subprocess_mode: subprocess` - Force subprocess mode

## Performance Characteristics

### Overhead Comparison

| Metric | Embedded (Phases 1-5) | Subprocess (Phase 6) |
|--------|----------------------|---------------------|
| Memory | Lower (shared process) | Higher (separate processes) |
| Latency | Lowest (direct FFI) | Low (unix socket) |
| Throughput | Highest | High (OTLP batching) |
| Startup | Fast | Moderate (process spawn) |
| Complexity | Medium (FFI) | Low (standard OTLP) |

**Use Case Guidelines:**

Choose embedded mode (Phases 1-5) when:

- Maximum performance is required
- Platform supports CGO + rust2go
- Complex FFI integration is acceptable

Choose subprocess mode (Phase 6) when:

- Pure Go builds are required
- Platform has CGO limitations
- Operational simplicity is preferred
- Security isolation between components is needed

## Success Criteria

- [ ] Phase 1's `cargo` specifications work identically in subprocess mode
- [ ] Service graph partitioning correctly identifies Go↔️Rust boundaries
- [ ] Single deployment artifact contains embedded Rust binary
- [ ] Phase 5's context/error propagation works automatically via OTLP
- [ ] Performance overhead is minimal (<15%) compared to native OTLP
- [ ] Pure-Go builds automatically use subprocess mode
- [ ] Configuration remains identical between embedded and subprocess modes
- [ ] Standard collector operational patterns (logging, health checks) work across both modes

This design provides a robust fallback that maintains user experience while leveraging the OpenTelemetry ecosystem's existing OTLP infrastructure. The automatic service graph partitioning ensures mixed Go/Rust pipelines work seamlessly regardless of the underlying communication mechanism.
