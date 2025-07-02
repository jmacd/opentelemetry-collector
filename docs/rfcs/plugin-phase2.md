# Phase 2: Runtime Configuration Design for Rust Components

## Overview

Phase 2 addresses the challenge of enabling Rust components to define and validate arbitrary configuration structs, similar to how Go components use mapstructure tags and custom validation. This phase builds on Phase 1's builder configuration foundation and establishes the runtime configuration patterns that will enable seamless integration between Go's confmap system and Rust's serde ecosystem.

## Design Goals

1. **Idiomatic Rust Configuration**: Use standard serde patterns rather than custom abstractions
2. **Full confmap Integration**: Rust validation integrates with existing OpenTelemetry configuration pipeline
3. **Arbitrary Configuration Support**: Rust components can define complex, custom configuration structs
4. **Consistent Error Reporting**: Validation errors flow through standard collector error reporting
5. **Zero Performance Impact**: Configuration parsing is startup-time only, JSON overhead is acceptable

## Core Architecture

### Rust Side: Standard Serde Patterns

Rust components define configuration using idiomatic serde derive macros:

```rust
use serde::{Deserialize, Serialize};
use std::time::Duration;
use std::collections::HashMap;

#[derive(Debug, Deserialize, Serialize)]
pub struct ProcessorConfig {
    // Default values using functions
    #[serde(default = "default_batch_size")]
    pub batch_size: usize,
    
    // Standard defaults using Default trait
    #[serde(default)]
    pub timeout: Duration,
    
    // Optional fields with conditional serialization
    #[serde(skip_serializing_if = "Option::is_none")]
    pub custom_headers: Option<HashMap<String, String>>,
    
    // Skip fields during deserialization
    #[serde(skip_deserializing)]
    pub internal_state: String,
    
    // Field renaming (equivalent to mapstructure tags)
    #[serde(rename = "batch_timeout")]
    pub timeout_duration: Duration,
    
    // Flattened structures (equivalent to mapstructure squash)
    #[serde(flatten)]
    pub common_config: CommonConfig,
}

impl ProcessorConfig {
    pub fn validate(&self) -> Result<(), String> {
        if self.batch_size == 0 {
            return Err("batch_size must be greater than 0".to_string());
        }
        if self.timeout.as_secs() > 300 {
            return Err("timeout cannot exceed 5 minutes".to_string());
        }
        Ok(())
    }
}

fn default_batch_size() -> usize { 8192 }
```

### FFI Interface: JSON + String Errors

Simple C-compatible functions bridge configuration between Go and Rust:

```rust
use std::ffi::{CStr, CString};
use std::os::raw::c_char;

#[no_mangle]
pub extern "C" fn processor_create_default_config() -> *mut c_char {
    let config = ProcessorConfig::default();
    match serde_json::to_string(&config) {
        Ok(json) => CString::new(json).unwrap().into_raw(),
        Err(_) => CString::new("{}").unwrap().into_raw()
    }
}

#[no_mangle]
pub extern "C" fn processor_validate_config(json_ptr: *const c_char) -> *mut c_char {
    let json_str = unsafe { 
        CStr::from_ptr(json_ptr).to_str().unwrap_or("")
    };
    
    match serde_json::from_str::<ProcessorConfig>(json_str) {
        Ok(config) => {
            match config.validate() {
                Ok(()) => std::ptr::null_mut(), // Success - null pointer
                Err(err) => CString::new(err).unwrap().into_raw()
            }
        }
        Err(err) => CString::new(format!("serde parse error: {}", err))
            .unwrap().into_raw()
    }
}

#[no_mangle]
pub extern "C" fn free_rust_string(ptr: *mut c_char) {
    if !ptr.is_null() {
        unsafe { CString::from_raw(ptr) };
    }
}
```

### Go Side: Custom confmap Integration

Go wrapper integrates Rust validation with the existing confmap system:

```go
type RustComponentConfig struct {
    rawJSON string
}

func (c *RustComponentConfig) Unmarshal(conf *confmap.Conf) error {
    // Get raw config as map
    var rawConfig map[string]any
    if err := conf.Unmarshal(&rawConfig); err != nil {
        return err
    }
    
    // Convert to JSON for Rust
    jsonBytes, err := json.Marshal(rawConfig)
    if err != nil {
        return fmt.Errorf("failed to marshal config to JSON: %w", err)
    }
    
    // Call Rust validation via FFI
    jsonCStr := C.CString(string(jsonBytes))
    defer C.free(unsafe.Pointer(jsonCStr))
    
    errorPtr := C.processor_validate_config(jsonCStr)
    if errorPtr != nil {
        defer C.free_rust_string(errorPtr)
        errorMsg := C.GoString(errorPtr)
        return fmt.Errorf("rust config validation failed: %s", errorMsg)
    }
    
    // Store validated JSON for runtime use
    c.rawJSON = string(jsonBytes)
    return nil
}

func (c *RustComponentConfig) Marshal(conf *confmap.Conf) error {
    if c.rawJSON == "" {
        return nil
    }
    
    var rawConfig map[string]any
    if err := json.Unmarshal([]byte(c.rawJSON), &rawConfig); err != nil {
        return err
    }
    
    return conf.Marshal(rawConfig)
}

// Factory integration
func (f *rustProcessorFactory) CreateDefaultConfig() component.Config {
    jsonPtr := C.processor_create_default_config()
    defer C.free_rust_string(jsonPtr)
    
    jsonStr := C.GoString(jsonPtr)
    return &RustComponentConfig{rawJSON: jsonStr}
}
```

## Serde Feature Mapping

Phase 2 leverages serde's comprehensive annotation system to provide equivalents for all mapstructure features used in OpenTelemetry:

| mapstructure Feature | Serde Equivalent | Usage |
|---------------------|------------------|-------|
| `mapstructure:"field"` | `#[serde(rename = "field")]` | Field name mapping |
| `mapstructure:",omitempty"` | `#[serde(skip_serializing_if = "...")]` | Conditional serialization |
| `mapstructure:",squash"` | `#[serde(flatten)]` | Embedded struct flattening |
| `mapstructure:"-"` | `#[serde(skip)]` | Skip field entirely |
| `mapstructure:",remain"` | `#[serde(flatten)] HashMap<String, serde_json::Value>` | Collect unknown fields |
| Default values | `#[serde(default)]` or `#[serde(default = "func")]` | Field defaults |
| Custom validation | `impl Validate for Config` | Post-deserialization validation |

## Type System Compatibility

For edge cases requiring Go-Rust type compatibility:

```rust
// Custom serde for Duration to match Go's expectations
#[derive(Debug, Deserialize, Serialize)]
pub struct Config {
    #[serde(with = "duration_string")]
    pub timeout: Duration,
}

mod duration_string {
    use serde::{Deserialize, Deserializer, Serializer};
    use std::time::Duration;

    pub fn serialize<S>(duration: &Duration, serializer: S) -> Result<S::Ok, S::Error>
    where S: Serializer {
        let s = format!("{}ms", duration.as_millis());
        serializer.serialize_str(&s)
    }

    pub fn deserialize<'de, D>(deserializer: D) -> Result<Duration, D::Error>
    where D: Deserializer<'de> {
        let s = String::deserialize(deserializer)?;
        // Parse duration string (e.g., "5s", "100ms")
        parse_duration(&s).map_err(serde::de::Error::custom)
    }
}
```

## Error Handling Strategy

Simple string-based error reporting across FFI boundary:

1. **Serde Parse Errors**: JSON deserialization failures become `"serde parse error: <details>"`
2. **Validation Errors**: Custom validation returns descriptive error messages
3. **Go Integration**: Errors flow through standard `confmap.Conf.Unmarshal()` error handling
4. **Error Context**: Go side can add configuration path context to Rust errors

## Performance Considerations

- **Configuration Parsing**: Startup-time only, JSON overhead is negligible
- **Memory Management**: Explicit cleanup with `free_rust_string()` for error messages
- **FFI Calls**: Minimal - only during configuration validation at startup
- **JSON Serialization**: Standard practice for configuration exchange, well-optimized

## Integration Points

### Phase 1 Connection
- Uses cargo field specifications from Phase 1's Module struct
- Rust components identified by cargo field presence in builder configuration

### Phase 3 Connection  
- Configuration structs defined in Phase 2 will be used by build process in Phase 3
- Template generation in Phase 3 will reference configuration struct names

### Phase 4 Connection
- Component factories in Phase 4 will use RustComponentConfig wrapper pattern
- Runtime data processing will access validated configuration via rawJSON field

## Configuration Examples

### Simple Processor Configuration
```rust
#[derive(Debug, Deserialize, Serialize)]
pub struct BatchProcessorConfig {
    #[serde(default = "default_batch_size")]
    pub send_batch_size: usize,
    
    #[serde(default = "default_max_size")]
    pub send_batch_max_size: usize,
    
    #[serde(default = "default_timeout")]
    pub timeout: Duration,
}
```

### Complex Exporter Configuration
```rust
#[derive(Debug, Deserialize, Serialize)]
pub struct HTTPExporterConfig {
    pub endpoint: String,
    
    #[serde(default)]
    pub headers: HashMap<String, String>,
    
    #[serde(flatten)]
    pub tls_config: TLSConfig,
    
    #[serde(flatten)]
    pub retry_config: RetryConfig,
    
    #[serde(skip_serializing_if = "Option::is_none")]
    pub proxy_url: Option<String>,
}
```

## Validation Patterns

### Required Field Validation
```rust
impl HTTPExporterConfig {
    pub fn validate(&self) -> Result<(), String> {
        if self.endpoint.is_empty() {
            return Err("endpoint is required".to_string());
        }
        
        if !self.endpoint.starts_with("http") {
            return Err("endpoint must be a valid HTTP URL".to_string());
        }
        
        // Validate nested configs
        self.tls_config.validate()?;
        self.retry_config.validate()?;
        
        Ok(())
    }
}
```

### Cross-Field Validation
```rust
impl BatchProcessorConfig {
    pub fn validate(&self) -> Result<(), String> {
        if self.send_batch_size == 0 {
            return Err("send_batch_size must be greater than 0".to_string());
        }
        
        if self.send_batch_max_size != 0 && 
           self.send_batch_max_size < self.send_batch_size {
            return Err("send_batch_max_size must be >= send_batch_size when set".to_string());
        }
        
        if self.timeout.as_secs() == 0 {
            return Err("timeout must be greater than 0".to_string());
        }
        
        Ok(())
    }
}
```

## Key Design Decisions

### Standard Serde Over Custom Abstractions
**Decision**: Use serde derive macros and standard annotations
**Rationale**: 
- Leverages mature, well-tested ecosystem
- Familiar to Rust developers
- No custom abstractions to maintain
- Rich feature set covers all mapstructure equivalents

### JSON Serialization for FFI
**Decision**: Use JSON for configuration data exchange
**Rationale**:
- Configuration parsing is not performance-critical
- JSON provides language-agnostic data representation
- Serde JSON integration is highly optimized
- Avoids complex FFI data structure marshaling

### String-Based Error Reporting
**Decision**: Return error messages as C strings
**Rationale**:
- Simple FFI interface
- Integrates cleanly with Go error handling
- Avoids complex error code mappings
- Allows descriptive error messages

### Custom Validation Trait
**Decision**: Use validate() method pattern rather than serde validation
**Rationale**:
- Separates parsing from business logic validation
- Allows complex cross-field validation
- Common pattern in Rust configuration libraries
- Integrates cleanly with OpenTelemetry error reporting

## Implementation Plan

### Step 1: Core FFI Functions
- Implement basic create_default_config() and validate_config() C functions
- Add memory management with free_rust_string()
- Test JSON serialization round-trip

### Step 2: Go Integration
- Create RustComponentConfig wrapper struct
- Implement confmap.Unmarshaler and confmap.Marshaler interfaces
- Integration test with existing confmap validation pipeline

### Step 3: Configuration Examples
- Implement sample processor configuration struct
- Demonstrate serde annotations and validation patterns
- Test complex configuration scenarios

### Step 4: Type System Edge Cases  
- Handle Duration and other Go-specific types
- Custom serde serializers for compatibility
- Test with realistic OpenTelemetry configurations

## Testing Strategy

### Unit Testing
- Rust: Test serde serialization/deserialization and validation
- Go: Test confmap integration and error handling
- FFI: Test memory management and error propagation

### Integration Testing
- End-to-end configuration parsing from YAML to validated Rust structs
- Error reporting through confmap validation pipeline
- Complex configuration scenarios with nested structs

### Compatibility Testing
- Verify equivalent behavior between Go and Rust configuration validation
- Test edge cases like empty configs, invalid JSON, validation failures
- Performance benchmarking (though configuration parsing is not critical path)

## Success Criteria

- [ ] Rust components can define arbitrary configuration structs using standard serde
- [ ] Configuration validation integrates seamlessly with confmap error reporting  
- [ ] Complex configuration scenarios work equivalent to Go mapstructure patterns
- [ ] Error messages are clear and actionable for users
- [ ] No performance regression in configuration parsing
- [ ] Memory management across FFI boundary is leak-free

This design provides a solid foundation for Phase 2 that leverages the best of both ecosystems - OpenTelemetry's proven confmap system and Rust's mature serde configuration patterns.
# Phase 2: Runtime Configuration Design for Rust Components

## Overview

Phase 2 addresses the challenge of enabling Rust components to define and validate arbitrary configuration structs, similar to how Go components use mapstructure tags and custom validation. This phase builds on Phase 1's builder configuration foundation and establishes the runtime configuration patterns that will enable seamless integration between Go's confmap system and Rust's serde ecosystem.

## Design Goals

1. **Idiomatic Rust Configuration**: Use standard serde patterns rather than custom abstractions
2. **Full confmap Integration**: Rust validation integrates with existing OpenTelemetry configuration pipeline
3. **Arbitrary Configuration Support**: Rust components can define complex, custom configuration structs
4. **Consistent Error Reporting**: Validation errors flow through standard collector error reporting
5. **Zero Performance Impact**: Configuration parsing is startup-time only, JSON overhead is acceptable

## Core Architecture

### Rust Side: Standard Serde Patterns

Rust components define configuration using idiomatic serde derive macros:

```rust
use serde::{Deserialize, Serialize};
use std::time::Duration;
use std::collections::HashMap;

#[derive(Debug, Deserialize, Serialize)]
pub struct ProcessorConfig {
    // Default values using functions
    #[serde(default = "default_batch_size")]
    pub batch_size: usize,
    
    // Standard defaults using Default trait
    #[serde(default)]
    pub timeout: Duration,
    
    // Optional fields with conditional serialization
    #[serde(skip_serializing_if = "Option::is_none")]
    pub custom_headers: Option<HashMap<String, String>>,
    
    // Skip fields during deserialization
    #[serde(skip_deserializing)]
    pub internal_state: String,
    
    // Field renaming (equivalent to mapstructure tags)
    #[serde(rename = "batch_timeout")]
    pub timeout_duration: Duration,
    
    // Flattened structures (equivalent to mapstructure squash)
    #[serde(flatten)]
    pub common_config: CommonConfig,
}

impl ProcessorConfig {
    pub fn validate(&self) -> Result<(), String> {
        if self.batch_size == 0 {
            return Err("batch_size must be greater than 0".to_string());
        }
        if self.timeout.as_secs() > 300 {
            return Err("timeout cannot exceed 5 minutes".to_string());
        }
        Ok(())
    }
}

fn default_batch_size() -> usize { 8192 }
```

## Factory Configuration Integration

### Factory CreateDefaultConfig() Implementation

Phase 2's configuration design directly supports the factory pattern established in Phase 1. The `CreateDefaultConfig()` method in component factories returns configuration structs that encapsulate both the default values and the parsing metadata:

**Go Factory Integration**:

```go
// Factory integration with Phase 2 config patterns
func (f *rustProcessorFactory) CreateDefaultConfig() component.Config {
    // Call Rust FFI to get default config JSON
    jsonPtr := C.processor_create_default_config()
    defer C.free_rust_string(jsonPtr)
    
    jsonStr := C.GoString(jsonPtr)
    return &RustComponentConfig{rawJSON: jsonStr}
}

// Type() returns the component type for factory registration
func (f *rustProcessorFactory) Type() component.Type {
    return component.MustNewType("rust_batch_processor")
}
```

**Rust Factory Implementation (linkme registration)**:

```rust
use linkme::distributed_slice;

#[distributed_slice(RUST_PROCESSORS)]
static BATCH_PROCESSOR_FACTORY: &'static dyn ProcessorFactory = &BatchProcessorFactory;

pub struct BatchProcessorFactory;

impl ProcessorFactory for BatchProcessorFactory {
    fn component_type(&self) -> &'static str {
        "rust_batch_processor"
    }
    
    fn create_default_config(&self) -> String {
        let config = ProcessorConfig::default();
        serde_json::to_string(&config).unwrap_or_else(|_| "{}".to_string())
    }
    
    fn validate_config(&self, config_json: &str) -> Result<(), String> {
        let config: ProcessorConfig = serde_json::from_str(config_json)
            .map_err(|e| format!("serde parse error: {}", e))?;
        config.validate()
    }
}
```

### Configuration Type System Integration

The configuration structs defined in Phase 2 serve dual roles in the factory system:

1. **Default Value Source**: `CreateDefaultConfig()` returns structs with proper defaults
2. **Validation Schema**: Serde derive macros provide parsing rules equivalent to mapstructure tags

This creates a clean separation where:

- **Rust side** defines configuration schema using idiomatic serde patterns
- **Go side** wraps Rust configuration in `RustComponentConfig` for confmap integration
- **Factory system** bridges both through FFI calls to Rust default/validation functions

### Factory Integration Points

#### Phase 1 Connection

- Uses cargo field specifications from Phase 1's Module struct to identify Rust components
- Rust factories discovered via linkme registration complement Go factory registration
- Component type names in factory `Type()` method match builder configuration expectations

#### Phase 3 Connection

- Configuration structs defined in Phase 2 are returned by factory `CreateDefaultConfig()` in Phase 3
- Factory validation functions provide the FFI interface for runtime configuration validation
- Extension factories in Phase 3 will use this exact pattern for configuration management

#### Phase 4 Connection

- Pipeline component factories (processor, exporter, receiver) extend this pattern
- Configuration validation happens during factory `Create()` step via confmap integration
- Runtime data processing components access validated configuration via `rawJSON` field

### FFI Interface: JSON + String Errors

Simple C-compatible functions bridge configuration between Go and Rust:

```rust
use std::ffi::{CStr, CString};
use std::os::raw::c_char;

#[no_mangle]
pub extern "C" fn processor_create_default_config() -> *mut c_char {
    let config = ProcessorConfig::default();
    match serde_json::to_string(&config) {
        Ok(json) => CString::new(json).unwrap().into_raw(),
        Err(_) => CString::new("{}").unwrap().into_raw()
    }
}

#[no_mangle]
pub extern "C" fn processor_validate_config(json_ptr: *const c_char) -> *mut c_char {
    let json_str = unsafe { 
        CStr::from_ptr(json_ptr).to_str().unwrap_or("")
    };
    
    match serde_json::from_str::<ProcessorConfig>(json_str) {
        Ok(config) => {
            match config.validate() {
                Ok(()) => std::ptr::null_mut(), // Success - null pointer
                Err(err) => CString::new(err).unwrap().into_raw()
            }
        }
        Err(err) => CString::new(format!("serde parse error: {}", err))
            .unwrap().into_raw()
    }
}

#[no_mangle]
pub extern "C" fn free_rust_string(ptr: *mut c_char) {
    if !ptr.is_null() {
        unsafe { CString::from_raw(ptr) };
    }
}
```

### Go Side: Custom confmap Integration

Go wrapper integrates Rust validation with the existing confmap system:

```go
type RustComponentConfig struct {
    rawJSON string
}

func (c *RustComponentConfig) Unmarshal(conf *confmap.Conf) error {
    // Get raw config as map
    var rawConfig map[string]any
    if err := conf.Unmarshal(&rawConfig); err != nil {
        return err
    }
    
    // Convert to JSON for Rust
    jsonBytes, err := json.Marshal(rawConfig)
    if err != nil {
        return fmt.Errorf("failed to marshal config to JSON: %w", err)
    }
    
    // Call Rust validation via FFI
    jsonCStr := C.CString(string(jsonBytes))
    defer C.free(unsafe.Pointer(jsonCStr))
    
    errorPtr := C.processor_validate_config(jsonCStr)
    if errorPtr != nil {
        defer C.free_rust_string(errorPtr)
        errorMsg := C.GoString(errorPtr)
        return fmt.Errorf("rust config validation failed: %s", errorMsg)
    }
    
    // Store validated JSON for runtime use
    c.rawJSON = string(jsonBytes)
    return nil
}

func (c *RustComponentConfig) Marshal(conf *confmap.Conf) error {
    if c.rawJSON == "" {
        return nil
    }
    
    var rawConfig map[string]any
    if err := json.Unmarshal([]byte(c.rawJSON), &rawConfig); err != nil {
        return err
    }
    
    return conf.Marshal(rawConfig)
}

// Factory integration
func (f *rustProcessorFactory) CreateDefaultConfig() component.Config {
    jsonPtr := C.processor_create_default_config()
    defer C.free_rust_string(jsonPtr)
    
    jsonStr := C.GoString(jsonPtr)
    return &RustComponentConfig{rawJSON: jsonStr}
}
```

## Serde Feature Mapping

Phase 2 leverages serde's comprehensive annotation system to provide equivalents for all mapstructure features used in OpenTelemetry:

| mapstructure Feature | Serde Equivalent | Usage |
|---------------------|------------------|-------|
| `mapstructure:"field"` | `#[serde(rename = "field")]` | Field name mapping |
| `mapstructure:",omitempty"` | `#[serde(skip_serializing_if = "...")]` | Conditional serialization |
| `mapstructure:",squash"` | `#[serde(flatten)]` | Embedded struct flattening |
| `mapstructure:"-"` | `#[serde(skip)]` | Skip field entirely |
| `mapstructure:",remain"` | `#[serde(flatten)] HashMap<String, serde_json::Value>` | Collect unknown fields |
| Default values | `#[serde(default)]` or `#[serde(default = "func")]` | Field defaults |
| Custom validation | `impl Validate for Config` | Post-deserialization validation |

## Type System Compatibility

For edge cases requiring Go-Rust type compatibility:

```rust
// Custom serde for Duration to match Go's expectations
#[derive(Debug, Deserialize, Serialize)]
pub struct Config {
    #[serde(with = "duration_string")]
    pub timeout: Duration,
}

mod duration_string {
    use serde::{Deserialize, Deserializer, Serializer};
    use std::time::Duration;

    pub fn serialize<S>(duration: &Duration, serializer: S) -> Result<S::Ok, S::Error>
    where S: Serializer {
        let s = format!("{}ms", duration.as_millis());
        serializer.serialize_str(&s)
    }

    pub fn deserialize<'de, D>(deserializer: D) -> Result<Duration, D::Error>
    where D: Deserializer<'de> {
        let s = String::deserialize(deserializer)?;
        // Parse duration string (e.g., "5s", "100ms")
        parse_duration(&s).map_err(serde::de::Error::custom)
    }
}
```

## Error Handling Strategy

Simple string-based error reporting across FFI boundary:

1. **Serde Parse Errors**: JSON deserialization failures become `"serde parse error: <details>"`
2. **Validation Errors**: Custom validation returns descriptive error messages
3. **Go Integration**: Errors flow through standard `confmap.Conf.Unmarshal()` error handling
4. **Error Context**: Go side can add configuration path context to Rust errors

## Performance Considerations

- **Configuration Parsing**: Startup-time only, JSON overhead is negligible
- **Memory Management**: Explicit cleanup with `free_rust_string()` for error messages
- **FFI Calls**: Minimal - only during configuration validation at startup
- **JSON Serialization**: Standard practice for configuration exchange, well-optimized

## Configuration Examples

### Simple Processor Configuration
```rust
#[derive(Debug, Deserialize, Serialize)]
pub struct BatchProcessorConfig {
    #[serde(default = "default_batch_size")]
    pub send_batch_size: usize,
    
    #[serde(default = "default_max_size")]
    pub send_batch_max_size: usize,
    
    #[serde(default = "default_timeout")]
    pub timeout: Duration,
}
```

### Complex Exporter Configuration
```rust
#[derive(Debug, Deserialize, Serialize)]
pub struct HTTPExporterConfig {
    pub endpoint: String,
    
    #[serde(default)]
    pub headers: HashMap<String, String>,
    
    #[serde(flatten)]
    pub tls_config: TLSConfig,
    
    #[serde(flatten)]
    pub retry_config: RetryConfig,
    
    #[serde(skip_serializing_if = "Option::is_none")]
    pub proxy_url: Option<String>,
}
```

## Validation Patterns

### Required Field Validation
```rust
impl HTTPExporterConfig {
    pub fn validate(&self) -> Result<(), String> {
        if self.endpoint.is_empty() {
            return Err("endpoint is required".to_string());
        }
        
        if !self.endpoint.starts_with("http") {
            return Err("endpoint must be a valid HTTP URL".to_string());
        }
        
        // Validate nested configs
        self.tls_config.validate()?;
        self.retry_config.validate()?;
        
        Ok(())
    }
}
```

### Cross-Field Validation
```rust
impl BatchProcessorConfig {
    pub fn validate(&self) -> Result<(), String> {
        if self.send_batch_size == 0 {
            return Err("send_batch_size must be greater than 0".to_string());
        }
        
        if self.send_batch_max_size != 0 && 
           self.send_batch_max_size < self.send_batch_size {
            return Err("send_batch_max_size must be >= send_batch_size when set".to_string());
        }
        
        if self.timeout.as_secs() == 0 {
            return Err("timeout must be greater than 0".to_string());
        }
        
        Ok(())
    }
}
```

## Key Design Decisions

### Standard Serde Over Custom Abstractions
**Decision**: Use serde derive macros and standard annotations
**Rationale**: 
- Leverages mature, well-tested ecosystem
- Familiar to Rust developers
- No custom abstractions to maintain
- Rich feature set covers all mapstructure equivalents

### JSON Serialization for FFI
**Decision**: Use JSON for configuration data exchange
**Rationale**:
- Configuration parsing is not performance-critical
- JSON provides language-agnostic data representation
- Serde JSON integration is highly optimized
- Avoids complex FFI data structure marshaling

### String-Based Error Reporting
**Decision**: Return error messages as C strings
**Rationale**:
- Simple FFI interface
- Integrates cleanly with Go error handling
- Avoids complex error code mappings
- Allows descriptive error messages

### Custom Validation Trait
**Decision**: Use validate() method pattern rather than serde validation
**Rationale**:
- Separates parsing from business logic validation
- Allows complex cross-field validation
- Common pattern in Rust configuration libraries
- Integrates cleanly with OpenTelemetry error reporting

## Implementation Plan

### Step 1: Core FFI Functions
- Implement basic create_default_config() and validate_config() C functions
- Add memory management with free_rust_string()
- Test JSON serialization round-trip

### Step 2: Go Integration
- Create RustComponentConfig wrapper struct
- Implement confmap.Unmarshaler and confmap.Marshaler interfaces
- Integration test with existing confmap validation pipeline

### Step 3: Configuration Examples
- Implement sample processor configuration struct
- Demonstrate serde annotations and validation patterns
- Test complex configuration scenarios

### Step 4: Type System Edge Cases  
- Handle Duration and other Go-specific types
- Custom serde serializers for compatibility
- Test with realistic OpenTelemetry configurations

## Testing Strategy

### Unit Testing
- Rust: Test serde serialization/deserialization and validation
- Go: Test confmap integration and error handling
- FFI: Test memory management and error propagation

### Integration Testing
- End-to-end configuration parsing from YAML to validated Rust structs
- Error reporting through confmap validation pipeline
- Complex configuration scenarios with nested structs

### Compatibility Testing
- Verify equivalent behavior between Go and Rust configuration validation
- Test edge cases like empty configs, invalid JSON, validation failures
- Performance benchmarking (though configuration parsing is not critical path)

## Success Criteria

- [ ] Rust components can define arbitrary configuration structs using standard serde
- [ ] Configuration validation integrates seamlessly with confmap error reporting  
- [ ] Complex configuration scenarios work equivalent to Go mapstructure patterns
- [ ] Error messages are clear and actionable for users
- [ ] No performance regression in configuration parsing
- [ ] Memory management across FFI boundary is leak-free

This design provides a solid foundation for Phase 2 that leverages the best of both ecosystems - OpenTelemetry's proven confmap system and Rust's mature serde configuration patterns.
