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

Phase 3 implements the complete `extension.Factory` interface, demonstrating the factory pattern that will be extended in Phase 4 for pipeline components. The factory serves as the bridge between Go's component system and Rust's linkme-based static registration.

**Factory Interface Implementation**:
```go
// Factory implements extension.Factory with complete component.Factory interface
type Factory struct {
    componentType component.Type        // Returned by Type()
    stability     component.StabilityLevel
    logger        *zap.Logger
}

// Type() returns the component type for registration in generated components.go
func (f *Factory) Type() component.Type {
    return f.componentType
}

// CreateDefaultConfig() uses Phase 2 patterns to get default from Rust
func (f *Factory) CreateDefaultConfig() component.Config {
    jsonPtr := C.rust_extension_default_config()
    defer C.free_rust_string(jsonPtr)
    
    jsonStr := C.GoString(jsonPtr)
    return &RustComponentConfig{rawJSON: jsonStr}  // Phase 2 config wrapper
}

// Create() implements the factory creation pattern - no Rust instance yet
func (f *Factory) Create(ctx context.Context, set extension.Settings, cfg component.Config) (extension.Extension, error) {
    rustCfg, ok := cfg.(*RustComponentConfig)
    if !ok {
        return nil, fmt.Errorf("invalid config type")
    }
    
    // Only create Go wrapper - Rust component instance created during Start()
    return &rustExtension{
        id:        set.ID,
        config:    rustCfg,  // Config already validated by confmap during RustComponentConfig.Unmarshal()
        started:   false,
        rustHandle: nil,
    }, nil
}
```

### Rust Factory Registration (linkme)

Rust components use `linkme` for static registration, complementing Go's manual registration:

```rust
use linkme::distributed_slice;

// Distributed slice for extension factory registration at link time
#[distributed_slice]
pub static RUST_EXTENSIONS: [&'static dyn ExtensionFactory] = [..];

// Factory trait for extensions
pub trait ExtensionFactory: Send + Sync {
    fn component_type(&self) -> &'static str;
    fn create_default_config(&self) -> String;
    fn validate_config(&self, config_json: &str) -> Result<(), String>;
    fn create_instance(&self, config: &str) -> Result<Box<dyn ExtensionLifecycle>, String>;
}

// Static registration using linkme
#[distributed_slice(RUST_EXTENSIONS)]
static SAMPLE_EXTENSION_FACTORY: &'static dyn ExtensionFactory = &SampleExtensionFactory;

pub struct SampleExtensionFactory;

impl ExtensionFactory for SampleExtensionFactory {
    fn component_type(&self) -> &'static str {
        "rust_sample"
    }
    
    fn create_default_config(&self) -> String {
        let config = ExtensionConfig::default();
        serde_json::to_string(&config).unwrap_or_else(|_| "{}".to_string())
    }
    
    fn validate_config(&self, config_json: &str) -> Result<(), String> {
        let config: ExtensionConfig = serde_json::from_str(config_json)
            .map_err(|e| format!("serde parse error: {}", e))?;
        config.validate()
    }
    
    fn create_instance(&self, config: &str) -> Result<Box<dyn ExtensionLifecycle>, String> {
        let config: ExtensionConfig = serde_json::from_str(config)?;
        Ok(Box::new(SampleExtension::new(config)))
    }
}
```

### Factory Discovery and Integration

The factory system provides a bridge between Go's explicit registration and Rust's static registration:

1. **Builder Phase (Phase 1)**: `cargo` fields identify Rust extensions for factory generation
2. **Configuration Phase (Phase 2)**: Factory `CreateDefaultConfig()` returns `RustComponentConfig` wrappers
3. **Runtime Phase (Phase 3)**: Factory `Create()` builds Go wrappers, `Start()` creates Rust instances via rust2go
4. **Pipeline Phase (Phase 4)**: Same pattern extends to processor/exporter/receiver factories

## Component Lifecycle Separation

### Step 1: Component Creation (Build Time)
- **When**: During collector startup, when building component graph
- **Purpose**: Instantiate Go wrapper components and validate all configurations  
- **Rust Interaction**: Only configuration validation (from Phase 2)
- **No Rust Component Instance**: Go wrapper created, Rust component instance deferred

### Step 2: Component Activation (Runtime)
- **When**: After all components created, during ordered startup
- **Purpose**: Create and start Rust component instances with validated configs
- **Rust Interaction**: rust2go create_and_start() call
- **State Management**: Rust handle stored for later shutdown

## Core Architecture

### Go Extension Factory Implementation

````go
package rustextension

import (
    "context"
    "encoding/json"
    "fmt"
    "unsafe"
    
    "go.opentelemetry.io/collector/component"
    "go.opentelemetry.io/collector/extension"
    "go.opentelemetry.io/collector/confmap"
    "go.uber.org/zap"
)

/*
#include "rust_extension.h"
*/
import "C"

// Factory implements extension.Factory
type Factory struct {
    componentType component.Type
    stability     component.StabilityLevel
    logger        *zap.Logger
}

// NewFactory creates a new rust extension factory
func NewFactory() extension.Factory {
    return &Factory{
        componentType: component.MustNewType("rust_sample"),
        stability:     component.StabilityLevelDevelopment,
        logger:        zap.NewNop(), // Will be set by collector
    }
}

// Type returns the component type
func (f *Factory) Type() component.Type {
    return f.componentType
}

// Stability returns the stability level
func (f *Factory) Stability() component.StabilityLevel {
    return f.stability
}

// CreateDefaultConfig creates the default configuration
func (f *Factory) CreateDefaultConfig() component.Config {
    // Call Rust to get default config JSON (from Phase 2)
    jsonPtr := C.rust_extension_default_config()
    defer C.free_rust_string(jsonPtr)
    
    if jsonPtr == nil {
        // Fallback default
        return &RustComponentConfig{rawJSON: "{}"}
    }
    
    jsonStr := C.GoString(jsonPtr)
    return &RustComponentConfig{rawJSON: jsonStr}
}

// Create creates the extension wrapper - NO Rust component instance yet
func (f *Factory) Create(
    ctx context.Context, 
    set extension.Settings, 
    cfg component.Config,
) (extension.Extension, error) {
    rustCfg, ok := cfg.(*RustComponentConfig)
    if !ok {
        return nil, fmt.Errorf("invalid config type for rust extension, expected *RustComponentConfig, got %T", cfg)
    }
    
    // Config is already validated by confmap.Unmarshal during RustComponentConfig.Unmarshal()
    // This step is ONLY about creating the Go wrapper - no Rust FFI calls to create component
    
    ext := &rustExtension{
        id:        set.ID,
        logger:    set.Logger,
        config:    rustCfg,           // Already validated JSON from Phase 2
        buildInfo: set.BuildInfo,
        
        // Runtime state - will be set during Start()
        rustHandle: nil,
        started:    false,
    }
    
    set.Logger.Debug("Created Rust extension wrapper", 
        zap.String("id", set.ID.String()),
        zap.String("type", set.ID.Type().String()))
    
    return ext, nil
}
````

### Extension Instance Implementation

````go
// rustExtension implements extension.Extension (which embeds component.Component)
type rustExtension struct {
    id        component.ID
    logger    *zap.Logger
    config    *RustComponentConfig
    buildInfo component.BuildInfo
    
    // Runtime state - set during Start()
    rustHandle unsafe.Pointer  // Handle to Rust component instance
    started    bool
}

// Start implements component.Component.Start - Creates and starts Rust component
func (e *rustExtension) Start(ctx context.Context, host component.Host) error {
    if e.started {
        return fmt.Errorf("rust extension %s already started", e.id)
    }
    
    e.logger.Info("Starting Rust extension", 
        zap.String("id", e.id.String()),
        zap.String("type", e.id.Type().String()),
        zap.String("name", e.id.Name()))
    
    // NOW we call Rust to create and start the component instance
    // Config was already validated during Create step
    
    // Prepare simplified context for Rust (placeholder)
    ctxData := &FFIContext{
        ComponentID:   e.id.String(),
        ComponentType: e.id.Type().String(),
        ComponentName: e.id.Name(),
        Cancelled:     ctx.Err() != nil,
        // TODO: Add timeout, deadline, values when implementing full context support
    }
    
    // Prepare simplified host for Rust (placeholder)  
    hostData := &FFIHost{
        BuildVersion:    e.buildInfo.Version,
        BuildCommand:    e.buildInfo.Command,
        BuildTimestamp:  e.buildInfo.Timestamp,
        ExtensionCount:  0, // TODO: Add extension registry info when implementing full host sharing
    }
    
    // Convert to JSON for FFI transfer
    ctxJSON, err := json.Marshal(ctxData)
    if err != nil {
        return fmt.Errorf("failed to marshal context for rust: %w", err)
    }
    
    hostJSON, err := json.Marshal(hostData)
    if err != nil {
        return fmt.Errorf("failed to marshal host for rust: %w", err)
    }
    
    // Call Rust create_and_start via rust2go
    result, err := callRustCreateAndStart(
        string(ctxJSON), 
        string(hostJSON), 
        e.config.rawJSON,  // Pre-validated config from Phase 2
    )
    if err != nil {
        return fmt.Errorf("rust extension start call failed: %w", err)
    }
    
    if result.ErrorMsg != "" {
        return fmt.Errorf("rust extension start failed: %s", result.ErrorMsg)
    }
    
    // Store handle for shutdown
    e.rustHandle = result.Handle
    e.started = true
    
    e.logger.Info("Rust extension started successfully")
    return nil
}

// Shutdown implements component.Component.Shutdown  
func (e *rustExtension) Shutdown(ctx context.Context) error {
    if !e.started {
        e.logger.Debug("Rust extension shutdown called but not started")
        return nil
    }
    
    e.logger.Info("Shutting down Rust extension", zap.String("id", e.id.String()))
    
    // Always clean up Go state, even if Rust call fails
    defer func() {
        e.started = false
        e.rustHandle = nil
    }()
    
    // Prepare simplified context for shutdown
    ctxData := &FFIContext{
        ComponentID:   e.id.String(),
        ComponentType: e.id.Type().String(), 
        ComponentName: e.id.Name(),
        Cancelled:     ctx.Err() != nil,
    }
    
    ctxJSON, err := json.Marshal(ctxData)
    if err != nil {
        return fmt.Errorf("failed to marshal context for rust shutdown: %w", err)
    }
    
    // rust2go call for shutdown
    err = callRustShutdown(e.rustHandle, string(ctxJSON))
    if err != nil {
        // Log error but don't fail shutdown - always clean up
        e.logger.Error("Rust extension shutdown failed", zap.Error(err))
    }
    
    e.logger.Info("Rust extension shutdown successfully")
    return nil
}
````

### FFI Data Structures (Placeholders)

````go
// FFIContext - simplified context for FFI transfer
type FFIContext struct {
    ComponentID   string `json:"component_id"`
    ComponentType string `json:"component_type"`
    ComponentName string `json:"component_name"`
    Cancelled     bool   `json:"cancelled"`
    // TODO Phase 4+: Add timeout, deadline, values when implementing full context support
}

// FFIHost - simplified host for FFI transfer  
type FFIHost struct {
    BuildVersion   string `json:"build_version"`
    BuildCommand   string `json:"build_command"`
    BuildTimestamp string `json:"build_timestamp"`
    ExtensionCount int    `json:"extension_count"`
    // TODO Phase 4+: Add extension registry, resource info when implementing full host sharing
}

// RustResult - result from rust2go calls
type RustResult struct {
    Handle   unsafe.Pointer `json:"-"`
    ErrorMsg string         `json:"error_msg,omitempty"`
}
````

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
// This is a placeholder - will be implemented by Rust team
use rust2go::r2g;
use serde::{Deserialize, Serialize};

#[derive(Debug, Deserialize, Serialize)]
pub struct FFIContext {
    pub component_id: String,
    pub component_type: String,
    pub component_name: String,
    pub cancelled: bool,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct FFIHost {
    pub build_version: String,
    pub build_command: String,
    pub build_timestamp: String,
    pub extension_count: u32,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct ExtensionConfig {
    pub endpoint: Option<String>,
    pub timeout_ms: u64,
    pub enabled: bool,
}

impl ExtensionConfig {
    pub fn validate(&self) -> Result<(), String> {
        if let Some(ref endpoint) = self.endpoint {
            if endpoint.is_empty() {
                return Err("endpoint cannot be empty when specified".to_string());
            }
        }
        if self.timeout_ms == 0 {
            return Err("timeout_ms must be greater than 0".to_string());
        }
        Ok(())
    }
}

#[rust2go::r2g(queue_size = 1024)]
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

// Placeholder implementation
pub struct SampleExtension {
    started: bool,
    config: Option<ExtensionConfig>,
}

impl ExtensionLifecycle for SampleExtension {
    async fn create_and_start(&mut self, ctx: &str, host: &str, config: &str) -> Result<(), String> {
        // Parse pre-validated config - this should never fail
        let config: ExtensionConfig = serde_json::from_str(config)
            .map_err(|e| format!("unexpected config parse error: {}", e))?;
            
        let ctx_info: FFIContext = serde_json::from_str(ctx)
            .map_err(|e| format!("context parse error: {}", e))?;
            
        let host_info: FFIHost = serde_json::from_str(host)
            .map_err(|e| format!("host parse error: {}", e))?;
        
        println!("Creating and starting extension {} with validated config", ctx_info.component_id);
        println!("Host: {} {}", host_info.build_command, host_info.build_version);
        
        // Initialize extension with validated config
        self.config = Some(config);
        self.started = true;
        
        // Start any background tasks here
        
        Ok(())
    }
    
    async fn shutdown(&mut self, ctx: &str) -> Result<(), String> {
        let ctx_info: FFIContext = serde_json::from_str(ctx)?;
        
        println!("Shutting down extension {}", ctx_info.component_id);
        
        // Stop any background tasks here
        self.started = false;
        self.config = None;
        
        Ok(())
    }
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

### Step 6: Full Integration Testing

- [ ] Test complete lifecycle with real collector service
- [ ] Verify extension ordering and dependency management
- [ ] Test error scenarios and recovery patterns
- [ ] Performance testing of FFI calls and JSON marshaling

### Step 7: Documentation and Examples

- [ ] Document the Create vs Start separation clearly
- [ ] Provide configuration examples matching Phase 2 patterns
- [ ] Create integration examples with collector service
- [ ] Document placeholder interfaces for future phases

## Testing Strategy

### Unit Tests

````go
func TestExtensionFactoryLifecycleSeparation(t *testing.T) {
    factory := NewFactory()
    
    // Step 1: Create with config validation
    cfg := factory.CreateDefaultConfig()
    set := extension.Settings{
        ID:                component.NewID(factory.Type()),
        TelemetrySettings: componenttest.NewNopTelemetrySettings(),
        BuildInfo:         component.NewDefaultBuildInfo(),
    }
    
    // This should work - only Go wrapper created, no Rust instance
    ext, err := factory.Create(context.Background(), set, cfg)
    require.NoError(t, err)
    require.NotNil(t, ext)
    
    rustExt := ext.(*rustExtension)
    assert.False(t, rustExt.started)      // Not started yet
    assert.Nil(t, rustExt.rustHandle)     // No Rust instance yet
    assert.NotNil(t, rustExt.config)      // Config is set
    
    // Step 2: Start runtime activation  
    host := componenttest.NewNopHost()
    err = ext.Start(context.Background(), host)
    require.NoError(t, err)
    
    assert.True(t, rustExt.started)       // Now started
    assert.NotNil(t, rustExt.rustHandle)  // Rust instance created
    
    // Step 3: Shutdown cleanup
    err = ext.Shutdown(context.Background())
    require.NoError(t, err)
    
    assert.False(t, rustExt.started)      // Stopped
    assert.Nil(t, rustExt.rustHandle)     // Handle cleared
}

func TestConfigurationValidationBeforeCreate(t *testing.T) {
    factory := NewFactory()
    
    // Test with invalid config JSON
    invalidCfg := &RustComponentConfig{
        rawJSON: `{"timeout_ms": 0}`,  // Invalid - timeout must be > 0
    }
    
    set := extension.Settings{
        ID:                component.NewID(factory.Type()),
        TelemetrySettings: componenttest.NewNopTelemetrySettings(),
        BuildInfo:         component.NewDefaultBuildInfo(),
    }
    
    // Create should succeed even with invalid config (validation happened earlier)
    ext, err := factory.Create(context.Background(), set, invalidCfg)
    require.NoError(t, err)
    
    // But Start should fail when Rust tries to use the config
    host := componenttest.NewNopHost()
    err = ext.Start(context.Background(), host)
    require.Error(t, err)
    assert.Contains(t, err.Error(), "timeout_ms must be greater than 0")
}

func TestErrorHandlingInStart(t *testing.T) {
    factory := NewFactory()
    cfg := factory.CreateDefaultConfig()
    
    set := extension.Settings{
        ID:                component.NewID(factory.Type()),
        TelemetrySettings: componenttest.NewNopTelemetrySettings(),
        BuildInfo:         component.NewDefaultBuildInfo(),
    }
    
    ext, err := factory.Create(context.Background(), set, cfg)
    require.NoError(t, err)
    
    // Test context cancellation
    cancelCtx, cancel := context.WithCancel(context.Background())
    cancel() // Cancel immediately
    
    host := componenttest.NewNopHost()
    err = ext.Start(cancelCtx, host)
    // Should handle cancelled context gracefully
    if err != nil {
        assert.Contains(t, err.Error(), "context")
    }
}
````

### Integration Tests

````go
func TestRustExtensionWithCollector(t *testing.T) {
    // Test with real collector service
    factories := map[component.Type]extension.Factory{
        component.MustNewType("rust_sample"): NewFactory(),
    }
    
    cfg := &Config{
        Extensions: []component.ID{
            component.NewID(component.MustNewType("rust_sample")),
        },
    }
    
    extensionConfigs := map[component.ID]component.Config{
        component.NewID(component.MustNewType("rust_sample")): NewFactory().CreateDefaultConfig(),
    }
    
    extensions, err := New(context.Background(), Settings{
        Telemetry:  componenttest.NewNopTelemetrySettings(),
        BuildInfo:  component.NewDefaultBuildInfo(),
        Extensions: builders.NewExtension(extensionConfigs, factories),
    }, cfg.Extensions)
    
    require.NoError(t, err)
    
    // Test full lifecycle - this tests the Create/Start separation
    host := componenttest.NewNopHost()
    
    // All components created first
    err = extensions.Start(context.Background(), host)
    require.NoError(t, err)
    
    // Then all started in order
    err = extensions.Shutdown(context.Background())
    require.NoError(t, err)
}
````

## Build Integration

### Go Module Dependencies

````go
// go.mod
module github.com/example/otel-rust-extension

go 1.21

require (
    go.opentelemetry.io/collector/component v0.113.0
    go.opentelemetry.io/collector/extension v0.113.0
    go.opentelemetry.io/collector/confmap v1.22.0
    go.uber.org/zap v1.26.0
)

// CGO flags for linking rust2go generated library
/*
#cgo LDFLAGS: -L./rust/target/release -lrust_extension
#cgo CFLAGS: -I./rust/target/release
*/
import "C"
````

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

## Error Handling Strategy

### Go Error Patterns

````go
// Standardized error wrapping for rust FFI calls
func wrapRustError(operation string, rustError string) error {
    return fmt.Errorf("rust extension %s failed: %s", operation, rustError)
}

// Error handling in Start with context
func (e *rustExtension) Start(ctx context.Context, host component.Host) error {
    // ... setup code ...
    
    if result.ErrorMsg != "" {
        return wrapRustError("start", result.ErrorMsg)
    }
    
    return nil
}

// Graceful shutdown on errors
func (e *rustExtension) Shutdown(ctx context.Context) error {
    if !e.started {
        return nil // Already shutdown or never started
    }
    
    // Always attempt cleanup even if Rust call fails
    defer func() {
        e.started = false
        e.rustHandle = nil
    }()
    
    err := callRustShutdown(e.rustHandle, ctxJSON)
    if err != nil {
        // Log but don't fail shutdown - cleanup is more important
        e.logger.Error("Rust extension shutdown error", zap.Error(err))
    }
    
    return nil
}
````

## Phase Integration Summary

### Phase 1 → Phase 3 Connection

- **cargo Field Usage**: Phase 1's `cargo` field in Module struct identifies Rust extensions for rust2go processing
- **Builder Integration**: Factory registration leverages Phase 1's builder tool infrastructure
- **Template Generation**: Extension factories generated using Phase 1's template system patterns

### Phase 2 → Phase 3 Connection

- **Configuration Reuse**: `RustComponentConfig` from Phase 2 used unchanged for extension configuration
- **Validation Timing**: Phase 2's config validation during `confmap.Unmarshal()` happens before Phase 3's `Create()`
- **JSON Bridge**: Phase 2's JSON serialization patterns continue seamlessly into runtime FFI calls
- **Error Propagation**: Phase 2's error handling patterns extended to runtime component lifecycle

### Phase 3 → Phase 4 Preparation

- **Handle Patterns**: Extension handle management provides foundation for processor/exporter handles
- **Lifecycle Separation**: Create/Start separation scales to data processing components
- **Async Integration**: rust2go async patterns ready for high-throughput telemetry processing
- **Placeholder Evolution**: FFIContext and FFIHost placeholders ready for full implementation

## Integration Points

### Phase 2 Configuration

- Uses `RustComponentConfig` wrapper unchanged from Phase 2
- Leverages serde configuration parsing and validation
- Configuration validation happens during confmap.Unmarshal, before Create()
- JSON configuration transfer patterns established in Phase 2

### Phase 4 Preparation

- Establishes component lifecycle patterns for data processing components
- Provides foundation for telemetry data processing with handles
- Tests async patterns needed for data pipelines
- Handle management patterns ready for processor/exporter components

### Future Phase Integration

- Context placeholder ready for full context.Context implementation
- Host placeholder ready for full component.Host sharing
- Extension registry patterns established for cross-component communication
- Error handling patterns established for complex runtime scenarios

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
