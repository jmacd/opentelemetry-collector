# Phase 4: Pipeline Components Integration with otap-dataflow

## Overview

Phase 4 integrates the OpenTelemetry Collector with the existing `otel-arrow/rust/otap-dataflow` engine for high-performance telemetry data processing. Rather than designing new Rust component patterns, Phase 4 creates a bridge between the Go collector's component interfaces and otap-dataflow's proven EffectHandler architecture.

## Core Integration Strategy

### Target Architecture: otap-dataflow Bridge
Phase 4 creates a translation layer between:

```
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
- Components use their `EffectHandler<PData>` for runtime interaction (Send vs !Send choice)

## Rust Runtime Configuration

### Configuration Structure
```yaml
rust_runtime:
  executor:
    type: "tokio_local"  # Local task executor
  rust2go:
    queue_size: 65536    # Shared memory ring buffer size
```

### Implementation
```rust
#[derive(Deserialize, Serialize)]
pub struct RustRuntimeConfig {
    #[serde(default)]
    pub executor: ExecutorConfig,
    #[serde(default)]
    pub rust2go: Rust2goConfig,
}

#[derive(Deserialize, Serialize)]
pub struct ExecutorConfig {
    pub executor_type: String,
}

#[derive(Deserialize, Serialize)]
pub struct Rust2goConfig {
    pub queue_size: usize,
}

impl Default for RustRuntimeConfig {
    fn default() -> Self {
        Self {
            executor: ExecutorConfig::default(),
            rust2go: Rust2goConfig::default(),
        }
    }
}

impl Default for ExecutorConfig {
    fn default() -> Self {
        Self {
            executor_type: "tokio_local".to_string(),
        }
    }
}

impl Default for Rust2goConfig {
    fn default() -> Self {
        Self {
            queue_size: 65536,
        }
    }
}

pub fn default_config() -> RustRuntimeConfig {
    RustRuntimeConfig::default()
}
```

## otap-dataflow Integration Architecture

### Factory Traits

For the interaction between Rust and Go, each pipeline component will have a 
corresponding bridge. The example below is for the processor interface, and
we expect to have a similar bridge for the other components.

Note that the on the Rust side, we use the [linkme crate](https://docs.rs/linkme/latest/linkme/) to 
register factories at link time.

```rust
#[rust2go::r2g] // Queue size configured via runtime config
pub trait Rust2goProcessorBridge {
    #[mem]
    async fn new_processor_factory(
        &mut self,
    ) -> Result<u64, String>; // Returns factory handle ID
    
    #[mem]
    async fn create_traces_processor(
        &mut self,
        factory_id: u64,
        config: &[u8],        // Component configuration (JSON)
    ) -> Result<u64, String>; // Returns traces processor instance handle ID
    
    #[mem]
    async fn create_metrics_processor(
        &mut self,
        factory_id: u64,
        config: &[u8],        // Component configuration (JSON)
    ) -> Result<u64, String>; // Returns metrics processor instance handle ID
    
    #[mem]
    async fn create_logs_processor(
        &mut self,
        factory_id: u64,
        config: &[u8],        // Component configuration (JSON)
    ) -> Result<u64, String>; // Returns logs processor instance handle ID
    
    #[mem] 
    async fn process_traces(
        &mut self,
        processor_id: u64,
        traces_data: &[u8],   // OTLP traces protobuf data
    ) -> Result<(), Error>;   // Structured error handling
    
    #[mem] 
    async fn process_metrics(
        &mut self,
        processor_id: u64,
        metrics_data: &[u8],  // OTLP metrics protobuf data
    ) -> Result<(), Error>;   // Structured error handling
    
    #[mem] 
    async fn process_logs(
        &mut self,
        processor_id: u64,
        logs_data: &[u8],     // OTLP logs protobuf data
    ) -> Result<(), Error>;   // Structured error handling
}
```

### Factory Integration
```rust
impl Rust2goProcessorBridge for Rust2goBridgeImpl {
    async fn create_processor_factory(&mut self, name: &str, runtime_config: &[u8]) -> Result<u64, String> {
        // Parse runtime configuration
        let runtime_cfg: RustRuntimeConfig = serde_json::from_slice(runtime_config)
            .map_err(|e| format!("runtime config parse error: {}", e))?;
            
        // Find factory in the distributed slice by name
        let factory = SHARED_PROCESSORS.iter()
            .find(|f| f.name == name)
            .ok_or_else(|| format!("processor factory not found: {}", name))?;
            
        // Store factory reference and return handle
        let factory_id = register_factory(factory.clone(), &runtime_cfg)?;
        Ok(factory_id)
    }
    
    async fn create_traces_processor(&mut self, factory_id: u64, config: &[u8]) -> Result<u64, String> {
        // Get factory from handle
        let factory = get_factory_by_id(factory_id)
            .ok_or_else(|| "invalid factory handle".to_string())?;
            
        // Parse component configuration
        let component_config: serde_json::Value = serde_json::from_slice(config)
            .map_err(|e| format!("component config parse error: {}", e))?;
            
        // Create processor instance using factory (specialized for traces)
        let processor = (factory.create)(&component_config);
        
        // Register as traces processor and return handle
        let processor_id = register_traces_processor(processor)?;
        Ok(processor_id)
    }
    
    async fn create_metrics_processor(&mut self, factory_id: u64, config: &[u8]) -> Result<u64, String> {
        ...
    }
    
    async fn create_logs_processor(&mut self, factory_id: u64, config: &[u8]) -> Result<u64, String> {
        ...
    }
    
    async fn process_traces(&mut self, processor_id: u64, traces_data: &[u8]) -> Result<(), Error> {
        // Get traces processor by handle
        let processor = get_traces_processor_by_id_mut(processor_id)
            .ok_or_else(|| Error::InvalidHandle(processor_id))?;
            
        // Convert OTLP traces protobuf to otap-dataflow format
        let traces_message = convert_otlp_traces_to_dataflow_message(traces_data)
            .map_err(|e| Error::ConversionError(format!("traces conversion error: {}", e)))?;
        
        // Create EffectHandler for this processing operation
        let mut effect_handler = create_effect_handler_for_processing()
            .map_err(|e| Error::ProcessingError(format!("effect handler creation error: {}", e)))?;
        
        // Process via otap-dataflow Processor trait
        processor.process(traces_message, &mut effect_handler)
            .await
            .map_err(|e| Error::ProcessingError(format!("traces processing error: {}", e)))?;
            
        // Processing complete - data flows through EffectHandler to next component
        Ok(())
    }
    
    async fn process_metrics(&mut self, processor_id: u64, metrics_data: &[u8]) -> Result<(), Error> {
        ... 
    }
    
    async fn process_logs(&mut self, processor_id: u64, logs_data: &[u8]) -> Result<(), Error> {
        ...
    }
}
```

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
- Receiver, Exporter, Processor compnents (receiver.Factory, exporter.Factory, processor.Factory, ...)
- Consumer interface translation (consumer.Traces, consumer.Logs, consumer.Metrics, ...)
- Integration with existing otap-dataflow components for OTLP pdata (Go) to OTAP pdata (Rust)
- Runtime configuration for Rust execution environment using `otap-dataflow`
- Rust2go integration for passing OTLP bytes in consumers.# Phase 4: Pipeline Components Integration with otap-dataflow

## Overview

Phase 4 integrates the OpenTelemetry Collector with the existing `otel-arrow/rust/otap-dataflow` engine for high-performance telemetry data processing. Rather than designing new Rust component patterns, Phase 4 creates a bridge between the Go collector's component interfaces and otap-dataflow's proven EffectHandler architecture.

## Pipeline Component Factory Architecture

### Factory Pattern Extension from Phase 3

Phase 4 extends the factory pattern established in Phase 3 to pipeline components (processors, exporters, receivers). Each component type implements its specialized factory interface while maintaining the same core patterns:

**Processor Factory Implementation**:
```go
// ProcessorFactory implements processor.Factory
type ProcessorFactory struct {
    componentType component.Type
    stability     component.StabilityLevel
}

func NewProcessorFactory() processor.Factory {
    return &ProcessorFactory{
        componentType: component.MustNewType("rust_batch_processor"),
        stability:     component.StabilityLevelDevelopment,
    }
}

// Type() returns component type for factory registration
func (f *ProcessorFactory) Type() component.Type {
    return f.componentType
}

// CreateDefaultConfig() returns Phase 2 configuration wrapper
func (f *ProcessorFactory) CreateDefaultConfig() component.Config {
    jsonPtr := C.rust_processor_default_config()
    defer C.free_rust_string(jsonPtr)
    
    jsonStr := C.GoString(jsonPtr)
    return &RustComponentConfig{rawJSON: jsonStr}
}

// CreateTracesProcessor implements processor.Factory interface
func (f *ProcessorFactory) CreateTracesProcessor(
    ctx context.Context,
    set processor.Settings,
    cfg component.Config,
    nextConsumer consumer.Traces,
) (processor.Traces, error) {
    rustCfg, ok := cfg.(*RustComponentConfig)
    if !ok {
        return nil, fmt.Errorf("invalid config type")
    }
    
    // Create Go wrapper that will bridge to otap-dataflow
    return &rustTracesProcessor{
        id:           set.ID,
        config:       rustCfg,
        nextConsumer: nextConsumer,
        factoryID:    0,  // Will be set during Start()
        processorID:  0,  // Will be set during Start()
    }, nil
}

// Similar implementations for CreateMetricsProcessor, CreateLogsProcessor
```

### Rust Factory Registration with otap-dataflow

Rust factories use linkme registration to provide otap-dataflow components:

```rust
use linkme::distributed_slice;
use otap_dataflow::shared::{Processor, EffectHandler};

// Distributed slice for processor factory registration
#[distributed_slice]
pub static RUST_PROCESSORS: [&'static dyn ProcessorFactory] = [..];

pub trait ProcessorFactory: Send + Sync {
    fn component_type(&self) -> &'static str;
    fn create_default_config(&self) -> String;
    fn validate_config(&self, config_json: &str) -> Result<(), String>;
    
    // Create otap-dataflow processor instance
    fn create_processor(&self, config: &str) -> Result<Box<dyn Processor<PData>>, String>;
}

// Factory registration using linkme
#[distributed_slice(RUST_PROCESSORS)]
static BATCH_PROCESSOR_FACTORY: &'static dyn ProcessorFactory = &BatchProcessorFactory;

pub struct BatchProcessorFactory;

impl ProcessorFactory for BatchProcessorFactory {
    fn component_type(&self) -> &'static str {
        "rust_batch_processor"
    }
    
    fn create_default_config(&self) -> String {
        let config = BatchProcessorConfig::default();
        serde_json::to_string(&config).unwrap_or_else(|_| "{}".to_string())
    }
    
    fn validate_config(&self, config_json: &str) -> Result<(), String> {
        let config: BatchProcessorConfig = serde_json::from_str(config_json)
            .map_err(|e| format!("serde parse error: {}", e))?;
        config.validate()
    }
    
    fn create_processor(&self, config: &str) -> Result<Box<dyn Processor<PData>>, String> {
        let config: BatchProcessorConfig = serde_json::from_str(config)?;
        
        // Create otap-dataflow processor with EffectHandler
        let processor = BatchProcessor::new(config)?;
        Ok(Box::new(processor))
    }
}

// BatchProcessor implements otap-dataflow Processor trait
pub struct BatchProcessor {
    config: BatchProcessorConfig,
    effect_handler: Option<Box<dyn EffectHandler<PData>>>,
}

impl Processor<PData> for BatchProcessor {
    async fn process(&mut self, data: PData, effect_handler: &mut dyn EffectHandler<PData>) -> Result<(), ProcessingError> {
        // Implementation uses otap-dataflow patterns
        // Process arrow record batches using batch configuration
        // Send results through effect_handler for next component
    }
}
```

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

```
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
- Components use their `EffectHandler<PData>` for runtime interaction (Send vs !Send choice)

## Rust Runtime Configuration

### Configuration Structure
```yaml
rust_runtime:
  executor:
    type: "tokio_local"  # Local task executor
  rust2go:
    queue_size: 65536    # Shared memory ring buffer size
```

### Implementation
```rust
#[derive(Deserialize, Serialize)]
pub struct RustRuntimeConfig {
    #[serde(default)]
    pub executor: ExecutorConfig,
    #[serde(default)]
    pub rust2go: Rust2goConfig,
}

#[derive(Deserialize, Serialize)]
pub struct ExecutorConfig {
    pub executor_type: String,
}

#[derive(Deserialize, Serialize)]
pub struct Rust2goConfig {
    pub queue_size: usize,
}

impl Default for RustRuntimeConfig {
    fn default() -> Self {
        Self {
            executor: ExecutorConfig::default(),
            rust2go: Rust2goConfig::default(),
        }
    }
}

impl Default for ExecutorConfig {
    fn default() -> Self {
        Self {
            executor_type: "tokio_local".to_string(),
        }
    }
}

impl Default for Rust2goConfig {
    fn default() -> Self {
        Self {
            queue_size: 65536,
        }
    }
}

pub fn default_config() -> RustRuntimeConfig {
    RustRuntimeConfig::default()
}
```

## otap-dataflow Integration Architecture

### Factory Traits

For the interaction between Rust and Go, each pipeline component will have a 
corresponding bridge. The example below is for the processor interface, and
we expect to have a similar bridge for the other components.

Note that the on the Rust side, we use the [linkme crate](https://docs.rs/linkme/latest/linkme/) to 
register factories at link time.

```rust
#[rust2go::r2g] // Queue size configured via runtime config
pub trait Rust2goProcessorBridge {
    #[mem]
    async fn new_processor_factory(
        &mut self,
    ) -> Result<u64, String>; // Returns factory handle ID
    
    #[mem]
    async fn create_traces_processor(
        &mut self,
        factory_id: u64,
        config: &[u8],        // Component configuration (JSON)
    ) -> Result<u64, String>; // Returns traces processor instance handle ID
    
    #[mem]
    async fn create_metrics_processor(
        &mut self,
        factory_id: u64,
        config: &[u8],        // Component configuration (JSON)
    ) -> Result<u64, String>; // Returns metrics processor instance handle ID
    
    #[mem]
    async fn create_logs_processor(
        &mut self,
        factory_id: u64,
        config: &[u8],        // Component configuration (JSON)
    ) -> Result<u64, String>; // Returns logs processor instance handle ID
    
    #[mem] 
    async fn process_traces(
        &mut self,
        processor_id: u64,
        traces_data: &[u8],   // OTLP traces protobuf data
    ) -> Result<(), Error>;   // Structured error handling
    
    #[mem] 
    async fn process_metrics(
        &mut self,
        processor_id: u64,
        metrics_data: &[u8],  // OTLP metrics protobuf data
    ) -> Result<(), Error>;   // Structured error handling
    
    #[mem] 
    async fn process_logs(
        &mut self,
        processor_id: u64,
        logs_data: &[u8],     // OTLP logs protobuf data
    ) -> Result<(), Error>;   // Structured error handling
}
```

### Factory Integration
```rust
impl Rust2goProcessorBridge for Rust2goBridgeImpl {
    async fn create_processor_factory(&mut self, name: &str, runtime_config: &[u8]) -> Result<u64, String> {
        // Parse runtime configuration
        let runtime_cfg: RustRuntimeConfig = serde_json::from_slice(runtime_config)
            .map_err(|e| format!("runtime config parse error: {}", e))?;
            
        // Find factory in the distributed slice by name
        let factory = SHARED_PROCESSORS.iter()
            .find(|f| f.name == name)
            .ok_or_else(|| format!("processor factory not found: {}", name))?;
            
        // Store factory reference and return handle
        let factory_id = register_factory(factory.clone(), &runtime_cfg)?;
        Ok(factory_id)
    }
    
    async fn create_traces_processor(&mut self, factory_id: u64, config: &[u8]) -> Result<u64, String> {
        // Get factory from handle
        let factory = get_factory_by_id(factory_id)
            .ok_or_else(|| "invalid factory handle".to_string())?;
            
        // Parse component configuration
        let component_config: serde_json::Value = serde_json::from_slice(config)
            .map_err(|e| format!("component config parse error: {}", e))?;
            
        // Create processor instance using factory (specialized for traces)
        let processor = (factory.create)(&component_config);
        
        // Register as traces processor and return handle
        let processor_id = register_traces_processor(processor)?;
        Ok(processor_id)
    }
    
    async fn create_metrics_processor(&mut self, factory_id: u64, config: &[u8]) -> Result<u64, String> {
        ...
    }
    
    async fn create_logs_processor(&mut self, factory_id: u64, config: &[u8]) -> Result<u64, String> {
        ...
    }
    
    async fn process_traces(&mut self, processor_id: u64, traces_data: &[u8]) -> Result<(), Error> {
        // Get traces processor by handle
        let processor = get_traces_processor_by_id_mut(processor_id)
            .ok_or_else(|| Error::InvalidHandle(processor_id))?;
            
        // Convert OTLP traces protobuf to otap-dataflow format
        let traces_message = convert_otlp_traces_to_dataflow_message(traces_data)
            .map_err(|e| Error::ConversionError(format!("traces conversion error: {}", e)))?;
        
        // Create EffectHandler for this processing operation
        let mut effect_handler = create_effect_handler_for_processing()
            .map_err(|e| Error::ProcessingError(format!("effect handler creation error: {}", e)))?;
        
        // Process via otap-dataflow Processor trait
        processor.process(traces_message, &mut effect_handler)
            .await
            .map_err(|e| Error::ProcessingError(format!("traces processing error: {}", e)))?;
            
        // Processing complete - data flows through EffectHandler to next component
        Ok(())
    }
    
    async fn process_metrics(&mut self, processor_id: u64, metrics_data: &[u8]) -> Result<(), Error> {
        ... 
    }
    
    async fn process_logs(&mut self, processor_id: u64, logs_data: &[u8]) -> Result<(), Error> {
        ...
    }
}
```

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
- Receiver, Exporter, Processor compnents (receiver.Factory, exporter.Factory, processor.Factory, ...)
- Consumer interface translation (consumer.Traces, consumer.Logs, consumer.Metrics, ...)
- Integration with existing otap-dataflow components for OTLP pdata (Go) to OTAP pdata (Rust)
- Runtime configuration for Rust execution environment using `otap-dataflow`
- Rust2go integration for passing OTLP bytes in consumers.