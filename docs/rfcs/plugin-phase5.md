# Phase 5: Cross-Runtime Metadata Propagation

## Overview

Phase 5 establishes bidirectional metadata propagation between Go and Rust runtimes, enabling seamless context flow and error handling across the FFI boundary. This phase builds on Phase 4's otap-dataflow integration and provides the foundation for production-grade telemetry processing with proper observability, cancellation, and error propagation.

## Design Goals

1. **Forward Context Propagation**: Propagate Go `context.Context` with timeouts, cancellation, and metadata to Rust EffectHandler
2. **Reverse Error Propagation**: Translate Rust enum-structured errors to Go error interface with proper permanent/retryable classification
3. **Standards Compliance**: Follow gRPC and HTTP metadata conventions established in the collector
4. **Performance**: Minimize serialization overhead for high-throughput data processing
5. **Observability**: Enable distributed tracing and debugging across runtime boundaries

## Architecture Overview

### Forward Propagation: Go Context → Rust EffectHandler

```text
Go Context {
  - Deadline/Timeout
  - Cancellation
  - client.Metadata
  - trace.SpanContext
} → rust2go FFI → Rust EffectHandler {
  - Async cancellation tokens
  - Timeout handling
  - Metadata access
  - Tracing integration
}
```

### Reverse Propagation: Rust Error → Go Error

```text
Rust Error {
  - enum variants
  - gRPC codes
  - HTTP status codes
  - Retry information
} → rust2go FFI → Go Error {
  - consumererror.Permanent
  - gRPC status codes
  - HTTP status codes
  - Retry semantics
}
```

## Forward Context Propagation Design

### Context Transfer Strategy

Phase 5 uses a dual strategy for transferring Go context information to Rust:

1. **Primary: rust2go Struct Marshaling** - Direct marshaling of context data structures for optimal performance
2. **Fallback: JSON Serialization** - Used when rust2go marshaling proves difficult for complex metadata scenarios

### Context Information Flow

The context transfer process extracts key information from Go's `context.Context`:

**Timing Information**:

- Deadline timestamps (Unix time) when present
- Boolean flags indicating deadline presence
- Timeout calculations for Rust async operations

**Cancellation Coordination**:

- Unique cancellation token IDs for cross-runtime coordination
- Go-side cancellation monitoring with immediate Rust notification
- Bidirectional cancellation channel management

**Client Metadata Propagation**:

- Extraction from existing `client.Metadata` patterns
- Key-value mapping following gRPC/HTTP conventions
- Preservation of multiple values per metadata key

**Distributed Tracing Context**:

- OpenTelemetry trace ID and span ID propagation
- Trace flags and trace state preservation
- Remote span context indication

The cancellation system provides immediate coordination between Go context cancellation and Rust async operations:

- **Token Registry**: Global registry manages active cancellation tokens
- **Monitoring**: Background goroutines watch for Go context cancellation
- **Notification**: Immediate rust2go async calls signal cancellation to Rust
- **Cleanup**: Automatic token cleanup after cancellation or completion

### rust2go FFI Integration

Rust receives context information through rust2go's marshaling system and integrates it with async cancellation patterns:

**Context Structure Translation**:

- Direct mapping from Go context transfer structures to Rust equivalents
- HashMap conversion for metadata with proper key/value preservation
- Byte array handling for trace context information

**Cancellation Token Integration**:

- Tokio broadcast channels for async cancellation coordination
- Cross-runtime token management with automatic cleanup
- Select-based cancellation in async operations

**rust2go Trait Implementation**:

- Dedicated traits for cancellation bridge operations
- Async FFI patterns for non-blocking cancellation signaling
- Queue-based message handling for high-frequency operations

### EffectHandler Context Integration

Phase 5 extends the existing otap-dataflow EffectHandler with cross-runtime context awareness:

**Context-Aware EffectHandler Enhancement**:

- Wraps existing EffectHandler with context propagation capabilities
- Integrates cancellation tokens for immediate async operation cancellation
- Provides timeout-aware send operations using context deadline information
- Maintains backward compatibility with existing EffectHandler interface

**Timeout and Cancellation Integration**:

- `send_message_with_timeout()` operations respect Go context deadlines
- Tokio `select!` patterns coordinate between data sending and cancellation events
- Automatic deadline checking before initiating long-running operations
- Graceful error handling when operations are cancelled or timeout

**Metadata and Tracing Access**:

- Helper methods provide access to propagated client metadata
- Trace context information available for distributed tracing integration
- Metadata access follows existing collector conventions (case-insensitive keys)
- Support for multi-value metadata headers

## Reverse Error Propagation Design

### Rust Error Enumeration

Phase 5 establishes structured error types for seamless marshaling across the Go/Rust boundary:

**Structured Error Classification**:

- **Permanent Errors**: Configuration issues, invalid arguments, authentication failures (no retry)
- **Retryable Errors**: Temporary failures, network issues, resource unavailability
- **Cancellation Errors**: Operations cancelled due to context cancellation from Go runtime
- **Timeout Errors**: Operations exceeding deadline (retryable by default)
- **Resource Exhaustion**: Rate limiting or capacity issues with optional retry-after timing
- **Configuration Errors**: Invalid component configuration (permanent failures)

**gRPC and HTTP Code Integration**:

Each error type includes appropriate status codes following collector conventions:

- Permanent errors: `codes.Internal` (gRPC) / 500 (HTTP)
- Retryable errors: `codes.Unavailable` (gRPC) / 503 (HTTP)
- Cancellation: `codes.Cancelled` (gRPC) / 499 (HTTP)
- Timeout: `codes.DeadlineExceeded` (gRPC) / 504 (HTTP)
- Resource exhaustion: `codes.ResourceExhausted` (gRPC) / 429 (HTTP)

**Retry Semantics**:

- Optional retry-after timing for rate limiting scenarios
- Classification methods for Go error translation (`is_permanent()`)
- Convenience constructors for common error patterns

### rust2go Error Transfer

Error information crosses the runtime boundary through simplified transfer structures optimized for rust2go marshaling:

**Error Transfer Structure**:

- Flattened error representation with essential information for Go error translation
- String message preservation with context information
- Boolean permanent/retryable classification for immediate Go error handling
- Numeric code preservation for gRPC and HTTP status integration
- Optional retry timing for rate limiting scenarios
- Error type categorization for structured handling

**Marshaling Strategy**:

- Primary use of rust2go struct marshaling for performance
- JSON fallback for complex error scenarios
- Minimal serialization overhead with precomputed error properties
- Automatic conversion from structured Rust enums to transfer format

### Go Error Translation

Go receives rust2go error transfers and translates them into appropriate Go error types following collector conventions:

**Error Type Translation**:

- `RustProcessingError` implements Go's `error` interface with full error information
- Integration with `consumererror.NewPermanent()` for non-retryable errors
- Automatic classification based on error permanence and gRPC codes
- Preservation of retry timing and metadata for higher-level retry logic

**gRPC Integration**:

- Automatic conversion to gRPC `status.Status` with appropriate codes
- Integration with error details for retry information
- Support for existing gRPC error handling patterns in the collector

**HTTP Status Integration**:

- HTTP status code preservation for HTTP-based exporters and receivers
- Standard HTTP error patterns following collector conventions
- Support for retry-after headers in HTTP scenarios

**consumererror Classification**:

The translation process applies collector-standard error classification:

- Permanent errors automatically wrapped with `consumererror.NewPermanent()`
- Specific gRPC codes (InvalidArgument, Unauthenticated, PermissionDenied, Unimplemented) force permanent classification
- Retryable errors return as standard Go errors for normal retry processing
- Error context preservation for debugging and observability

## Integration with otap-dataflow

### Context-Aware Processor Pattern

Phase 5 enhances existing otap-dataflow processor patterns with context propagation and timeout support:

**Enhanced Processor Interface**:

- Extended processor trait includes context-aware EffectHandler parameter
- Automatic context validation before processing operations
- Timeout checking and deadline enforcement during batch operations
- Cancellation support for long-running processing tasks

**Batch Processing with Context**:

Example batch processor demonstrates context integration patterns:

- Context deadline checking before adding items to batches
- Timeout-aware batch flushing with cancellation support
- Graceful shutdown with context-aware cleanup operations
- Select-based coordination between data processing and cancellation events

**Shutdown and Cleanup**:

- Context cancellation during shutdown triggers immediate cleanup
- Remaining data flushed with timeout awareness
- Resource cleanup respects cancellation deadlines
- Error propagation during shutdown follows structured error patterns

### Select Statement Integration

Phase 5 enables Go-style coordination between Go context events and Rust async operations:

**Context Coordination Strategy**:

- Go `select` statements coordinate between context cancellation and Rust completion
- Immediate cancellation signaling to Rust when Go context is cancelled
- Graceful timeout handling with brief cleanup periods
- Result channel coordination for async rust2go operations

**Processing Flow Pattern**:

The integration follows a standard pattern for all processing operations:

1. **Context Extraction**: Go context information extracted and prepared for rust2go transfer
2. **Async Initiation**: Rust processing started with context information via rust2go
3. **Select Coordination**: Go select statement monitors context cancellation and Rust completion
4. **Result Translation**: Rust error transfers translated to appropriate Go error types
5. **Cleanup**: Cancellation tokens and resources cleaned up regardless of outcome

**Timeout and Cancellation Handling**:

- Context cancellation triggers immediate Rust notification
- Brief cleanup periods allow Rust operations to complete gracefully
- Fallback timeouts prevent hanging operations when Rust doesn't respond quickly
- Error translation preserves cancellation context for proper error reporting

## gRPC and HTTP Metadata Conventions

### Client Metadata Integration

Phase 5 preserves existing collector `client.Metadata` patterns while making them accessible in Rust:

**Metadata Access Patterns**:

- Helper methods provide access to propagated client metadata following collector conventions
- Case-insensitive key lookup matching existing `client.Metadata.Get()` behavior
- Support for multi-value headers with proper value ordering
- Standard header extraction for host, authorization, and user-agent information

**gRPC Convention Compliance**:

- Authority header handling for gRPC-style host information (`:authority` pseudo-header)
- Authorization header processing for authentication information
- User-agent preservation for client identification
- Custom metadata preservation with proper key/value mapping

**HTTP Header Integration**:

- Standard HTTP headers accessible through consistent interface
- Header case normalization following HTTP/gRPC conventions
- Multi-value header support for headers allowing multiple values
- Integration with existing collector HTTP receiver patterns

### Error Code Mappings

Phase 5 follows established error code mappings from the collector's existing error handling infrastructure:

**gRPC Status Code Mappings**:

- **Configuration Errors**: `InvalidArgument` (3) for malformed configuration
- **Permanent Errors**: `Internal` (13) unless specific code provided
- **Timeout Errors**: `DeadlineExceeded` (4) for context deadline violations
- **Cancellation Errors**: `Cancelled` (1) for context cancellation
- **Resource Exhaustion**: `ResourceExhausted` (8) for rate limiting
- **Retryable Errors**: `Unavailable` (14) unless specific code provided

**HTTP Status Code Mappings**:

- **Configuration Errors**: 400 (Bad Request) for invalid configuration
- **Permanent Errors**: 500 (Internal Server Error) for processing failures
- **Timeout Errors**: 504 (Gateway Timeout) for deadline violations
- **Cancellation Errors**: 499 (Client Closed Request) for cancellation
- **Resource Exhaustion**: 429 (Too Many Requests) for rate limiting
- **Retryable Errors**: 503 (Service Unavailable) for temporary failures

**Collector Integration**:

The mappings follow patterns in `receiver/otlpreceiver/internal/errors` and integrate with existing error handling, ensuring consistent error behavior across Go and Rust components.

## Performance Considerations

### Marshaling Strategy

Phase 5 employs a performance-optimized approach to cross-runtime data transfer:

**Primary Strategy: rust2go Struct Marshaling**:

- Direct struct marshaling for simple context and error data structures
- Minimal serialization overhead through native rust2go type mapping
- Automatic field mapping without manual serialization/deserialization
- Type-safe marshaling with compile-time validation

**Fallback Strategy: JSON Serialization**:

- Used only when rust2go marshaling proves difficult for complex metadata scenarios
- Graceful degradation with minimal context information when JSON parsing fails
- Cached JSON serialization for repeated context transfers
- Structured error handling for serialization failures

**Context Optimization**:

- Context transfers cached and reused when context information unchanged
- Lazy deserialization where Rust components only access needed context fields
- String interning for common metadata keys and error messages
- Minimal context structure with only essential information

### Memory Management

**Automatic Resource Cleanup**:

- Cancellation tokens automatically cleaned up after use or cancellation
- rust2go handle management ensures proper memory cleanup across boundaries
- Context structures use stack allocation where possible in Rust
- Error structures minimize heap allocation through precomputed fields

**Memory Efficiency**:

- Shared string storage for common error messages and metadata keys
- Context caching reduces repeated allocations for similar requests
- Cancellation channel sizing optimized for minimal memory overhead
- Error transfer structures designed for minimal serialization size

### Async Integration

**Non-blocking Operations**:

- All FFI calls use rust2go's async patterns to avoid blocking Go runtime
- Channel-based coordination between Go select statements and Rust async operations
- Background cancellation monitoring with minimal goroutine overhead
- Efficient channel sizing to prevent memory buildup during high throughput

**Performance Targets**:

- Overall metadata propagation overhead target: <5% for high-throughput scenarios
- Context transfer latency: <100 microseconds for typical metadata sizes
- Error translation overhead: <10 microseconds per error
- Memory overhead: <1KB per active context transfer

## Migration Strategy

### Phase 5.1: Context Marshaling

**Foundation Implementation**:

- Implement basic context transfer structures using rust2go's native marshaling capabilities
- Add cancellation token management with cross-runtime coordination
- Establish JSON fallback patterns for complex metadata scenarios that prove difficult for direct marshaling
- Create helper functions for context extraction and validation

### Phase 5.2: Error Propagation

**Structured Error System**:

- Implement structured error types in Rust with comprehensive enum variants
- Add rust2go marshaling support for error transfer structures
- Create Go error translation layer with `consumererror` integration
- Establish error code mapping following collector conventions

### Phase 5.3: EffectHandler Integration

**Context-Aware Processing**:

- Extend otap-dataflow EffectHandler with context awareness and timeout support
- Add timeout and cancellation support to send operations with select-based coordination
- Implement metadata and tracing access methods following collector patterns
- Performance optimization with context caching and efficient async coordination

### Phase 5.4: Production Hardening

**Operational Excellence**:

- Comprehensive error handling coverage with graceful degradation patterns
- Performance benchmarking and optimization to meet <5% overhead target
- Production logging and debugging tools for cross-runtime troubleshooting
- Integration testing with realistic workloads and failure scenarios

## Connection to Previous Phases

### Phase 4 Integration

**otap-dataflow Foundation**:

- Builds directly on Phase 4's otap-dataflow EffectHandler patterns for high-performance telemetry processing
- Extends existing processor, exporter, and receiver traits with context awareness
- Maintains full compatibility with existing factory patterns while adding context propagation
- Preserves performance benefits of shared memory rings and batched operations

### Factory System Enhancement

**Lifecycle Integration**:

- Component factories pass context information during Create() calls following established patterns
- Error translation integrates seamlessly with factory validation patterns from Phase 2
- Cancellation tokens managed through component lifecycle with proper resource cleanup
- Context extraction and validation integrated with existing configuration validation flows

### Configuration Integration (Phase 2)

**Configuration Bridge Extension**:

- Context configuration becomes part of runtime configuration through existing serde patterns
- Metadata parsing leverages Phase 2's confmap ↔ serde integration for consistency
- Error handling extends validation error patterns established in configuration bridge
- Configuration-time context validation follows existing factory patterns

**Build System Integration (Phase 1)**:

- Context propagation patterns work with mixed Go/Rust components identified in Phase 1
- Factory registration supports context-aware components with no changes to builder tool
- Component discovery and compilation patterns remain unchanged with context enhancement

## Success Criteria

- [ ] Go context.Context with deadlines propagates to Rust EffectHandler operations
- [ ] Go context cancellation immediately cancels ongoing Rust async operations
- [ ] Rust ProcessingError enum values correctly map to Go consumererror classification
- [ ] gRPC and HTTP error codes are preserved across runtime boundaries
- [ ] Client metadata follows collector conventions and is accessible in Rust
- [ ] Distributed tracing context flows seamlessly across Go/Rust boundary
- [ ] All timeout and cancellation scenarios work correctly under load

This design provides a robust foundation for production telemetry processing with full observability and error handling across Go and Rust runtimes, building naturally on the collector's existing patterns while leveraging the performance benefits of the otap-dataflow engine.
