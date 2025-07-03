# Phase 6: Out-of-Process Fallback with Service Graph Partitioning

## Overview

Phase 6 provides a fallback architecture for scenarios where direct Go↔️Rust runtime integration (Phases 1-5) cannot be used. Instead of in-process FFI, this phase implements service graph partitioning where the collector spawns a subordinate Rust process and communicates via standard OTLP/gRPC over secure socketpairs.

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

```go
// Phase 6 extends Phase 1's builder with mode detection
func (d *Distribution) determineBuildMode() BuildMode {
    switch d.SubprocessMode {
    case "embedded":
        return ModeEmbedded
    case "subprocess": 
        return ModeSubprocess
    case "auto":
        // Auto-detect based on build constraints, etc.
        if os.Getenv("CGO_ENABLED") == "0" {
            return ModeSubprocess
        }
        return ModeEmbedded
    default:
        return ModeEmbedded
    }
}
```

### Phase 5 Context Propagation via OTLP

Phase 6 leverages Phase 5's context propagation patterns, but through standard OTLP gRPC instead of rust2go FFI:

- **Deadlines**: Automatic via gRPC context deadlines in OTLP calls
- **Cancellation**: Automatic via gRPC stream cancellation  
- **Metadata**: Phase 5's `client.Metadata` flows through gRPC headers
- **Tracing**: OpenTelemetry trace context propagates via gRPC metadata
- **Error Classification**: Phase 5's `ProcessingError` → gRPC status codes → Go `consumererror`

**No Additional Implementation Needed:** Standard OTLP receiver/exporter components handle all Phase 5 propagation automatically.

## Core Architecture

### Configuration Example: Input and Partitioned Output

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

The service graph partitioner automatically analyzes the user's configuration and splits it into separate Go and Rust sub-graphs:

**Component Type Analysis:**

- Uses Phase 1's component identification: components with `cargo` field are Rust components
- All other components (with `gomod` field or standard collector components) are Go components
- Creates a mapping of component names to their runtime (Go vs Rust)

**Pipeline Dependency Analysis:**

- Examines the `service.pipelines` configuration to understand data flow
- Builds a dependency graph showing how data flows between components
- Identifies the specific edges where data crosses from Go components to Rust components

**Bridge Insertion:**

- Inserts a single `otlp/bridge` exporter in the Go configuration at boundary points
- Inserts a corresponding `otlp/bridge` receiver in the Rust configuration
- Uses a private unix socket for secure inter-process communication
- Redirects pipeline connections to route through the bridge components

**Configuration Generation:**

- Produces a clean Go configuration with only Go components plus the bridge exporter
- Produces a clean Rust configuration with only Rust components plus the bridge receiver
- Both configurations use standard OTLP components that operators already understand
- **Phase 2 Integration**: Rust components in the subprocess use serde-based configuration parsing established in Phase 2

## Process Lifecycle Management

### Subprocess Management

The parent Go collector manages the Rust subprocess lifecycle through standard process management:

**Startup Process:**

- Extract the embedded Rust binary to a temporary location
- Generate the partitioned Rust configuration file
- Spawn the Rust collector subprocess with the configuration
- Wait for the unix socket to become available (with timeout)
- Integrate subprocess logs with the parent collector's logging system

**Graceful Shutdown:**

- Send SIGTERM to the Rust subprocess for graceful shutdown
- Wait for the process to exit cleanly (with timeout)
- If timeout expires, send SIGKILL to force termination
- Clean up temporary files and socket paths

**Health Monitoring:**

- Periodic health checks via gRPC health service on the unix socket
- Automatic restart on health check failures
- OTLP exporter's built-in retry logic handles temporary connection issues

## Build Process Changes

### Phase 1 Builder Extensions

Phase 6 extends Phase 1's builder with minimal changes to support subprocess mode:

**Configuration Extensions:**

- Adds a `subprocess_mode` field to the distribution configuration
- Supports three modes: `auto` (detect automatically), `embedded` (force FFI), `subprocess` (force out-of-process)
- Auto-detection checks the `CGO_ENABLED` environment variable and build tags

**Build Process Modifications:**

- **Embedded Mode**: Uses Phase 1-5 patterns to generate a single binary with static Rust library linking
- **Subprocess Mode**: Builds the Rust collector as a standalone executable and embeds it in the Go binary
- The builder automatically partitions the service graph configuration when in subprocess mode
- Generates separate configuration files for the Go parent and Rust subprocess
- **Factory Integration**: Phase 3's linkme-based factory registration works within the Rust subprocess, while Go uses standard collector factory patterns

### Binary Packaging

**Embedded Binary Strategy:**

- The Rust collector is built as a standalone executable during the build process
- Go's `embed` directive packages the Rust binary directly into the Go collector executable
- At runtime, the parent collector extracts the embedded binary to a temporary location
- The extracted binary is made executable and used to spawn the Rust subprocess
- Multiple platform binaries can be embedded for cross-platform distributions
- **Runtime Configuration**: The Rust subprocess uses Phase 4's runtime configuration patterns for executor, alongside rust2go settings

## Error Handling and Context Propagation

### Leveraging Phase 5 via OTLP

**All Phase 5 features work automatically through standard OTLP:**

1. **Context Deadlines**: gRPC context deadlines in OTLP calls
2. **Cancellation**: gRPC stream cancellation propagates across process boundary
3. **Error Classification**: Rust `ProcessingError` → gRPC status → Go `consumererror`
4. **Metadata**: `client.Metadata` flows through gRPC headers
5. **Tracing**: Distributed tracing context via gRPC metadata

**Error Mapping**: Phase 5's structured `ProcessingError` enum maps to gRPC status codes in OTLP responses, which the Go collector translates back to `consumererror` classifications, preserving permanent vs retryable error semantics.

**No additional implementation needed** - standard OTLP receiver/exporter handles all context propagation patterns established in Phase 5.

### Health Monitoring

**Automated Health Checks:**

- The parent collector periodically checks the Rust subprocess health via gRPC health service
- Health checks use the same unix socket as data communication
- Failed health checks trigger automatic subprocess restart attempts
- The OTLP exporter's built-in retry logic handles temporary connection issues during restart

**Operational Integration:**

- Subprocess logs are integrated with the parent collector's logging system
- Health status is exposed through the parent collector's metrics and status endpoints
- Graceful degradation when the subprocess is temporarily unavailable

## Platform Support and Compatibility

### Build Matrix

| Build Mode | Platform | Requirements | Distribution |
|------------|----------|--------------|--------------|
| Embedded | Linux/macOS | CGO + rust2go | Single binary |
| Subprocess | Linux/macOS | Unix sockets | Single binary |
| Subprocess | Windows | Named pipes | Single binary |
| Subprocess | Any | TCP localhost | Single binary |

### Transparent User Experience

**Configuration remains identical:**

- Same YAML configuration for both modes
- Builder automatically chooses mode based on constraints
- No changes to component specifications from Phase 1

**Mode Selection:**

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

### When to Use Each Mode

**Embedded Mode (Phases 1-5):**

- Maximum performance requirements
- Platform supports CGO + rust2go
- Complex FFI acceptable

**Subprocess Mode (Phase 6):**

- Pure Go build requirements
- Platform limitations (CGO unavailable)
- Operational simplicity preferred
- Security isolation needed

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
