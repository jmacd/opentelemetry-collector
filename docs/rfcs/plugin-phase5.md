# Phase 5: Cross-Runtime Metadata Propagation

## Overview

Phase 5 establishes bi-directional metadata propagation between Go and Rust runtimes, enabling seamless context flow and error handling across the FFI boundary. This phase builds on the otap-dataflow integration from Phase 4 and provides the foundation for production-grade telemetry processing with proper observability, cancellation, and error propagation.

## Design Goals

1. **Forward Context Propagation**: Seamlessly propagate Go `context.Context` with timeouts, cancellation, and metadata to Rust EffectHandler
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

Phase 5 uses rust2go's native struct marshaling for simple context data, with JSON as a fallback for complex scenarios that prove difficult to marshal directly.

#### Primary Approach: rust2go Struct Marshaling

```go
// ContextTransfer represents context information for rust2go marshaling
type ContextTransfer struct {
    // Deadline information
    DeadlineUnix int64  `json:"deadline_unix"`
    HasDeadline  bool   `json:"has_deadline"`
    
    // Cancellation token ID for rust2go management
    CancelTokenID string `json:"cancel_token_id"`
    
    // Simple metadata (rust2go can marshal map[string][]string)
    Metadata map[string][]string `json:"metadata,omitempty"`
    
    // Distributed tracing context
    TraceID    []byte `json:"trace_id,omitempty"`
    SpanID     []byte `json:"span_id,omitempty"`
    TraceFlags uint32 `json:"trace_flags"`
    TraceState string `json:"trace_state,omitempty"`
    Remote     bool   `json:"remote"`
}

// extractContextTransfer prepares Go context for rust2go marshaling
func extractContextTransfer(ctx context.Context) (*ContextTransfer, context.CancelFunc) {
    transfer := &ContextTransfer{}
    
    // Extract deadline
    if deadline, ok := ctx.Deadline(); ok {
        transfer.DeadlineUnix = deadline.Unix()
        transfer.HasDeadline = true
    }
    
    // Extract client metadata
    if clientInfo := client.FromContext(ctx); clientInfo.Metadata != nil {
        transfer.Metadata = make(map[string][]string)
        for key := range clientInfo.Metadata.Keys() {
            transfer.Metadata[key] = clientInfo.Metadata.Get(key)
        }
    }
    
    // Extract tracing span context
    if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
        traceID := spanCtx.TraceID()
        spanID := spanCtx.SpanID()
        transfer.TraceID = traceID[:]
        transfer.SpanID = spanID[:]
        transfer.TraceFlags = uint32(spanCtx.TraceFlags())
        transfer.TraceState = spanCtx.TraceState().String()
        transfer.Remote = spanCtx.IsRemote()
    }
    
    // Generate cancellation token for rust2go
    cancelCtx, cancelFunc := context.WithCancel(ctx)
    transfer.CancelTokenID = generateCancelTokenID()
    
    // Register cancellation monitoring
    go monitorCancellation(cancelCtx, transfer.CancelTokenID)
    
    return transfer, cancelFunc
}
```

#### Fallback Approach: JSON Serialization

If rust2go struct marshaling proves difficult for complex metadata scenarios:

```go
// JSONContextTransfer as fallback for complex cases
type JSONContextTransfer struct {
    ContextJSON string `json:"context_json"`
}

func extractContextTransferJSON(ctx context.Context) (*JSONContextTransfer, context.CancelFunc) {
    transfer, cancelFunc := extractContextTransfer(ctx)
    
    jsonBytes, err := json.Marshal(transfer)
    if err != nil {
        // Fallback to minimal context
        jsonBytes = []byte(`{"has_deadline":false,"metadata":{}}`)
    }
    
    return &JSONContextTransfer{
        ContextJSON: string(jsonBytes),
    }, cancelFunc
}
```

#### Cancellation Token Management

```go
// CancellationRegistry manages active cancellation tokens
type CancellationRegistry struct {
    mu     sync.RWMutex
    tokens map[string]chan struct{}
}

var globalCancelRegistry = &CancellationRegistry{
    tokens: make(map[string]chan struct{}),
}

func (cr *CancellationRegistry) Register(tokenID string) chan struct{} {
    cr.mu.Lock()
    defer cr.mu.Unlock()
    
    cancelChan := make(chan struct{})
    cr.tokens[tokenID] = cancelChan
    return cancelChan
}

func (cr *CancellationRegistry) Cancel(tokenID string) {
    cr.mu.Lock()
    defer cr.mu.Unlock()
    
    if cancelChan, exists := cr.tokens[tokenID]; exists {
        close(cancelChan)
        delete(cr.tokens, tokenID)
    }
}

// monitorCancellation watches for Go context cancellation and signals Rust
func monitorCancellation(ctx context.Context, tokenID string) {
    select {
    case <-ctx.Done():
        // Signal cancellation to Rust through rust2go
        globalCancelRegistry.Cancel(tokenID)
        
        // Notify Rust via rust2go async call
        go func() {
            _ = callRustCancelToken(tokenID)
        }()
    }
}
```

### rust2go FFI Integration

```rust
// Context transfer structures matching Go types
#[derive(Debug, Clone)]
pub struct ContextTransfer {
    pub deadline_unix: i64,
    pub has_deadline: bool,
    pub cancel_token_id: String,
    pub metadata: HashMap<String, Vec<String>>,
    pub trace_id: Vec<u8>,
    pub span_id: Vec<u8>,
    pub trace_flags: u32,
    pub trace_state: String,
    pub remote: bool,
}

// Rust cancellation token integration
#[derive(Clone)]
pub struct CrossRuntimeCancelToken {
    token_id: String,
    cancel_tx: Option<tokio::sync::broadcast::Sender<()>>,
    cancel_rx: tokio::sync::broadcast::Receiver<()>,
}

impl CrossRuntimeCancelToken {
    pub fn new(token_id: String) -> Self {
        let (cancel_tx, cancel_rx) = tokio::sync::broadcast::channel(1);
        Self {
            token_id,
            cancel_tx: Some(cancel_tx),
            cancel_rx,
        }
    }
    
    pub async fn cancelled(&mut self) -> bool {
        match self.cancel_rx.recv().await {
            Ok(_) | Err(tokio::sync::broadcast::error::RecvError::Closed) => true,
            Err(tokio::sync::broadcast::error::RecvError::Lagged(_)) => true,
        }
    }
    
    pub fn cancel(&self) {
        if let Some(tx) = &self.cancel_tx {
            let _ = tx.send(());
        }
    }
}

// rust2go trait for cancellation management
#[rust2go::r2g(queue_size = 1024)]
pub trait CancellationBridge {
    #[mem]
    async fn cancel_token(&mut self, token_id: String) -> Result<(), String>;
}

impl CancellationBridge for CancellationBridgeImpl {
    async fn cancel_token(&mut self, token_id: String) -> Result<(), String> {
        if let Some(token) = self.active_tokens.get(&token_id) {
            token.cancel();
            self.active_tokens.remove(&token_id);
        }
        Ok(())
    }
}
```

### EffectHandler Context Integration

Phase 5 extends the existing otap-dataflow EffectHandler with cross-runtime context awareness:

```rust
// Enhanced EffectHandler with context propagation
pub struct ContextAwareEffectHandler<PData> {
    inner: shared::EffectHandler<PData>,
    context: Option<ContextTransfer>,
    cancel_token: Option<CrossRuntimeCancelToken>,
}

impl<PData> ContextAwareEffectHandler<PData> {
    pub fn new(
        inner: shared::EffectHandler<PData>,
        context: ContextTransfer,
    ) -> Self {
        let cancel_token = if !context.cancel_token_id.is_empty() {
            Some(CrossRuntimeCancelToken::new(context.cancel_token_id.clone()))
        } else {
            None
        };
        
        Self {
            inner,
            cancel_token,
            context: Some(context),
        }
    }
    
    // Forward send_message with timeout and cancellation
    pub async fn send_message(&self, data: PData) -> Result<(), Error<PData>> {
        if let Some(ref cancel_token) = &self.cancel_token {
            tokio::select! {
                result = self.inner.send_message(data) => result,
                _ = cancel_token.cancelled() => {
                    Err(Error::Cancelled {
                        reason: "Context cancelled from Go runtime".to_string(),
                    })
                }
            }
        } else {
            self.inner.send_message(data).await
        }
    }
    
    // Timeout-aware send with deadline
    pub async fn send_message_with_timeout(&self, data: PData) -> Result<(), Error<PData>> {
        if let Some(context) = &self.context {
            if context.has_deadline {
                let deadline = SystemTime::UNIX_EPOCH + Duration::from_secs(context.deadline_unix as u64);
                let timeout = deadline.duration_since(SystemTime::now())
                    .unwrap_or(Duration::ZERO);
                    
                tokio::select! {
                    result = self.send_message(data) => result,
                    _ = tokio::time::sleep(timeout) => {
                        Err(Error::Timeout {
                            deadline: context.deadline_unix,
                        })
                    }
                }
            } else {
                self.send_message(data).await
            }
        } else {
            self.send_message(data).await
        }
    }
    
    // Access to propagated metadata
    pub fn metadata(&self) -> Option<&HashMap<String, Vec<String>>> {
        self.context.as_ref().map(|c| &c.metadata)
    }
    
    // Tracing context access
    pub fn trace_context(&self) -> Option<(&[u8], &[u8], u32, &str, bool)> {
        self.context.as_ref().map(|c| (
            &c.trace_id[..],
            &c.span_id[..],
            c.trace_flags,
            &c.trace_state,
            c.remote
        ))
    }
}
```

## Reverse Error Propagation Design

### Rust Error Enumeration

Phase 5 establishes structured error types that can be marshaled via rust2go or JSON fallback:

```rust
// Standard error types for cross-runtime propagation
#[derive(Debug, Clone)]
pub enum ProcessingError {
    // Permanent errors - do not retry
    Permanent {
        message: String,
        grpc_code: Option<i32>,      // gRPC codes::Code as i32
        http_status: Option<u16>,    // HTTP status code
    },
    
    // Retryable errors
    Retryable {
        message: String,
        grpc_code: Option<i32>,
        http_status: Option<u16>,
        retry_after: Option<u64>,    // Seconds to wait before retry
    },
    
    // Cancellation (special case of permanent)
    Cancelled {
        reason: String,
    },
    
    // Timeout (retryable by default)
    Timeout {
        deadline: i64,
    },
    
    // Resource exhaustion with backoff
    ResourceExhausted {
        message: String,
        retry_after: Option<u64>,
    },
    
    // Configuration errors (permanent)
    Configuration {
        message: String,
        field: Option<String>,
    },
}

impl ProcessingError {
    // Convenience constructors following gRPC conventions
    pub fn permanent(message: impl Into<String>) -> Self {
        Self::Permanent {
            message: message.into(),
            grpc_code: Some(13), // codes::Internal
            http_status: Some(500),
        }
    }
    
    pub fn retryable(message: impl Into<String>) -> Self {
        Self::Retryable {
            message: message.into(),
            grpc_code: Some(14), // codes::Unavailable
            http_status: Some(503),
            retry_after: None,
        }
    }
    
    pub fn invalid_argument(message: impl Into<String>) -> Self {
        Self::Permanent {
            message: message.into(),
            grpc_code: Some(3), // codes::InvalidArgument
            http_status: Some(400),
        }
    }
    
    pub fn resource_exhausted(message: impl Into<String>, retry_after: Option<u64>) -> Self {
        Self::ResourceExhausted {
            message: message.into(),
            retry_after,
        }
    }
    
    // Error classification for Go translation
    pub fn is_permanent(&self) -> bool {
        matches!(self, 
            ProcessingError::Permanent { .. } | 
            ProcessingError::Cancelled { .. } |
            ProcessingError::Configuration { .. }
        )
    }
    
    pub fn grpc_code(&self) -> i32 {
        match self {
            ProcessingError::Permanent { grpc_code, .. } => grpc_code.unwrap_or(13),
            ProcessingError::Retryable { grpc_code, .. } => grpc_code.unwrap_or(14),
            ProcessingError::Cancelled { .. } => 1, // codes::Cancelled
            ProcessingError::Timeout { .. } => 4,   // codes::DeadlineExceeded
            ProcessingError::ResourceExhausted { .. } => 8, // codes::ResourceExhausted
            ProcessingError::Configuration { .. } => 3,     // codes::InvalidArgument
        }
    }
    
    pub fn http_status(&self) -> u16 {
        match self {
            ProcessingError::Permanent { http_status, .. } => http_status.unwrap_or(500),
            ProcessingError::Retryable { http_status, .. } => http_status.unwrap_or(503),
            ProcessingError::Cancelled { .. } => 499, // Client Closed Request
            ProcessingError::Timeout { .. } => 504,   // Gateway Timeout
            ProcessingError::ResourceExhausted { .. } => 429, // Too Many Requests
            ProcessingError::Configuration { .. } => 400,     // Bad Request
        }
    }
    
    pub fn retry_after(&self) -> Option<u64> {
        match self {
            ProcessingError::Retryable { retry_after, .. } => *retry_after,
            ProcessingError::ResourceExhausted { retry_after, .. } => *retry_after,
            _ => None,
        }
    }
}
```

### rust2go Error Transfer

```rust
// Simplified error transfer for rust2go marshaling
#[derive(Debug, Clone)]
pub struct ErrorTransfer {
    pub message: String,
    pub permanent: bool,
    pub grpc_code: i32,
    pub http_status: u16,
    pub retry_after: Option<u64>,
    pub error_type: String,
}

impl From<ProcessingError> for ErrorTransfer {
    fn from(err: ProcessingError) -> Self {
        Self {
            message: match &err {
                ProcessingError::Permanent { message, .. } => message.clone(),
                ProcessingError::Retryable { message, .. } => message.clone(),
                ProcessingError::Cancelled { reason } => reason.clone(),
                ProcessingError::Timeout { deadline } => format!("Timeout at deadline {}", deadline),
                ProcessingError::ResourceExhausted { message, .. } => message.clone(),
                ProcessingError::Configuration { message, .. } => message.clone(),
            },
            permanent: err.is_permanent(),
            grpc_code: err.grpc_code(),
            http_status: err.http_status(),
            retry_after: err.retry_after(),
            error_type: match err {
                ProcessingError::Permanent { .. } => "permanent".to_string(),
                ProcessingError::Retryable { .. } => "retryable".to_string(),
                ProcessingError::Cancelled { .. } => "cancelled".to_string(),
                ProcessingError::Timeout { .. } => "timeout".to_string(),
                ProcessingError::ResourceExhausted { .. } => "resource_exhausted".to_string(),
                ProcessingError::Configuration { .. } => "configuration".to_string(),
            },
        }
    }
}
```

### Go Error Translation

```go
// RustProcessingError wraps rust2go error transfers for Go error interface
type RustProcessingError struct {
    Message      string  `json:"message"`
    Permanent    bool    `json:"permanent"`
    GRPCCode     int32   `json:"grpc_code"`
    HTTPStatus   uint16  `json:"http_status"`
    RetryAfter   *uint64 `json:"retry_after,omitempty"`
    ErrorType    string  `json:"error_type"`
}

func (e *RustProcessingError) Error() string {
    return e.Message
}

// translateRustError converts rust2go error transfer to Go error types
func translateRustError(errorTransfer *ErrorTransfer) error {
    if errorTransfer == nil {
        return nil
    }
    
    baseErr := &RustProcessingError{
        Message:    errorTransfer.Message,
        Permanent:  errorTransfer.Permanent,
        GRPCCode:   errorTransfer.GRPCCode,
        HTTPStatus: errorTransfer.HTTPStatus,
        RetryAfter: errorTransfer.RetryAfter,
        ErrorType:  errorTransfer.ErrorType,
    }
    
    // Apply consumererror classification
    if errorTransfer.Permanent {
        return consumererror.NewPermanent(baseErr)
    }
    
    // Check for specific gRPC error codes that should be permanent
    switch codes.Code(errorTransfer.GRPCCode) {
    case codes.InvalidArgument, codes.Unauthenticated, codes.PermissionDenied, codes.Unimplemented:
        return consumererror.NewPermanent(baseErr)
    default:
        return baseErr // Retryable by default
    }
}

// Integration with existing error handling patterns
func (e *RustProcessingError) GRPCStatus() *status.Status {
    st := status.New(codes.Code(e.GRPCCode), e.Message)
    
    // Add retry information if available
    if e.RetryAfter != nil {
        retryInfo := &errdetails.RetryInfo{
            RetryDelay: durationpb.New(time.Duration(*e.RetryAfter) * time.Second),
        }
        if st, err := st.WithDetails(retryInfo); err == nil {
            return st
        }
    }
    
    return st
}

func (e *RustProcessingError) HTTPStatusCode() int {
    return int(e.HTTPStatus)
}
```

## Integration with otap-dataflow

### Context-Aware Processor Pattern

```rust
// Enhanced processor trait with context propagation
#[async_trait]
pub trait ContextAwareProcessor<PData> {
    async fn process_with_context(
        &mut self,
        msg: Message<PData>,
        effect_handler: &mut ContextAwareEffectHandler<PData>,
    ) -> Result<(), ProcessingError>;
}

// Example batch processor with context integration
pub struct ContextAwareBatchProcessor {
    config: BatchProcessorConfig,
    pending_batches: HashMap<String, Vec<PData>>,
}

#[async_trait]
impl ContextAwareProcessor<OTLPData> for ContextAwareBatchProcessor {
    async fn process_with_context(
        &mut self,
        msg: Message<OTLPData>,
        effect_handler: &mut ContextAwareEffectHandler<OTLPData>,
    ) -> Result<(), ProcessingError> {
        match msg {
            Message::PData(data) => {
                // Check if context is still valid
                if let Some(context) = &effect_handler.context {
                    if context.has_deadline {
                        let now = SystemTime::now().duration_since(SystemTime::UNIX_EPOCH)
                            .unwrap().as_secs() as i64;
                        if now > context.deadline_unix {
                            return Err(ProcessingError::Timeout { deadline: context.deadline_unix });
                        }
                    }
                }
                
                // Add to batch
                self.add_to_batch(data);
                
                // Check if batch is ready to flush
                if self.should_flush() {
                    // Use timeout-aware send
                    effect_handler.send_message_with_timeout(batch_data).await
                        .map_err(|e| ProcessingError::retryable(format!("Batch send failed: {}", e)))?;
                }
                
                Ok(())
            }
            Message::Control(ControlMsg::Shutdown { .. }) => {
                // Flush remaining data with cancellation support
                self.flush_all(effect_handler).await
            }
            _ => Ok(())
        }
    }
    
    async fn flush_all(&mut self, effect_handler: &mut ContextAwareEffectHandler<OTLPData>) -> Result<(), ProcessingError> {
        for (_, batch) in self.pending_batches.drain() {
            // Use select! to respect cancellation during shutdown
            tokio::select! {
                result = effect_handler.send_message_with_timeout(batch) => {
                    result.map_err(|e| ProcessingError::retryable(format!("Flush failed: {}", e)))?;
                }
                _ = effect_handler.cancel_token.as_mut().unwrap().cancelled() => {
                    return Err(ProcessingError::Cancelled {
                        reason: "Shutdown cancelled by Go runtime".to_string(),
                    });
                }
            }
        }
        Ok(())
    }
}
```

### Select Statement Integration

Phase 5 enables Go-style `select` patterns for coordinating between Go context events and Rust async operations:

```go
// processTracesWithContext demonstrates select-based coordination
func (p *rustTracesProcessor) processTracesWithContext(
    ctx context.Context, 
    traces ptrace.Traces,
) error {
    // Extract context for Rust
    contextTransfer, cancelFunc := extractContextTransfer(ctx)
    defer cancelFunc()
    
    tracesJSON, _ := ptrace.NewJSONMarshaler().MarshalTraces(traces)
    
    // Create result channel for rust2go async call
    resultChan := make(chan *ErrorTransfer, 1)
    
    // Start async Rust processing (rust2go marshals contextTransfer directly)
    go func() {
        errorTransfer := callRustProcessTraces(contextTransfer, string(tracesJSON))
        resultChan <- errorTransfer
    }()
    
    // Use select to coordinate Go context and Rust completion
    select {
    case <-ctx.Done():
        // Context cancelled - signal Rust and wait briefly for cleanup
        globalCancelRegistry.Cancel(contextTransfer.CancelTokenID)
        
        select {
        case errorTransfer := <-resultChan:
            // Rust completed quickly, return its result
            return translateRustError(errorTransfer)
        case <-time.After(100 * time.Millisecond):
            // Rust didn't complete, return cancellation error
            return ctx.Err()
        }
        
    case errorTransfer := <-resultChan:
        // Rust processing completed normally
        return translateRustError(errorTransfer)
        
    case <-time.After(30 * time.Second):
        // Fallback timeout (should not happen with proper context deadline)
        globalCancelRegistry.Cancel(contextTransfer.CancelTokenID)
        return fmt.Errorf("rust processing timeout")
    }
}
```

## gRPC and HTTP Metadata Conventions

### Client Metadata Integration

Phase 5 preserves the existing `client.Metadata` patterns established in the collector:

```rust
// Rust metadata access following collector conventions
impl<PData> ContextAwareEffectHandler<PData> {
    // Get metadata value following client.Metadata.Get() pattern
    pub fn get_metadata(&self, key: &str) -> Vec<String> {
        self.metadata()
            .and_then(|m| m.get(&key.to_lowercase()))
            .cloned()
            .unwrap_or_default()
    }
    
    // Check for host header (following gRPC/HTTP conventions)
    pub fn host(&self) -> Option<String> {
        self.get_metadata("host")
            .or_else(|| self.get_metadata(":authority"))
            .into_iter()
            .next()
    }
    
    // Extract authentication information
    pub fn authorization(&self) -> Option<String> {
        self.get_metadata("authorization")
            .into_iter()
            .next()
    }
    
    // User agent information
    pub fn user_agent(&self) -> Option<String> {
        self.get_metadata("user-agent")
            .into_iter()
            .next()
    }
}
```

### Error Code Mappings

Phase 5 follows the established error code mappings from `receiver/otlpreceiver/internal/errors`:

```rust
impl ProcessingError {
    // Map to gRPC codes following collector conventions
    pub fn to_grpc_status(&self) -> i32 {
        match self {
            // Permanent errors
            ProcessingError::Configuration { .. } => 3,  // InvalidArgument
            ProcessingError::Permanent { grpc_code: Some(code), .. } => *code,
            ProcessingError::Permanent { .. } => 13,     // Internal
            
            // Retryable errors  
            ProcessingError::Timeout { .. } => 4,        // DeadlineExceeded
            ProcessingError::Cancelled { .. } => 1,      // Cancelled
            ProcessingError::ResourceExhausted { .. } => 8, // ResourceExhausted
            ProcessingError::Retryable { grpc_code: Some(code), .. } => *code,
            ProcessingError::Retryable { .. } => 14,     // Unavailable
        }
    }
    
    // Map to HTTP status codes following collector conventions
    pub fn to_http_status(&self) -> u16 {
        match self {
            ProcessingError::Configuration { .. } => 400,       // Bad Request
            ProcessingError::Permanent { .. } => 500,           // Internal Server Error
            ProcessingError::Timeout { .. } => 504,             // Gateway Timeout
            ProcessingError::Cancelled { .. } => 499,           // Client Closed Request
            ProcessingError::ResourceExhausted { .. } => 429,   // Too Many Requests
            ProcessingError::Retryable { .. } => 503,          // Service Unavailable
        }
    }
}
```

## Performance Considerations

### Marshaling Strategy

1. **rust2go Structs**: Primary approach for simple context and error data structures
2. **JSON Fallback**: Used when rust2go marshaling proves difficult for complex scenarios
3. **Context Caching**: Context transfers are cached and reused when context hasn't changed
4. **Lazy Deserialization**: Rust components only access context fields they actually use

### Memory Management

1. **String Interning**: Common metadata keys and error messages use string interning
2. **Context Cleanup**: Cancellation tokens are automatically cleaned up after use
3. **Error Allocation**: Processing errors use stack allocation where possible

### Async Integration

1. **Non-blocking FFI**: All FFI calls use rust2go's async patterns to avoid blocking
2. **Channel Sizing**: Cancellation channels use minimal buffer sizes
3. **Select Efficiency**: Go select statements avoid unnecessary goroutine spawning

## Migration Strategy

### Phase 5.1: Context Marshaling

- Implement basic context transfer structures using rust2go marshaling
- Add cancellation token management
- Fallback to JSON for complex metadata scenarios

### Phase 5.2: Error Propagation

- Implement structured error types in Rust with rust2go marshaling
- Add Go error translation layer
- Integrate with existing consumererror patterns

### Phase 5.3: EffectHandler Integration

- Extend otap-dataflow EffectHandler with context awareness
- Add timeout and cancellation support to send operations
- Performance optimization and caching

### Phase 5.4: Production Hardening

- Comprehensive error handling coverage
- Performance benchmarking and optimization
- Production logging and debugging tools

## Connection to Previous Phases

### Phase 4 Integration

- Builds on otap-dataflow EffectHandler patterns
- Extends processor/exporter/receiver traits with context awareness
- Maintains compatibility with existing factory patterns

### Factory System Enhancement

- Component factories pass context information during Create() calls
- Error translation integrates with factory validation patterns
- Cancellation tokens managed through component lifecycle

### Configuration Integration (Phase 2)

- Context configuration becomes part of runtime config
- Metadata parsing uses existing serde patterns
- Error handling extends validation error patterns

## Success Criteria

- [ ] Go context.Context with deadlines propagates to Rust EffectHandler operations
- [ ] Go context cancellation immediately cancels ongoing Rust async operations
- [ ] Rust ProcessingError enum values correctly map to Go consumererror classification
- [ ] gRPC and HTTP error codes are preserved across runtime boundaries
- [ ] Client metadata follows collector conventions and is accessible in Rust
- [ ] Distributed tracing context flows seamlessly across Go/Rust boundary
- [ ] Performance overhead is <5% for high-throughput telemetry processing
- [ ] All timeout and cancellation scenarios work correctly under load

This design provides a robust foundation for production telemetry processing with full observability and error handling across Go and Rust runtimes, building naturally on the collector's existing patterns while leveraging the performance benefits of the otap-dataflow engine.
