# OpenTelemetry Collector Rate Limiting Design Discussion

## Context

This document records a design discussion about implementing rate limiting in the OpenTelemetry Collector, following architectural patterns from Envoy's proven rate limiting system. The discussion covers the translation of Envoy concepts to the Collector context and validates the design choices made.

## Design Overview

### Architectural Translation: Envoy → OTel Collector

The mapping from Envoy to Collector concepts follows the Requestor/Implementor pattern:

#### **Implementor Side Translation**
```
Envoy                    →    OTel Collector
─────────────────────────     ─────────────────
Filters                  →    Extensions (localrate/*, globalrate/*, localresource/*)
Descriptors (patterns)   →    limiters[] (with conditions)
Descriptor entries       →    conditions[] (pattern matching)
Token buckets           →    token_bucket + admission algorithms
```

#### **Requestor Side Translation**
```
Envoy                    →    OTel Collector
─────────────────────────     ─────────────────
Route rate_limits        →    Receiver limiters
Actions (extractors)     →    Extractors (opentelemetry_signal, etc.)
Descriptor instances     →    Runtime request descriptors
```

## Three Degrees of Freedom Analysis

The design provides **three key degrees of freedom** that create combinatorial power:

### **Requestor Side: 1 Degree of Freedom**
```yaml
receivers:
  otlp:
    protocols:
      http:
        limiters:
          - request:                    # Request 1: User limiting
              - request_headers: {...}
          - request:                    # Request 2: Tenant limiting  
              - generic_key: {...}
          - request:                    # Request 3: Operation limiting
              - opentelemetry_signal: {...}
```

**Multiple requests** = Multiple independent rate limiting dimensions

### **Implementor Side: 2 Degrees of Freedom**

#### **Degree 1: Multiple Extensions (Limiters)**
```yaml
extensions:
  localrate/http:      # Limiter 1: Fast local protection
    limiters: [...]
  globalrate/http:     # Limiter 2: Coordinated global limits  
    limiters: [...]
  localresource/memory: # Limiter 3: Resource admission control
    limiters: [...]
```

#### **Degree 2: Multiple Patterns per Extension**
```yaml
localrate/http:
  limiters:
    - token_bucket: {...}        # Pattern 1: Premium users
      conditions:
        - key: tenant_id
          value: premium
    - token_bucket: {...}        # Pattern 2: Standard users
      conditions:
        - key: tenant_id  
          value: standard
```

## The "Conditional-AND" Combinator

The evaluation logic follows **"all limiters, but only matching conditions"**:

```
For each Request from Requestor:
  For each Limiter Extension:
    For each Pattern in Limiter:
      If Pattern.conditions.match(Request):
        Apply Pattern.limit(Request)
        
Result = AND(all matching pattern results)
```

This provides:
- **Comprehensive protection** (all limiters must approve)
- **Selective application** (only relevant patterns activate)
- **Flexible targeting** (same request can match different patterns in different limiters)

## Key Design Improvements Over Envoy

### 1. **Multi-Algorithm Support**
Extends beyond Envoy's token-bucket-only approach:

```yaml
# Rate limiting with token buckets
- token_bucket:
    rated: 50000
    burst: 100000

# Resource limiting with admission control  
- admission:
    allowed: 10000000
    waiting: 2000000
```

Handles both **rate limiting** (requests/time) and **resource limiting** (memory/concurrency) with different algorithms.

### 2. **Unit System Innovation**
Explicit unit declaration for type safety:

```yaml
unit: network_bytes/second  # For rate limiting
unit: request_bytes         # For resource limiting
```

Ensures callers use limiters correctly and enables different limit types.

### 3. **Improved Cardinality Management**
More sophisticated cardinality control:

```yaml
cardinality:
  max_count: 20
  behavior: replace  # or refuse
```

Better control than Envoy's simple LRU behavior.

## Expressiveness Through Composition

The three degrees of freedom create a **rich configuration space**:

```yaml
# Example: Multi-dimensional rate limiting
receivers:
  otlp:
    limiters:
      - request:                     # Dimension 1: User quotas
          - request_headers:
              header_name: "x-user-id"
              descriptor_key: "user_id"
        network_bytes:
          - id: localrate/user       # Local user limits
          - id: globalrate/user      # Global user coordination
          
      - request:                     # Dimension 2: Tenant quotas
          - request_headers:
              header_name: "x-tenant-id"  
              descriptor_key: "tenant_id"
        network_bytes:
          - id: localrate/tenant     # Local tenant limits
          - id: globalresource/tenant # Global tenant resources
          
      - request:                     # Dimension 3: Signal type quotas
          - opentelemetry_signal:
              descriptor_key: "signal"
        request_count:
          - id: localrate/signal     # Signal-specific limits
```

**Result**: 3 requests × 2-3 limiters each = 6-9 independent rate limiting checks, all applied with AND logic.

## Clean Separation of Concerns

| Concern | Handled By | Configuration Location |
|---------|------------|----------------------|
| **What to extract** | Request extractors | Receiver `limiters.request` |
| **How to classify** | Condition matching | Extension `limiters.conditions` | 
| **What limits to apply** | Algorithm choice | Extension `limiters.token_bucket/admission` |
| **Where to coordinate** | Extension type | Extension `localrate` vs `globalrate` |

## Incremental Complexity Support

The design supports gradual complexity addition:

### **Level 1: Basic Rate Limiting**
```yaml
# Simple: One request, one limiter, one pattern
extensions:
  localrate/basic:
    limiters:
      - token_bucket: {rated: 1000, burst: 2000}
        unit: requests/second
        conditions: []  # Wildcard - applies to all

receivers:
  otlp:
    limiters:
      - request: [{generic_key: {descriptor_value: "basic"}}]
        request_count: [{id: localrate/basic}]
```

### **Level 2: Multi-Tier Users**
```yaml
# Add user tiers with same limiter
extensions:
  localrate/user:
    limiters:
      - token_bucket: {rated: 5000, burst: 10000}
        conditions: [{key: user_tier, value: premium}]
      - token_bucket: {rated: 1000, burst: 2000}  
        conditions: [{key: user_tier, value: standard}]

receivers:
  otlp:
    limiters:
      - request: [{request_headers: {header_name: "x-user-tier", descriptor_key: "user_tier"}}]
        request_count: [{id: localrate/user}]
```

### **Level 3: Multi-Dimensional + Global Coordination**
```yaml
# Add tenant limits + global coordination
extensions:
  localrate/user: {as above}
  localrate/tenant:
    limiters:
      - token_bucket: {rated: 50000, burst: 100000}
        conditions: [{key: tenant_id}]  # Wildcard per tenant
  globalrate/coordination:
    domain: cluster_limits
    service: {...}

receivers:
  otlp:
    limiters:
      - request: [{request_headers: {header_name: "x-user-tier", descriptor_key: "user_tier"}}]
        request_count: [{id: localrate/user}]
      - request: [{request_headers: {header_name: "x-tenant-id", descriptor_key: "tenant_id"}}]  
        network_bytes: 
          - id: localrate/tenant
          - id: globalrate/coordination
```

## Advantages of Following Envoy's Proven Model

### **1. Proven Performance Characteristics**
- Hash-based lookups for condition matching
- Optimized token bucket implementations
- Well-understood memory management patterns

### **2. Familiar Mental Model**
- Developers who know Envoy can immediately understand the configuration
- Debugging strategies transfer directly
- Performance tuning knowledge applies

### **3. Rich Documentation and Tooling**
- Extensive real-world examples available
- Known best practices and anti-patterns
- Existing monitoring and observability patterns

## The "V3" Evolution Path

The design represents evolution:
- **Envoy v1 learnings**: Basic rate limiting patterns
- **Envoy v2 learnings**: Advanced descriptor matching, staged processing
- **Collector v3 innovation**: Multi-algorithm support, type safety, collector-specific adaptations

While keeping the **proven core architecture**.

## Implementation Efficiency

The three degrees of freedom map to efficient implementations:

```go
// Pseudo-code for efficient evaluation
func EvaluateRequest(request Request, limiters []Limiter) bool {
    for _, limiter := range limiters {  // Degree 2: Multiple limiters
        matched := false
        for _, pattern := range limiter.Patterns {  // Degree 3: Multiple patterns
            if pattern.Conditions.Match(request) {
                matched = true
                if !pattern.Algorithm.Allow(request) {
                    return false  // AND logic: any failure = reject
                }
            }
        }
        // If no patterns matched, limiter doesn't apply (conditional-AND)
    }
    return true  // All applicable limiters passed
}
```

**Performance**: O(L×P×C) where L=limiters, P=patterns, C=conditions - same as Envoy's proven complexity.

## Design Discussion Points

### 1. **Cross-Product Complexity Management**

The design inherits Envoy's cross-product challenge but maintains efficiency. Potential future enhancements could include:
- **Limiter chaining**: First-match-wins instead of all-must-pass
- **Priority levels**: High-priority limiters can short-circuit lower ones
- **Limiter groups**: Bundle related limiters for atomic evaluation

### 2. **Condition Matching Philosophy**

Currently follows Envoy's AND-based condition matching. Future consideration for OR-based matching:
```yaml
# Potential future enhancement: Match premium users OR internal traffic
conditions:
  - any_of:
    - key: tenant_id
      value: premium
    - key: data_source  
      value: internal
```

### 3. **Multi-Signal Complexity**

The Collector's multi-signal nature (traces, metrics, logs) adds complexity Envoy doesn't have. The unified approach provides:

**Pros of unified**:
- Consistent configuration model
- Cross-signal limiting (total throughput)
- Simpler deployment

**Considerations**:
- More complex per-signal configuration
- Potential performance overhead for unused signals

### 4. **Error Handling Strategy**

Future considerations for explicit error handling configuration:
```yaml
# Potential enhancement: Explicit failure modes
localrate/http:
  failure_mode: allow  # or deny or degrade
  timeout: 100ms
```

## Conclusion

The design achieves **maximum expressiveness with minimum architectural risk**. The three degrees of freedom with conditional-AND combinators provide a **proven, powerful, and intuitive** rate limiting system that can handle virtually any real-world requirement while maintaining Envoy's performance characteristics.

The approach demonstrates excellent architectural judgment: **innovate where you add unique value** (multi-algorithm support, type safety, collector-specific features), **adopt proven patterns everywhere else** (core evaluation logic, condition matching, data flow).

This creates a solid foundation that:
- Builds on Envoy's battle-tested architecture
- Adds meaningful innovations for the Collector context
- Maintains familiar mental models for operators
- Provides clear scaling and complexity management patterns
- Delivers predictable performance characteristics

The design successfully translates Envoy's proven rate limiting model to the OpenTelemetry Collector while enhancing it with collector-specific capabilities and improved type safety.
