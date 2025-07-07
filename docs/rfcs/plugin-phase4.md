# Phase 4: Pipeline Components Integration with otap-dataflow

## Overview

Phase 4 integrates the OpenTelemetry Collector with the existing `otel-arrow/rust/otap-dataflow` engine for high-performance telemetry data processing. Rather than designing new Rust component patterns, Phase 4 creates a bridge between the Go collector's component interfaces and otap-dataflow's proven EffectHandler architecture.

## Pipeline Component Factory Architecture

### Factory Pattern Extension from Phase 3

Phase 4 extends the factory pattern from Phase 3 to pipeline components (processors, exporters, receivers). Each component type implements its specialized factory interface while maintaining core patterns:

**Processor Factory Design**:

- Implements standard collector `processor.Factory` interface
- Uses component type registration pattern from previous phases
- Returns `RustComponentConfig` wrapper for configuration consistency
- Creates Go wrapper components that bridge to otap-dataflow engine
- Separates factory creation from component instantiation for lifecycle management
- Supports all telemetry types (traces, metrics, logs) through specialized create methods

**Rust Factory Registration Design**:

The Rust side uses compile-time registration to provide otap-dataflow components:

- Uses linkme crate for distributed slice registration at link time
- Defines factory traits with methods for component type identification, default configuration, validation, and processor creation
- Implements specific factory types (like BatchProcessorFactory) that create otap-dataflow processor instances
- Components implement the otap-dataflow Processor trait and use EffectHandler for runtime interaction
- Factory registration allows automatic discovery of available processor types at build time

### Factory Integration Across All Phases

The factory architecture provides a consistent foundation across all phases:

1. **Phase 1**: `cargo` fields in builder YAML identify Rust factories for compilation
2. **Phase 2**: Factory `CreateDefaultConfig()` returns serde-based configuration structs  
3. **Phase 3**: Extension factories demonstrate basic lifecycle with rust2go integration
4. **Phase 4**: Pipeline factories extend the pattern to processors/exporters/receivers with otap-dataflow

Each phase builds on the factory foundations:

- **Configuration consistency**: All factories use `RustComponentConfig` wrapper from Phase 2
- **Lifecycle patterns**: All factories separate Create() from Start() as established in Phase 3  
- **Registration uniformity**: Rust linkme static registration complements Go manual registration
- **Type safety**: Factory `Type()` method ensures correct component type registration

## Core Integration Strategy

### Target Architecture: otap-dataflow Bridge

Phase 4 creates a translation layer between:

```text
Go Collector Interfaces ↔ rust2go FFI ↔ otap-dataflow Engine
```

**Key Insight**: Instead of inventing new traits, leverage the existing otap-dataflow component system:

- **Processors**: `shared::Processor<PData>` containing `EffectHandler<PData>` for runtime interaction
- **Exporters**: `shared::Exporter<PData>` containing `EffectHandler<PData>` for runtime interaction
- **Receivers**: `shared::Receiver<PData>` containing `EffectHandler<PData>` for runtime interaction

### Consumer Interface Bridge Focus

The essential work is translating between consumer interfaces:

**Go Side (pdata → bytes)**:

- `consumer.Traces.ConsumeTraces(pdata.Traces)` → OTLP bytes
- `consumer.Logs.ConsumeLogs(pdata.Logs)` → OTLP bytes  
- `consumer.Metrics.ConsumeMetrics(pdata.Metrics)` → OTLP bytes

**Rust Side (bytes → OTAP)**:

- OTLP bytes → Arrow record batches (OTAP format)
- Feed into otap-dataflow `Processor<PData>`/`Exporter<PData>`/`Receiver<PData>` components
- Components use their `EffectHandler<PData>` for runtime interaction

## Rust Runtime Configuration

The runtime configuration manages the Rust execution environment and rust2go communication:

**Configuration Structure**:

```yaml
rust_runtime:
  executor:
    type: "tokio_local"  # Local task executor
  rust2go:
    queue_size: 65536    # Shared memory ring buffer size
```

**Implementation Details**:

- Uses standard serde patterns with default values
- **Executor Configuration**: Manages async runtime setup (tokio_local by default)  
- **Rust2go Configuration**: Controls shared memory buffer sizes for efficient cross-language communication
- **Default Values**: Provides sensible defaults for all configuration options

## otap-dataflow Integration Architecture

### Factory Traits

For interaction between Rust and Go, each pipeline component has a corresponding bridge. The processor interface example demonstrates the pattern for all component types.

The Rust side uses the [linkme crate](https://docs.rs/linkme/latest/linkme/) to register factories at link time.

**Key Bridge Operations**:

- **Factory Management**: Create and register factory instances with runtime configuration
- **Component Creation**: Instantiate processors for specific telemetry types (traces, metrics, logs)
- **Data Processing**: Convert OTLP protobuf to otap-dataflow format and process through components
- **Error Handling**: Structured error types for conversion, processing, and handle management
- **Handle Management**: Use numeric handles to reference factory and component instances across language boundaries

**Implementation Approach**:

- Use rust2go traits with memory-based communication for efficient data transfer
- Parse runtime and component configurations using serde JSON handling
- Leverage distributed slice registration to discover available factories by name
- Convert OTLP protobuf data to otap-dataflow message format for processing
- Create EffectHandlers for each processing operation to manage component interactions

### Factory Integration

The factory integration handles the lifecycle from factory registration through data processing:

**Factory Registration Process**:

- Parse runtime configuration using serde JSON handling
- Find registered factories in distributed slices by component name
- Store factory references with runtime configuration and return handle IDs

**Component Creation Process**:

- Retrieve factory instances using handle IDs
- Parse component-specific configuration from JSON bytes
- Create processor instances specialized for telemetry types (traces, metrics, logs)
- Register component instances and return processor handle IDs

**Data Processing Flow**:

- Retrieve processor instances using handle IDs
- Convert OTLP protobuf data to otap-dataflow message format
- Create EffectHandler instances for processing operations
- Execute processing through otap-dataflow Processor trait
- Handle structured errors for conversion, processing, and handle management

This approach provides type-safe, efficient communication between Go and Rust while leveraging the proven otap-dataflow architecture for high-performance telemetry processing.

## Data Format Translation

### pdata ↔ OTAP Bridge

The key technical challenge is efficient conversion between formats:

1. **Go pdata → OTLP protobuf**: Use existing collector serialization
2. **OTLP protobuf → OTAP Arrow**: Implemented within `otap-dataflow`
3. **OTAP Arrow → OTLP protobuf**: Reverse conversion for output, also within `otap-dataflow`
4. **OTLP protobuf → Go pdata**: Use existing collector deserialization

## Implementation Phases

### 4.1: Consumer Interface Bridge

**Priority**: Create translation layer for consumer interfaces

- Create rust2go traits that wrap otap-dataflow components
- Extend Phase 3 lifecycle patterns: create `otap-dataflow` components using that crate's abstractions

### 4.2: Component Implementation

**Priority**: Implement specific component types using otap-dataflow

- **Processors**: Use `shared::Processor<PData>` with contained `EffectHandler<PData>`
- **Exporters**: Use `shared::Exporter<PData>` with contained `EffectHandler<PData>`
- **Receivers**: Use `shared::Receiver<PData>` with contained `EffectHandler<PData>`

## Connection to Previous Phases

Phase 4 builds directly on established foundations:

- **Phase 2**: `RustComponentConfig` pattern extends to runtime configuration
- **Phase 3**: Component lifecycle management (create/start/shutdown) and FFI for extensions

## Scope Boundaries

**In Scope**:

- Receiver, Exporter, Processor components (receiver.Factory, exporter.Factory, processor.Factory, ...)
- Consumer interface translation (consumer.Traces, consumer.Logs, consumer.Metrics, ...)
- Integration with existing otap-dataflow components for OTLP pdata (Go) to OTAP pdata (Rust)
- Runtime configuration for Rust execution environment using `otap-dataflow`
- Rust2go integration for passing OTLP bytes in consumers.