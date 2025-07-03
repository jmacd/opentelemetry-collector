# Phase 1: Mixed Go/Rust Component Support in Builder

## Overview

Phase 1 establishes the foundation for supporting both Go and Rust components within the OpenTelemetry Collector builder tool. This phase focuses on extending the existing YAML configuration format and Module struct to accommodate Rust components while maintaining full backward compatibility with existing Go-only configurations.

## Design Goals

1. **Minimal Configuration Changes**: Reuse existing builder YAML structure as much as possible
2. **Consistency**: Follow the same patterns for Rust as established for Go components  
3. **Flexibility**: Support both simple and complex Cargo dependency specifications
4. **Backward Compatibility**: Existing Go-only configurations continue to work unchanged

## Core Design Decisions

### Unified Module Approach
Rather than creating separate configuration sections for Rust components, we extend the existing `Module` struct to support both languages within the same component lists (receivers, exporters, processors, extensions, connectors).

### Mutually Exclusive Language Fields
Components are either Go OR Rust, never both. The `gomod` and `cargo` fields are mutually exclusive - a module specifies one or the other, but not both.

### Literal Text Insertion Pattern
Following the existing `gomod` field pattern where content is inserted literally into go.mod files, the new `cargo` field inserts content literally into Cargo.toml dependency sections.

## Implementation

### Enhanced Module Struct

```go
// Module represents a receiver, exporter, processor or extension for the distribution
type Module struct {
    Name   string `mapstructure:"name"`   // if not specified, this is package part of the go mod (last part of the path)
    Import string `mapstructure:"import"` // if not specified, this is the path part of the go mods
    GoMod  string `mapstructure:"gomod"`  // a gomod-compatible spec for the module
    Path   string `mapstructure:"path"`   // an optional path to the local version of this module
    Cargo  string `mapstructure:"cargo"`  // a cargo-compatible dependency line for Cargo.toml
}
```

### YAML Configuration Examples

**Simple Rust dependency:**
```yaml
receivers:
  - cargo: jaeger-receiver = "0.129.0"
```

**Complex Rust dependency with features:**
```yaml
exporters:
  - cargo: 'otel-otlp-exporter = { version = "0.129.0", features = ["trace", "metrics"] }'
```

**Go-only component (unchanged):**
```yaml
extensions:
  - gomod: go.opentelemetry.io/collector/extension/zpagesextension v0.129.0
```

**Mixed components list:**
```yaml
processors:
  - gomod: go.opentelemetry.io/collector/processor/batchprocessor v0.129.0
  - cargo: async-processor = "0.129.0"
```

### Validation Updates

Current validation requires `gomod` field to be non-empty. Phase 1 updates validation to:
- Accept modules with either `gomod` OR `cargo` fields (mutually exclusive)
- Maintain existing error messages for clarity
- Ensure components specify exactly one language

```go
func validateModules(name string, mods []Module) error {
    for i, mod := range mods {
        if mod.GoMod == "" && mod.Cargo == "" {
            return fmt.Errorf("%s module at index %v: missing gomod or cargo specification", name, i)
        }
        if mod.GoMod != "" && mod.Cargo != "" {
            return fmt.Errorf("%s module at index %v: cannot specify both gomod and cargo", name, i)
        }
    }
    return nil
}
```

## YAML String Handling

For complex Cargo dependency specifications, YAML provides multiple quoting options:

1. **Simple dependencies** (no special characters):
   ```yaml
   cargo: serde = "1.0.1"
   ```

2. **Complex dependencies** (using YAML literal strings):
   ```yaml
   cargo: |
     serde = { version = "1.0", features = ["derive"] }
   ```

3. **Complex dependencies** (using quoted strings):
   ```yaml
   cargo: 'serde = { version = "1.0", features = ["derive"] }'
   ```

## Component Factory Architecture

### Factory Registration Strategy

**Go Factory Registration**: In Go, component factories are registered through inclusion in the main package's `components.go` file, generated from builder templates. Each factory implements the relevant interface (`component.Factory` base with specialized `extension.Factory`, `processor.Factory`, etc.) with two essential methods:

- **`Type()`**: Returns the component type name (e.g., "otlp", "debug", "prometheus")
- **`CreateDefaultConfig()`**: Returns the default configuration struct, implicitly defining the config type and its parsing metadata (mapstructure tags)

**Rust Factory Registration**: Rust components use the `linkme` crate for static registration at link time. Factories are collected into distributed static slices, allowing discovery at runtime without explicit registration calls.

### Factory Integration Across Phases

This factory architecture connects all four phases:

1. **Phase 1 (Builder)**: Module cargo fields identify Rust components for compilation and factory generation
2. **Phase 2 (Configuration)**: Factory `CreateDefaultConfig()` returns config structs defined using serde patterns
3. **Phase 3 (Extensions)**: Extension factories implement `extension.Factory` with basic lifecycle for non-pipeline components
4. **Phase 4 (Pipeline)**: Processor/exporter/receiver factories implement specialized interfaces for data processing

### Factory Template Generation

The builder tool generates factory registration code for both Go and Rust components:

**Go Factory Registration (in generated components.go)**:

```go
func Components() (
    extensions map[component.Type]extension.Factory,
    // ... other component types
) {
    extensions = map[component.Type]extension.Factory{
        component.MustNewType("zpages"): zpagesextension.NewFactory(),
        component.MustNewType("rust_sample"): rustextension.NewFactory(), // Generated from cargo field
    }
    // ...
}
```

**Rust Factory Registration (linkme collection)**:

```rust
use linkme::distributed_slice;

#[distributed_slice]
pub static RUST_EXTENSIONS: [&'static dyn ExtensionFactory] = [..];

// Components register themselves at link time
#[distributed_slice(RUST_EXTENSIONS)]
static SAMPLE_EXTENSION: &'static dyn ExtensionFactory = &SampleExtensionFactory;

pub trait ExtensionFactory {
    fn component_type(&self) -> &'static str;
    fn create_default_config(&self) -> String; // JSON serialized default config
    fn create_instance(&self, config: &str) -> Result<Box<dyn Extension>, String>;
}
```

## Phase 1 Scope

### What's Included

- Enhanced Module struct with Cargo field for Rust factory identification
- Updated validation logic for mutually exclusive language specifications
- YAML configuration format extensions for factory specification
- Backward compatibility preservation for existing Go factory patterns

### What's Deferred to Later Phases

- **Phase 2: Configuration Structs** - serde-based config structs returned by factory `CreateDefaultConfig()`
- **Phase 3: Extension Factories** - Complete `extension.Factory` implementation with rust2go lifecycle
- **Phase 4: Pipeline Component Factories** - processor.Factory, exporter.Factory, receiver.Factory implementation
- Distribution struct enhancements for Rust toolchain
- Factory discovery and registration infrastructure
- Cargo patches (equivalent to Go replaces/excludes)

## Configuration Strategy

Phase 1 deliberately avoids addressing how Rust components will be configured at runtime. For initial implementation, Rust components will use empty configuration objects, allowing the builder and factory infrastructure to be established. **Phase 2 will focus specifically on the configuration design challenges**, including serde-based configuration structs and confmap integration.

## Compatibility Impact

### Backward Compatibility

- All existing Go-only configurations work unchanged
- No breaking changes to existing APIs or structures
- Existing validation continues to work for Go modules

### Forward Compatibility

- Module struct extensible for future language support
- Template system can be extended for additional build files
- Validation framework supports multiple language requirements

## Next Phase Considerations

Phase 1 establishes the configuration foundation. Subsequent phases will need to address:

1. **Phase 2: Runtime Configuration Design**: serde-based configuration structs, confmap integration, FFI validation
2. **Phase 3: Build Process Integration**: Cargo.toml generation and Rust compilation
3. **Phase 4: Component Registration**: Factory patterns for Rust components
4. **Phase 5: rust2go Integration**: Go↔️Rust interoperability and runtime data processing
5. **Toolchain Management**: Rust version specification in Distribution struct

## Files Modified

- `cmd/builder/internal/builder/config.go`: Enhanced Module struct and validation
- (Future phases will modify templates and build process files)

## Testing Strategy

Phase 1 testing focuses on configuration parsing and validation:

- YAML parsing with new cargo field
- Validation ensures mutually exclusive gomod/cargo fields
- Validation accepts modules with either gomod or cargo
- Backward compatibility with existing configurations
- Error handling for malformed specifications

This design provides a solid foundation for mixed-language component support while maintaining the builder tool's existing simplicity and consistency. The mutually exclusive approach keeps the initial implementation simple while the unified Module struct maintains consistency with existing patterns.
