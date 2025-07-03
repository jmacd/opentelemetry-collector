# Phase 7: Plugin Architecture for Go and Rust Components

## Overview

Phase 7 introduces dynamic plugin support for both Go and Rust components in the OpenTelemetry Collector. This enables runtime extensibility, allowing new components to be loaded without recompiling the main binary. The design must ensure safety, compatibility, and hermeticity, given the challenges of dynamic linking in both languages.

## Design Goals

1. **Dynamic Extensibility**: Load Go and Rust components as plugins at runtime.
2. **Hermetic Builds**: Guarantee that plugins and the main binary are built with exactly matching dependencies, compiler versions, and build flags.
3. **Version Safety**: Prevent runtime crashes or undefined behavior due to mismatched versions or ABIs.
4. **Cross-Language Support**: Support plugins written in both Go and Rust, with idiomatic loading and registration patterns.
5. **Distribution Verification**: Provide manifest and artifact verification to ensure plugin compatibility before loading.

## Go Plugin System

- Use Go's native `plugin` package (`-buildmode=plugin`) to build shared libraries.
- Plugins must be built with the exact same Go version, module versions, and build flags as the main Collector binary.
- The builder will:
  - Generate a manifest (e.g., JSON or YAML) describing the Go version, module hashes, and build flags.
  - Embed this manifest in both the main binary and each plugin.
  - At runtime, the Collector will verify the manifest before loading a plugin.
- Plugins will export a known symbol (e.g., `CollectorPluginInit`) for registration.

**Caveats:**
- Go plugins are only supported on Linux and macOS, not Windows.
- All dependencies must be statically linked (in each plugin) and version-matched.

## Rust Plugin System

- Use Cargo to build shared libraries (`cdylib`), loaded via `dlopen()` and `dlsym()`.
- Rust plugins must be built with the exact same Rust version, crate versions, and Cargo settings as the main binary.
- The builder will:
  - Generate a manifest (e.g., TOML or JSON) with Rust version, crate hashes, and build flags.
  - Embed this manifest in both the main binary and each plugin (e.g., as a section or exported symbol).
  - At runtime, the Collector will verify the manifest before loading a plugin.
- Plugins will export a C-compatible registration function (e.g., `register_collector_plugin`).

**Caveats:**
- Rust has no stable ABI; all FFI boundaries must use C-compatible types.
- All dependencies must be version-matched and statically linked.

## Hermetic Build & Distribution

- The builder will support a "hermetic" build mode:
  - Use Docker to encapsulate the build environment (compiler versions, OS, etc.).
  - The Dockerfile will be generated or versioned alongside the distribution.
  - All plugins and the main binary are built in the same container, ensuring identical environments.
  - The build process will output:
    - The main Collector binary
    - Plugin shared libraries
    - Version manifests for each artifact (see manifest schema below)
    - The Dockerfile or a hash of the build environment
- At runtime, the Collector will:
  - Refuse to load plugins with mismatched manifests.
  - Provide CLI commands to verify plugin compatibility and inspect plugin metadata before loading.

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

- For Go, fill `toolchain_version` with `go version`, and `dependency_hashes` with module hashes.
- For Rust, fill `toolchain_version` with `rustc --version`, and `dependency_hashes` with crate hashes.
- The manifest is exported as a symbol (e.g., `collector_plugin_manifest`) for Rust, and embedded in Go binaries/plugins for extraction.

## CLI Plugin Probing and Introspection

The Collector CLI will provide commands to help users and CI/CD systems inspect and validate plugins:

- `otelcol plugins list` — List all plugins in the configured directory, showing manifest info (language, version, hashes, etc.).
- `otelcol plugins validate <plugin.so>` — Load the plugin, extract and print its manifest, and check for compatibility with the main binary.
- `otelcol plugins inspect <plugin.so>` — Print detailed manifest and exported symbols, without loading the plugin into the main process.
- `otelcol plugins check-all` — Validate all plugins in the directory, reporting any mismatches or issues.

These commands allow safe, transparent plugin management and can be used in automation.

## Component Introspection and Documentation

The Collector CLI will also support commands for built-in and plugin components:

- `otelcol components` — List all built-in and plugin components, their types, modules, and stability levels.
- `otelcol components explain <type>` — Show configuration schema and documentation for a component.
- `otelcol components default-config <type>` — Print the default config for a component.
- `otelcol components list --plugins` — List only plugin components.

This enables users to discover, understand, and validate all available components, regardless of origin.


## Plugin Discovery & Registration

- Plugins are discovered via a configured directory or manifest file.
- On load, the Collector:
  - Verifies the manifest.
  - Loads the shared library.
  - Calls the exported registration function to register new factories/components.


## Testing & Verification

- Automated tests will:
  - Build the Collector and plugins in a hermetic Docker environment.
  - Attempt to load plugins with mismatched manifests (should fail).
  - Load and use plugins with matching manifests (should succeed).
- Manual and CLI verification tools will be provided to inspect plugin manifests, build environments, and component documentation.

## Next Steps

- Define manifest schema for both Go and Rust plugins.
- Implement manifest embedding and verification logic.
- Extend the builder to support Docker-based hermetic builds.
- Implement plugin loading and registration logic in the Collector.
- Document plugin development and distribution workflow.
