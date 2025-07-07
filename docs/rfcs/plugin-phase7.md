# Phase 7: Plugin Architecture for Go and Rust Components

## Overview

Phase 7 introduces dynamic plugin support for Go and Rust components in the OpenTelemetry Collector. This enables runtime extensibility—loading new components without recompiling the main binary. The design ensures safety, compatibility, and hermeticity despite dynamic linking challenges.

## Design Goals

1. **Dynamic Extensibility**: Load Go and Rust components as plugins at runtime
2. **Hermetic Builds**: Guarantee plugins and main binary use identical dependencies, compiler versions, and build flags
3. **Version Safety**: Prevent runtime crashes from mismatched versions or ABIs
4. **Cross-Language Support**: Support plugins in both Go and Rust with idiomatic loading patterns
5. **Distribution Verification**: Verify plugin compatibility before loading via manifest and artifact checks

## Go Plugin System

- Uses Go's native plugin package with plugin build mode for shared libraries
- Plugins must use identical Go version, module versions, and build flags as the main binary
- The builder will:
  - Generate manifests describing Go version, module hashes, and build flags (JSON/YAML)
  - Embed manifests in both main binary and plugins
  - Verify manifests at runtime before loading plugins
- Plugins export a known registration symbol following standard naming conventions

**Caveats:**
- Go plugins only supported on Linux and macOS, not Windows
- All dependencies must be statically linked and version-matched

## Rust Plugin System

- Uses Cargo to build shared libraries as cdylib crates, loaded via dynamic linking
- Plugins must use identical Rust version, crate versions, and Cargo settings as the main binary
- The builder will:
  - Generate manifests with Rust version, crate hashes, and build flags (TOML/JSON)
  - Embed manifests in both main binary and plugins as sections or exported symbols
  - Verify manifests at runtime before loading plugins
- Plugins export C-compatible registration functions following standard FFI conventions

**Caveats:**

- Rust has no stable ABI; all FFI boundaries must use C-compatible types
- All dependencies must be version-matched and statically linked

## Hermetic Build & Distribution

The builder supports "hermetic" build mode:

- Uses Docker to encapsulate build environment (compiler versions, OS, etc.)
- Dockerfile is generated or versioned alongside the distribution
- All plugins and main binary built in the same container for identical environments
- Build process outputs:
  - Main Collector binary
  - Plugin shared libraries
  - Version manifests for each artifact
  - Dockerfile or build environment hash

At runtime, the Collector will:

- Refuse to load plugins with mismatched manifests
- Provide CLI commands to verify plugin compatibility and inspect metadata

## Unified Plugin Manifest Schema

Both Go and Rust plugins will embed a manifest with the following structure (JSON):

```json
{
  "language": "go", // or "rust"
  "toolchain_version": "1.22.3", // go version or rustc version
  "build_profile": "release",
  "dependency_hashes": {
    "otel-arrow": "b1c2d3...",
    "go.opentelemetry.io/collector": "a9b8c7..."
  },
  "build_flags": ["-buildmode=plugin"],
  "target": "x86_64-unknown-linux-gnu",
  "build_time": "2025-07-03T12:34:56Z"
}
```

- For Go: toolchain_version contains Go version output; dependency_hashes contains module hashes
- For Rust: toolchain_version contains Rust compiler version output; dependency_hashes contains crate hashes
- Manifest exported as named symbol for Rust plugins, embedded in Go binaries/plugins for extraction

## CLI Plugin Probing and Introspection

The Collector CLI provides commands for plugin inspection and validation:

- **List plugins**: Display all plugins in configured directory with manifest info (language, version, hashes)
- **Validate plugin**: Load specific plugin, extract manifest, check compatibility with main binary
- **Inspect plugin**: Print detailed manifest and exported symbols without loading into main process
- **Check all plugins**: Validate all plugins in directory, report mismatches or issues

These commands enable safe, transparent plugin management for automation.

## Component Introspection and Documentation

The Collector CLI supports commands for built-in and plugin components:

- **List components**: Display all components with types, modules, and stability levels
- **Explain component**: Show configuration schema and documentation for specific component type
- **Default configuration**: Print default configuration for component type
- **List plugin components**: Display only plugin components, filtering out built-in ones

This enables component discovery, understanding, and validation regardless of origin.


## Plugin Discovery & Registration

Plugins are discovered via configured directory or manifest file. On load, the Collector:

- Verifies the manifest
- Loads the shared library
- Calls exported registration function to register new factories/components


## Testing & Verification

Automated tests will:

- Build Collector and plugins in hermetic Docker environment
- Attempt loading plugins with mismatched manifests (should fail)
- Load and use plugins with matching manifests (should succeed)

Manual and CLI verification tools inspect plugin manifests, build environments, and component documentation.

## Next Steps

- Define manifest schema for Go and Rust plugins
- Implement manifest embedding and verification logic
- Extend builder to support Docker-based hermetic builds
- Implement plugin loading and registration logic in Collector
- Document plugin development and distribution workflow
