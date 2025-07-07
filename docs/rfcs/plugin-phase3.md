# Phase 3: Go-Side Extension Component Lifecycle with rust2go

## Overview

Phase 3 implements the Go mechanics for extension component lifecycle using rust2go FFI, building on Phase 2's configuration design. This phase focuses on proper separation between component creation (with config validation) and component activation, using placeholders for complex runtime sharing while establishing solid patterns for the `extension.Factory` interface.

## Design Goals

1. **Clear Lifecycle Separation**: Distinct Create vs Start phases matching collector's component graph building
2. **Go Extension Factory**: Complete implementation of `extension.Factory` interface  
3. **rust2go Integration**: Use rust2go for high-performance component lifecycle FFI calls
4. **Placeholder Approach**: Simple structs for Context and Host to focus on core mechanics
5. **Extension Focus**: Start with extensions to avoid pipeline data complexities before Phase 4

## Component Factory Architecture

### Extension Factory Implementation

Phase 3 implements the complete `extension.Factory` interface, using the factory pattern that will be extended in Phase 4 for pipeline components. The factory bridges Go's component system and Rust's static registration.

**Key points:**
- Implements the `extension.Factory` interface.
- Returns the component type for registration.
- Uses Rust FFI to get the default config (Phase 2 pattern).
- Creates Go wrapper components with validated configuration; Rust instance is created later during Start.

### Rust Factory Registration (linkme)

Rust components use `linkme` for static registration, complementing Go's manual registration.

**Key points:**
- Uses distributed slices for extension factory registration at link time
- Defines a factory trait for extensions, with methods for type, default config, validation, and instance creation
- Registers factories statically using linkme
- Each factory provides type information, default config, validation, and instance creation logic

### Factory Discovery and Integration

The factory system bridges Go's explicit registration and Rust's static registration.

**Key points:**
- Builder phase: `cargo` fields identify Rust extensions for factory generation
- Configuration phase: Factory `CreateDefaultConfig()` returns `RustComponentConfig` wrappers
- Runtime phase: Factory `Create()` builds Go wrappers, `Start()` creates Rust instances via rust2go
- Pipeline phase: Same pattern extends to processor/exporter/receiver factories

## Component Lifecycle Separation

Component lifecycle is separated into two phases:

**Step 1: Component Creation (Build Time)**
- During collector startup, when building the component graph
- Instantiates Go wrapper components and validates all configurations
- Only configuration validation occurs on the Rust side (from Phase 2)
- No Rust component instance is created yet; only the Go wrapper exists

**Step 2: Component Activation (Runtime)**
- After all components are created, during ordered startup
- Creates and starts Rust component instances with validated configs
- Calls rust2go create_and_start() for Rust interaction
- Rust handle is stored for later shutdown

## Core Architecture

### Go Extension Factory Implementation
The Go extension factory is responsible for:

- Implementing the `extension.Factory` interface
- Returning the component type for registration
- Using Rust FFI to get the default config (from Phase 2)
- Creating Go wrapper components with validated configuration (Rust instance is created later during Start)
- Logging creation events for observability

All Rust FFI calls for instance creation are deferred until the Start phase, ensuring configuration is validated and the Go wrapper is ready before any Rust component is started.

### Extension Instance Implementation

The extension instance implementation manages the lifecycle of a Rust-backed extension component in Go, focusing on:

- **Lifecycle State**: Maintains component ID, logger, validated config, and runtime state (Rust handle, started flag).
- **Start Phase**:
    - Checks if already started; logs startup event.
    - Prepares simplified context and host data (as JSON) for FFI transfer.
    - Calls Rust via rust2go to create and start the component instance, passing pre-validated config.
    - Handles errors from FFI calls and propagates Rust error messages.
    - Stores the Rust handle for later shutdown and marks as started.
- **Shutdown Phase**:
    - Checks if already stopped; logs shutdown event.
    - Always cleans up Go state, even if Rust shutdown fails.
    - Prepares context for shutdown and calls Rust via rust2go to clean up the instance.
    - Logs errors but does not fail shutdown if Rust cleanup fails.
- **Error Handling**: All FFI errors and Rust error messages are wrapped and logged for observability.
- **Placeholder Data**: Uses placeholder structs for FFIContext and FFIHost, see phase 5.

This design ensures clear separation between Go and Rust responsibilities, robust error handling, and a clean lifecycle for extension components across the FFI boundary.

### rust2go Integration Layer

````go
// callRustCreateAndStart - wrapper for rust2go async create_and_start call
func callRustCreateAndStart(ctx, host, config string) (*RustResult, error) {
    // This will be generated by rust2go based on the trait definition
    // Showing the expected Go interface pattern
    
    ctxCStr := C.CString(ctx)
    hostCStr := C.CString(host)  
    configCStr := C.CString(config)
    
    defer func() {
        C.free(unsafe.Pointer(ctxCStr))
        C.free(unsafe.Pointer(hostCStr))
        C.free(unsafe.Pointer(configCStr))
    }()
    
    // rust2go async call - this would be generated
    var cResult C.RustResult
    ret := C.rust_extension_create_and_start_async(ctxCStr, hostCStr, configCStr, &cResult)
    
    if ret != 0 {
        return nil, fmt.Errorf("rust2go call failed with code %d", ret)
    }
    
    result := &RustResult{
        Handle: cResult.handle,
    }
    
    if cResult.error_ptr != nil {
        defer C.free_rust_string(cResult.error_ptr)
        result.ErrorMsg = C.GoString(cResult.error_ptr)
    }
    
    return result, nil
}

// callRustShutdown - wrapper for rust2go async shutdown call  
func callRustShutdown(handle unsafe.Pointer, ctx string) error {
    if handle == nil {
        return fmt.Errorf("cannot shutdown: nil rust handle")
    }
    
    ctxCStr := C.CString(ctx)
    defer C.free(unsafe.Pointer(ctxCStr))
    
    // rust2go async call - this would be generated
    var cResult C.RustResult
    ret := C.rust_extension_shutdown_async(handle, ctxCStr, &cResult)
    
    if ret != 0 {
        return fmt.Errorf("rust2go shutdown call failed with code %d", ret)
    }
    
    if cResult.error_ptr != nil {
        defer C.free_rust_string(cResult.error_ptr)
        return fmt.Errorf("rust shutdown failed: %s", C.GoString(cResult.error_ptr))
    }
    
    return nil
}
````

### Configuration Integration (From Phase 2)

````go
// RustComponentConfig from Phase 2 - reused here
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
    
    // Call Rust validation via FFI (Phase 2 pattern)
    jsonCStr := C.CString(string(jsonBytes))
    defer C.free(unsafe.Pointer(jsonCStr))
    
    errorPtr := C.rust_extension_validate_config(jsonCStr)
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
````

## Rust Side Placeholders (For Reference)

### Component Trait (Rust Placeholder)

````rust
// This is a placeholder - will be implemented by Rust team, see
// integration in phase 4 document.
use rust2go::r2g;
use serde::{Deserialize, Serialize};

#[derive(Debug, Deserialize, Serialize)]
pub struct FFIContext {
    ...
}

#[derive(Debug, Deserialize, Serialize)]
pub struct FFIHost {
    ...
}

#[derive(Debug, Deserialize, Serialize)]
pub struct ExtensionConfig {
    pub endpoint: Option<String>,
    pub timeout_ms: u64,
}

impl ExtensionConfig {
    pub fn validate(&self) -> Result<(), String> {
        // ...
        Ok(())
    }
}

#[rust2go::r2g]
pub trait ExtensionLifecycle {
    // Create AND Start in one call - config already validated in Phase 2
    #[mem]
    async fn create_and_start(
        &mut self,
        ctx: &str,      // JSON serialized FFIContext  
        host: &str,     // JSON serialized FFIHost
        config: &str,   // Pre-validated JSON configuration from Phase 2
    ) -> Result<(), String>;
    
    #[mem]
    async fn shutdown(
        &mut self,
        ctx: &str,      // JSON serialized FFIContext
    ) -> Result<(), String>;
}

````

### Configuration Validation (From Phase 2)

````rust
// Configuration validation (Phase 2) - separate from lifecycle
#[no_mangle]
pub extern "C" fn rust_extension_validate_config(json_ptr: *const c_char) -> *mut c_char {
    let json_str = unsafe { 
        CStr::from_ptr(json_ptr).to_str().unwrap_or("")
    };
    
    match serde_json::from_str::<ExtensionConfig>(json_str) {
        Ok(config) => {
            match config.validate() {
                Ok(()) => std::ptr::null_mut(), // Success
                Err(err) => CString::new(err).unwrap().into_raw()
            }
        }
        Err(err) => CString::new(format!("config parse error: {}", err))
            .unwrap().into_raw()
    }
}

#[no_mangle]
pub extern "C" fn rust_extension_default_config() -> *mut c_char {
    let config = ExtensionConfig {
        endpoint: Some("http://localhost:8080".to_string()),
        timeout_ms: 5000,
        enabled: true,
    };
    
    match serde_json::to_string(&config) {
        Ok(json) => CString::new(json).unwrap().into_raw(),
        Err(_) => CString::new("{}").unwrap().into_raw()
    }
}
````

## rust2go vs Traditional CGO

This design uses rust2go's generated bindings rather than manual CGO:

- **Generated Code**: rust2go creates the FFI layer from Rust trait definitions
- **Async Support**: Built-in goroutine integration for Rust async functions  
- **Memory Management**: Automatic handle lifecycle management
- **Performance**: Shared memory rings for high-throughput data transfer

Unlike traditional CGO where you manually write `#[no_mangle] extern "C"` functions, rust2go generates all FFI code from trait definitions, providing better safety and async integration.

## rust2go Handle Management

The rust handle represents a Rust component instance managed across the FFI boundary:

```go
// During Start() - rust2go creates handle
result, err := callRustCreateAndStart(ctxJSON, hostJSON, configJSON)
e.rustHandle = result.Handle  // Store for later use

// During Shutdown() - rust2go consumes handle  
err := callRustShutdown(e.rustHandle, ctxJSON)
e.rustHandle = nil  // Always clear, even on error
```

rust2go automatically manages the underlying `Box<ComponentInstance>` lifecycle, eliminating manual memory management across the FFI boundary.

## Implementation Steps

### Step 1: Basic Factory Structure

- [ ] Implement `extension.Factory` interface with proper Create/Start separation
- [ ] Create basic `rustExtension` struct with lifecycle state management
- [ ] Test factory registration with collector (no Rust calls yet)
- [ ] Verify component creation works independently of Rust components

### Step 2: FFI Placeholder Integration

- [ ] Define FFI data structures (FFIContext, FFIHost) as placeholders
- [ ] Implement JSON marshaling for FFI transfer
- [ ] Create placeholder CGO function signatures for rust2go integration
- [ ] Test JSON serialization round-trip for context and host data

### Step 3: Configuration Integration from Phase 2

- [ ] Integrate `RustComponentConfig` from Phase 2 unchanged
- [ ] Test configuration validation during `RustComponentConfig.Unmarshal()`
- [ ] Verify config validation happens before component creation
- [ ] Test error propagation from Rust config validation to collector

### Step 4: Component Lifecycle Implementation

- [ ] Implement `Start()` method with rust2go `create_and_start()` calls
- [ ] Implement `Shutdown()` method with proper cleanup and error handling
- [ ] Test lifecycle with mock/placeholder Rust responses
- [ ] Verify handle management and resource cleanup

### Step 5: rust2go Integration Layer

- [ ] Create wrapper functions for rust2go generated calls
- [ ] Implement proper memory management across FFI boundary
- [ ] Test async patterns with rust2go framework
- [ ] Validate error handling and result parsing


## Build Integration

### Go Module Dependencies

The Go module requires standard OpenTelemetry Collector dependencies:

**go.mod file:**

```go
module github.com/example/otel-rust-extension

go 1.21

require (
    go.opentelemetry.io/collector/component v0.113.0
    go.opentelemetry.io/collector/extension v0.113.0
    go.opentelemetry.io/collector/confmap v1.22.0
    go.uber.org/zap v1.26.0
)
```

**CGO Integration:**

The Go source files include CGO directives for linking with the rust2go generated library:

```go
/*
#cgo LDFLAGS: -L./rust/target/release -lrust_extension
#cgo CFLAGS: -I./rust/target/release
*/
import "C"
```

### CGO Header (Generated by rust2go)

````c
// rust_extension.h - generated by rust2go
#ifndef RUST_EXTENSION_H
#define RUST_EXTENSION_H

typedef struct {
    void* handle;
    char* error_ptr;
} RustResult;

// Configuration functions (from Phase 2)
char* rust_extension_default_config();
char* rust_extension_validate_config(const char* config_json);

// Lifecycle functions (rust2go async)
int rust_extension_create_and_start_async(
    const char* ctx_json, 
    const char* host_json, 
    const char* config_json,
    RustResult* result
);

int rust_extension_shutdown_async(
    void* handle, 
    const char* ctx_json,
    RustResult* result
);

// Memory management
void free_rust_string(char* ptr);

#endif
````

## Success Criteria

- [ ] Extension factory registers successfully with collector service
- [ ] Clear separation between Create (wrapper + config) and Start (Rust instance) phases
- [ ] Configuration validation from Phase 2 integrates seamlessly with Create step
- [ ] Start/Shutdown lifecycle works correctly with rust2go async calls
- [ ] Error messages propagate properly from Rust to Go collector logs
- [ ] No memory leaks in FFI boundary during normal and error scenarios
- [ ] Integration tests pass with real collector service startup/shutdown
- [ ] Placeholders clearly documented for Phase 4+ implementation
- [ ] Handle management works correctly for multiple extension instances
- [ ] Component ordering and dependency management works with collector service
- [ ] rust2go performance benefits realized compared to traditional CGO approaches

This Phase 3 design establishes the complete Go-side mechanics for extension lifecycle while maintaining clear separation of concerns and providing a solid foundation for Phase 4's data processing components.
