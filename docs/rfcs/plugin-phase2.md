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

Rust components define configuration using idiomatic serde derive macros, leveraging features such as:

- Default values using `#[serde(default)]` or custom functions
- Optional fields with conditional serialization
- Field renaming and flattening for compatibility with Go's mapstructure
- Custom validation via a `validate()` method on the config struct

This approach ensures that configuration is both expressive and familiar to Rust developers, while supporting all necessary OpenTelemetry patterns.

## Factory Configuration Integration

### Factory CreateDefaultConfig() Implementation

The configuration design supports the factory pattern from Phase 1. The `CreateDefaultConfig()` method in component factories returns configuration structs with default values and parsing metadata. On the Go side, this involves calling a Rust FFI function to obtain a default config as JSON, which is then wrapped for confmap integration. On the Rust side, factories register themselves and provide default config and validation logic, using serde for serialization and deserialization.

### Configuration Type System Integration

The configuration structs defined in Phase 2 serve dual roles in the factory system:

1. **Default Value Source**: `CreateDefaultConfig()` returns structs with proper defaults
2. **Validation Schema**: Serde derive macros provide parsing rules equivalent to mapstructure tags

This creates a clean separation where:

- **Rust side** defines configuration schema using idiomatic serde patterns
- **Go side** wraps Rust configuration in `RustComponentConfig` for confmap integration
- **Factory system** bridges both through FFI calls to Rust default/validation functions

### FFI Interface: JSON + String Errors

The Go and Rust sides communicate via simple C-compatible FFI functions. These functions exchange configuration data as JSON strings and return error messages as C strings. Memory management is handled explicitly to avoid leaks. This approach keeps the interface simple and language-agnostic.

### Go Side: Custom confmap Integration

On the Go side, a wrapper struct integrates Rust validation with the confmap system. The process involves:

- Unmarshaling the config from confmap into a map
- Marshaling the map to JSON for Rust validation via FFI
- Handling errors returned from Rust and reporting them through the standard Go error pipeline
- Storing the validated JSON for runtime use

### Serde Feature Mapping

Serde's annotation system covers all mapstructure features used in OpenTelemetry, including:

- Field renaming
- Conditional serialization
- Embedded struct flattening
- Skipping fields
- Collecting unknown fields
- Field defaults
- Custom validation after deserialization

## Type System Compatibility

For edge cases, such as Go-Rust type compatibility (e.g., durations), custom serde serializers can be used to ensure that types are represented in a way that both languages understand. This may involve serializing durations as strings and providing custom parsing logic.

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

Validation is performed by implementing a `validate()` method on each config struct. This method can check for required fields, cross-field constraints, and delegate validation to nested configs. Errors are returned as descriptive strings for integration with Go's error reporting.

## Key Design Decisions

**Standard Serde Over Custom Abstractions**: Use serde derive macros and standard annotations to leverage a mature ecosystem, avoid custom code, and provide all needed features.

**JSON Serialization for FFI**: Use JSON for configuration data exchange, as it is language-agnostic and performance is not critical at startup.

**String-Based Error Reporting**: Return error messages as C strings for a simple FFI interface and clean Go error handling.

**Custom Validation Trait**: We use the [`serde_validate::Validate` trait](https://docs.rs/serde-validate/latest/serde_validate/trait.Validate.html) to separate parsing from business logic, support complex validation, and integrate with OpenTelemetry error reporting.

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
