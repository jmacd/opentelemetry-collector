# Rate Limiting Architecture Comparison: A Path to OpenTelemetry Standardization

## Executive Summary

This report analyzes four distinct rate limiting and filtering architectures across different systems to evaluate the potential for standardizing on a unified OpenTelemetry limiter architecture. We examine:

1. **Envoy v3 Rate Limiting** - Production-proven descriptor-based system with requestor/implementor pattern
2. **Hypothetical OTel Collector Limiter Extensions** - Proposed unified architecture following Envoy patterns
3. **OTel Tail Sampling Processor** - Current tree-based policy evaluation system
4. **OTEP-250 Composite Samplers** - Experimental rule-based sampler composition for SDKs

## Key Findings

### **Architectural Convergence Opportunity**
All four systems share fundamental concepts but implement them differently:
- **Pattern Matching**: All systems match request/span characteristics against configured patterns
- **Policy Composition**: All support combining multiple policies/limiters/samplers
- **Hierarchical Evaluation**: All evaluate multiple criteria in sequence
- **Rate/Resource Control**: All provide mechanisms to limit throughput or resource usage

### **Recommended Path Forward**
**Migrate toward the Envoy-inspired unified architecture** (Option 2) while preserving the best innovations from each system. This provides:
- **Proven performance** at scale (Envoy heritage)
- **Rich expressiveness** through three degrees of freedom
- **Incremental migration path** from existing systems
- **Multi-algorithm support** beyond just rate limiting

## 1. Architectural Analysis by System

### 1.1 Envoy v3 Rate Limiting Architecture

**Pattern**: Requestor/Implementor with asymmetric nesting complexity

#### **Core Architecture**
```
Routes (Requestor)         →    Filters (Implementor)
├── Rate Limit Requests    →    ├── Rate Limit Patterns
│   └── Request Extractors →    │   └── Key Conditions
└── [Multiple Requests]    →    └── [Multiple Patterns]
```

#### **Key Characteristics**
- **Proven at Scale**: Battle-tested in production environments
- **Asymmetric Complexity**: 2 levels (requestor) vs 3 levels (implementor)
- **Cross-Product Evaluation**: F×P×R×C complexity (manageable in practice)
- **Pure AND Logic**: Conjunctive evaluation throughout
- **No Transaction Semantics**: Optimized for performance over consistency
- **Hash-Based Matching**: O(1) pattern matching for performance

#### **Strengths**
- **Sophisticated Resource Management**: Multi-dimensional control (memory, time, proportional rate)
- **Compact Configuration**: Simple syntax hiding complex resource management
- **Automatic Optimization**: Built-in batching, memory management, concurrent access
- **Trace Context**: Complete trace information for intelligent decisions
- **Proportional Limiting**: Unique span-aware rate limiting not seen in other systems
- **Production Proven**: Widely deployed with sophisticated edge case handling

#### **Limitations**
- **Limited Expressiveness**: Fixed set of policy types, no complex composition
- **Traces Only**: Doesn't handle metrics or logs
- **No Global Coordination**: Local decisions only, no cluster-wide limiting
- **No AND Logic**: Only OR composition, can't require multiple conditions

### 1.2 Hypothetical OTel Collector Limiter Extensions

**Pattern**: Enhanced Requestor/Implementor with multi-algorithm support

#### **Core Architecture**
```
Receivers (Requestor)      →    Extensions (Implementor)
├── Limiter Requests       →    ├── Rate Limit Patterns
│   └── Request Extractors →    │   └── Conditions
└── [Multiple Requests]    →    └── [Multiple Patterns with Algorithms]
```

#### **Key Innovations Over Envoy**
- **Multi-Algorithm Support**: Token buckets + admission control + custom algorithms
- **Type Safety**: Explicit unit declarations (requests/second, bytes, etc.)
- **Improved Cardinality**: Better control than Envoy's simple LRU
- **Resource Limiting**: Beyond rate limiting to memory/concurrency control

#### **Three Degrees of Freedom**
1. **Multiple Requests** (Requestor): Independent rate limiting dimensions
2. **Multiple Extensions** (Implementor): Different limiter types (local/global/resource)
3. **Multiple Patterns per Extension**: Condition-based differentiation

#### **Example Configuration**
```yaml
# Requestor Side
receivers:
  otlp:
    limiters:
      - request: [{user_id extraction}]     # Dimension 1
        network_bytes: [{id: localrate/user}, {id: globalrate/user}]
      - request: [{tenant_id extraction}]   # Dimension 2
        request_count: [{id: localrate/tenant}]

# Implementor Side
extensions:
  localrate/user:                          # Extension 1
    limiters:
      - token_bucket: {rated: 5000}        # Pattern 1
        conditions: [{key: user_tier, value: premium}]
      - token_bucket: {rated: 1000}        # Pattern 2
        conditions: [{key: user_tier, value: standard}]
```

#### **Strengths**
- **Familiar Model**: Builds on proven Envoy patterns
- **Enhanced Capabilities**: Multi-algorithm support and type safety
- **Incremental Complexity**: Supports gradual adoption
- **Clear Separation**: Clean concerns between extraction and policy

#### **Potential Challenges**
- **Implementation Complexity**: More complex than existing systems
- **Migration Path**: Requires significant changes to current collector

### 1.3 OTel Tail Sampling Processor

**Pattern**: Sophisticated precedence-based policy composition with hierarchical evaluation

#### **Core Architecture**
```
Tail Sampling Processor (Evaluate ALL policies)
├── Policy 1: always_sample → Decision 1
├── Policy 2: numeric_attribute (key: status_code, range: 400-500) → Decision 2  
├── Policy 3: string_attribute (key: service.name, values: [critical_service]) → Decision 3
└── Policy 4: rate_limiting (spans_per_second: 1000) → Decision 4
                    ↓
Final Decision = OR(Decision 1, Decision 2, Decision 3, Decision 4)
Context from = First policy that returned Sampled
```

#### **Key Characteristics**
- **Hierarchical Precedence**: All policies evaluate, first "Sampled" policy provides context
- **Compact Expressions**: Combines degrees of freedom into elegant precedence system
- **Proportional Rate Limiting**: Accounts for variable span counts per trace
- **Trace-Level Decisions**: Operates on complete traces with rich context
- **Sophisticated OR Logic**: "MustSample" can override "MustNotSample"
- **Two Evaluation Modes**: All-policies vs first-match (configurable behavior)

#### **Evaluation Logic (Actual Implementation)**
```go
// Real implementation from processor.go
func makeDecision(trace *TraceData) (Decision, *Policy) {
    finalDecision := NotSampled
    var matchingPolicy *Policy = nil
    
    // Evaluate ALL policies (sophisticated precedence)
    for i, policy := range policies {
        decision := policy.Evaluator.Evaluate(trace)
        trace.Decisions[i] = decision  // Store all decisions
        
        if decision == Sampled {
            finalDecision = Sampled  // Any policy can override to Sample
            if matchingPolicy == nil {
                matchingPolicy = policy  // First match provides context
            }
        }
    }
    return finalDecision, matchingPolicy
}
```

#### **Unique Features Not Found in Other Systems**

##### **1. Proportional Rate Limiting**
```go
// Rate limiting considers variable span counts per trace
spansInSecondIfSampled := currentSpanCount + trace.SpanCount
if spansInSecondIfSampled < spansPerSecond {
    return Sampled
}
```

##### **2. Precedence-Based Context Assignment**
- All policies vote on sampling decision (OR logic)
- First policy that votes "Sample" provides the processing context
- Enables sophisticated override patterns: "Always sample errors, but rate limit everything else"

##### **3. Complete Trace Context**
- Policies see entire trace including span count, attributes across all spans
- Late-arriving spans can be handled post-decision
- Rich trace-level metadata available for decisions

#### **Compact Expression Power**
```yaml
# This compact config achieves complex logic:
policies:
  - {name: errors, type: numeric_attribute, 
     numeric_attribute: {key: http.status_code, min_value: 400}}
  - {name: critical, type: string_attribute,
     string_attribute: {key: service.name, values: [payment, auth]}}
  - {name: rate_limit, type: rate_limiting,
     rate_limiting: {spans_per_second: 1000}}

# Equivalent logic: 
# "Sample ALL errors OR critical services, but rate limit everything to 1000 spans/sec"
# The first matching policy provides context, but rate limiting applies globally
```

#### **Strengths**
- **Elegant Precedence Model**: Sophisticated override patterns with simple config
- **Compact Expressiveness**: Rich logic in minimal configuration
- **Proportional Rate Limiting**: Accounts for variable trace sizes
- **Production Proven**: Battle-tested in large collector deployments
- **Context Preservation**: First matching policy provides processing context
- **Two Evaluation Modes**: Flexible policy composition strategies

#### **Limitations**
- **Trace-Only**: Doesn't handle metrics or logs
- **Limited Policy Types**: Fixed set of built-in evaluators
- **Memory Intensive**: Must buffer traces during decision wait
- **No External Coordination**: All decisions are local to collector instance

### 1.4 OTEP-250 Composite Samplers

**Pattern**: Hierarchical sampler composition with consistent probability

#### **Core Architecture**
```
ConsistentRateLimiting(1000,
  ConsistentAnyOf(
    ConsistentParentBased(
      ConsistentRuleBased(ROOT, {
        (http.target == /healthcheck) => ConsistentAlwaysOff,
        (http.target == /checkout) => ConsistentAlwaysOn,
        true => ConsistentFixedThreshold(0.25)
      })
    ),
    ConsistentRuleBased(CLIENT, {
      (http.url == /foo) => ConsistentAlwaysOn
    })
  )
)
```

#### **Key Characteristics**
- **Hierarchical Composition**: Nested sampler trees
- **Consistent Probability**: Maintains statistical properties across composition
- **Rich Predicate System**: Flexible condition matching
- **Both AND and OR Logic**: Through different composite samplers
- **Head-Based Sampling**: SDK-level decisions before span creation

#### **Composite Sampler Types**
- **ConsistentRuleBased**: Pattern matching with predicates (like switch/case)
- **ConsistentParentBased**: Parent context-aware decisions
- **ConsistentAnyOf**: OR logic across multiple samplers
- **ConsistentRateLimiting**: Rate limiting with delegate sampler

#### **Strengths**
- **Rich Expressiveness**: Complex logic through composition
- **Statistical Consistency**: Preserves sampling properties
- **Flexible Predicates**: Arbitrary condition matching
- **Mathematical Foundation**: Based on consistent probability theory

#### **Limitations**
- **Complexity**: Steep learning curve for complex configurations
- **SDK-Level Only**: Designed for head-based sampling
- **Experimental Status**: Not yet standardized or widely implemented
- **Performance Unknown**: Complex evaluation may impact performance

## 2. Comparative Analysis

### 2.1 Configuration Complexity Comparison

#### **Simple Rate Limiting (1000 requests/second)**

**Envoy v3:**
```yaml
# Route level
rate_limits:
- actions: [{generic_key: {descriptor_value: "api"}}]

# Filter level  
descriptors:
- entries: [{key: generic_key, value: "api"}]
  token_bucket: {max_tokens: 1000, tokens_per_fill: 1000, fill_interval: 1s}
```

**OTel Collector (Proposed):**
```yaml
# Receiver level
limiters:
- request: [{generic_key: {descriptor_value: "api"}}]
  request_count: [{id: localrate/api}]

# Extension level
extensions:
  localrate/api:
    limiters:
    - token_bucket: {rated: 1000, burst: 1000}
      unit: requests/second
```

**Tail Sampling:**
```yaml
policies:
- name: rate_limit
  type: rate_limiting
  rate_limiting: {spans_per_second: 1000}
```

**OTEP-250:**
```yaml
# SDK configuration (conceptual)
sampler: ConsistentRateLimiting(
  ConsistentFixedThreshold(1.0),
  1000
)
```

**Winner**: Tail Sampling (simplest for basic cases)

### 2.2 Advanced Use Cases Comparison

#### **Multi-Dimensional Rate Limiting (User + Tenant + Operation)**

**Envoy v3:** ✅ **Excellent**
- Natural support through multiple rate_limits
- Independent evaluation of each dimension
- Proven at scale

**OTel Collector (Proposed):** ✅ **Excellent**  
- Three degrees of freedom provide rich expressiveness
- Clean separation of concerns
- Multi-algorithm support

**Tail Sampling:** ❌ **Poor**
- Would require multiple independent policies
- No coordination between dimensions
- Limited to trace-level decisions

**OTEP-250:** 🟡 **Moderate**
- Possible through composition but complex
- Requires nested ConsistentAnyOf structures
- Unclear performance characteristics

#### **Conditional Logic (Premium vs Standard Users)**

**Envoy v3:** 🟡 **Moderate**
- Requires route-level separation or complex header matching
- No native OR logic support
- Workarounds are verbose

**OTel Collector (Proposed):** ✅ **Good**
- Condition matching within patterns
- Clear pattern differentiation
- Follows proven Envoy model

**Tail Sampling:** ❌ **Poor**
- Would need separate policies for each user tier
- No built-in user classification
- Limited policy types

**OTEP-250:** ✅ **Excellent**
- Rich predicate system
- Natural conditional logic
- Hierarchical rule evaluation

### 2.3 Performance Characteristics

| System | Evaluation Complexity | Memory Usage | Network Calls | Hot Path Efficiency |
|--------|----------------------|--------------|---------------|-------------------|
| **Envoy v3** | O(F×P×R×C) | Hash tables + LRU | Global only | Excellent |
| **OTel Collector** | O(L×P×C) | Hash tables + controlled | Local + Global | Excellent |
| **Tail Sampling** | O(P) | Trace buffering | None | Good |
| **OTEP-250** | O(tree depth) | Minimal | None | Unknown |

### 2.4 Expressiveness Matrix

| Feature | Envoy v3 | OTel Collector | Tail Sampling | OTEP-250 |
|---------|----------|----------------|---------------|----------|
| **Pattern Matching** | ✅ Rich | ✅ Rich | 🟡 Limited | ✅ Rich |
| **AND Logic** | ✅ Native | ✅ Native | ❌ None | ✅ Native |
| **OR Logic** | ❌ None | ❌ None | ✅ Native | ✅ Native |
| **Multi-Algorithm** | ❌ Token bucket only | ✅ Extensible | 🟡 Rate only | 🟡 Probability only |
| **Multi-Signal** | ❌ HTTP only | ✅ Traces/Metrics/Logs | 🟡 Traces only | 🟡 Traces only |
| **Resource Limiting** | ❌ Rate only | ✅ Memory/CPU/etc | ❌ Rate only | ❌ Rate only |
| **Hierarchical** | 🟡 Stages | 🟡 Extensions | ❌ Flat | ✅ Full |

## 3. Migration Analysis

### 3.1 Current State Assessment

#### **Tail Sampling Processor Usage Patterns**
Based on the code analysis, common configurations include:
- Simple rate limiting: `rate_limiting: {spans_per_second: X}`
- Attribute filtering: `string_attribute: {key: service.name, values: [...]}`
- Health check exclusion: `string_attribute: {key: http.target, values: [/health]}`
- Error sampling: `numeric_attribute: {key: http.status_code, min_value: 400}`

#### **Migration Complexity Assessment**

**From Tail Sampling to Unified Architecture:**
- **Low Complexity**: Simple rate limiting policies
- **Medium Complexity**: Attribute-based filtering
- **High Complexity**: Complex multi-policy configurations

**Migration Strategy:**
1. **Phase 1**: Implement unified architecture alongside existing systems
2. **Phase 2**: Provide migration tools and configuration converters
3. **Phase 3**: Deprecate existing systems with clear migration paths

### 3.2 Backward Compatibility Requirements

#### **Must Preserve**
- **Existing Configurations**: Current tail sampling configs must continue working
- **Performance Characteristics**: No performance regression for existing use cases
- **Behavioral Semantics**: Same sampling decisions for equivalent configurations

#### **Can Evolve**
- **Configuration Schema**: New schema with migration tools
- **Advanced Features**: New capabilities not available in current systems
- **Internal Implementation**: As long as external behavior is preserved

## 4. Recommendations

### 4.1 Revised Analysis: Tail Sampling as Sophisticated Foundation

**Key Insight**: The tail sampling processor implements sophisticated multi-dimensional resource control with elegant precedence mechanisms that were initially underappreciated.

#### **4.1.1 Multi-Dimensional Resource Control**

**Three Orthogonal Resource Dimensions**:
1. **Memory Control** (`NumTraces`): Automatic admission control with ring buffer eviction
2. **Temporal Control** (`DecisionWait`): Batch processing optimization for efficiency  
3. **Proportional Rate Control** (`SpansPerSecond`): Span-aware rate limiting unique among all systems

**Example**:
```yaml
# Compact configuration hiding sophisticated resource management
decision_wait: 30s          # Batching optimization
num_traces: 50000          # Memory admission control  
policies:
  - type: rate_limiting
    rate_limiting: {spans_per_second: 1000}  # Proportional rate limiting
```

#### **4.1.2 Precedence-Based Policy Composition** 

**Evaluation Pattern**: "Evaluate ALL, OR for decision, FIRST for context"
- All policies evaluate completely (comprehensive analysis)
- Any positive decision triggers sampling (OR logic)
- First positive policy provides downstream context
- Complete decision tracking for observability

This is more sophisticated than Envoy's pure AND logic or OTEP-250's complex hierarchical composition.

#### **4.1.3 Hidden Sophistication**

The seemingly simple configuration masks sophisticated features:
- **Automatic memory management** via ring buffer eviction
- **Batch processing optimization** for performance
- **Concurrent access patterns** with atomic operations
- **Late-arriving span handling** for distributed traces
- **Proportional rate limiting** based on trace complexity

#### **4.1.4 Operational Excellence**

**Self-Tuning Characteristics**:
- Predictable memory usage regardless of traffic spikes
- Automatic batch sizing based on expected traffic
- Built-in performance optimization without manual tuning
- Graceful degradation under resource pressure

### 4.2 Tail Sampling vs Other Systems (Revised)

| Feature | Tail Sampling | Envoy v3 | OTEP-250 |
|---------|---------------|----------|----------|
| **Configuration Complexity** | ✅ Compact elegance | ❌ Verbose complexity | 🟡 Rich but complex |
| **Resource Management** | ✅ Multi-dimensional automatic | 🟡 Manual LRU only | ❌ None |
| **Policy Evaluation** | ✅ Sophisticated precedence | 🟡 Pure AND logic | ✅ Rich composition |
| **Operational Simplicity** | ✅ Self-tuning | ❌ Requires expertise | 🟡 Complex setup |
| **Proportional Limiting** | ✅ Span-aware unique | ❌ Simple token bucket | ❌ Probability only |
| **Production Maturity** | ✅ Widely deployed | ✅ Battle-tested | ❌ Experimental |

### 4.3 Recommendation Update

**Primary Recommendation: Evolve Tail Sampling Processor as Foundation**

**Rationale**: The tail sampling processor reveals sophisticated multi-dimensional resource control with elegant precedence mechanisms that provide the best foundation for unified OpenTelemetry architecture:

- **Proven Sophistication**: Already implements multi-dimensional resource control (memory, time, proportional rate)
- **Elegant Simplicity**: Compact configuration hiding sophisticated resource management
- **Production Maturity**: Widely deployed with edge case handling and performance optimization
- **Unique Capabilities**: Proportional rate limiting and automatic resource management not found elsewhere
- **Operational Excellence**: Self-tuning characteristics requiring minimal configuration expertise

**Evolution Strategy**: Extend tail sampling processor with:
1. **Multi-signal support** (metrics, logs in addition to traces)
2. **Global coordination** capabilities (cluster-wide rate limiting)  
3. **Enhanced policy types** (richer condition matching)
4. **AND logic support** for complex policy composition

This approach **builds on proven sophistication** rather than creating new complexity.

### 4.2 Implementation Strategy

#### **Phase 1: Foundation (6 months)**
1. **Core Extension Types**: Implement `localrate/*`, `globalrate/*`, `localresource/*`
2. **Basic Algorithms**: Token bucket, admission control, sliding window
3. **Request Extractors**: Common patterns from Envoy (headers, metadata, etc.)
4. **Migration Tools**: Converter from tail sampling to unified config

#### **Phase 2: Advanced Features (6 months)**
1. **Multi-Signal Support**: Traces, metrics, logs in unified system
2. **Complex Conditions**: Enhanced pattern matching beyond Envoy
3. **Custom Algorithms**: Plugin system for algorithm extensions
4. **Performance Optimization**: Fine-tuning based on real-world usage

#### **Phase 3: Ecosystem Integration (6 months)**
1. **SDK Integration**: Head-based sampling using unified patterns
2. **Collector Standardization**: Make unified architecture the standard
3. **Tool Ecosystem**: Monitoring, debugging, configuration validation
4. **Documentation**: Comprehensive guides and best practices

### 4.3 Configuration Schema Design

#### **Unified Schema Principles**
1. **Incremental Complexity**: Simple cases should be simple to configure
2. **Rich Expressiveness**: Complex cases should be possible
3. **Type Safety**: Explicit units and validation
4. **Migration Friendly**: Clear mapping from existing systems

#### **Example Unified Configuration**
```yaml
# Level 1: Basic rate limiting
extensions:
  localrate/basic:
    limiters:
    - token_bucket: {rated: 1000, burst: 2000}
      unit: requests/second

receivers:
  otlp:
    limiters:
    - request: [{generic_key: {descriptor_value: "basic"}}]
      request_count: [{id: localrate/basic}]

# Level 2: Multi-tier users  
extensions:
  localrate/user:
    limiters:
    - token_bucket: {rated: 5000, burst: 10000}
      conditions: [{key: user_tier, value: premium}]
    - token_bucket: {rated: 1000, burst: 2000}
      conditions: [{key: user_tier, value: standard}]

# Level 3: Multi-dimensional with global coordination
extensions:
  localrate/user: {as above}
  localrate/tenant:
    limiters:
    - token_bucket: {rated: 50000, burst: 100000}
      conditions: [{key: tenant_id}]  # Wildcard
  globalrate/coordination:
    domain: cluster_limits
    service: {endpoint: ratelimit-service:8081}

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

### 4.4 Addressing Current System Limitations

#### **From Tail Sampling**
- **Multi-Signal Support**: Extend beyond traces to metrics and logs
- **Rich Conditions**: Beyond simple attribute matching
- **Resource Limiting**: Memory, CPU, connection limits
- **Performance**: Avoid trace buffering overhead for simple cases

#### **From OTEP-250**
- **Performance Focus**: Hash-based matching instead of tree evaluation
- **Multi-Signal**: Beyond just trace sampling
- **Operational Simplicity**: Less complex configuration for common cases
- **Production Readiness**: Build on proven patterns

#### **From Envoy**
- **OR Logic**: Support disjunctive conditions where appropriate
- **Multi-Algorithm**: Beyond token buckets to admission control, etc.
- **Better Error Handling**: More sophisticated failure modes
- **Dynamic Configuration**: Runtime configuration updates

## 5. Technical Implementation Details

### 5.1 Extension Type Architecture

#### **Local Rate Limiting Extensions (`localrate/*`)**
```go
type LocalRateLimiter struct {
    Limiters []PatternLimiter
    Cardinality CardinalityConfig
    Stats StatsConfig
}

type PatternLimiter struct {
    Conditions []Condition
    Algorithm Algorithm  // TokenBucket, SlidingWindow, etc.
    Unit Unit           // requests/second, bytes/second, etc.
}
```

#### **Global Rate Limiting Extensions (`globalrate/*`)**
```go
type GlobalRateLimiter struct {
    Domain string
    Service GRPCServiceConfig
    Timeout time.Duration
    FailureMode FailureMode  // allow, deny, degrade
}
```

#### **Resource Admission Extensions (`localresource/*`)**
```go
type ResourceLimiter struct {
    Limiters []ResourcePattern
    ResourceType ResourceType  // memory, connections, etc.
}

type ResourcePattern struct {
    Conditions []Condition
    Algorithm AdmissionAlgorithm  // simple, weighted_fair_queue, etc.
    Limits ResourceLimits
}
```

### 5.2 Request Extractor System

#### **Core Extractor Types**
```go
type RequestExtractor interface {
    Extract(context Context) (Descriptor, error)
}

// Built-in extractors
type RequestHeadersExtractor struct {
    HeaderName string
    DescriptorKey string
}

type OpenTelemetrySignalExtractor struct {
    DescriptorKey string  // Creates {signal: "traces"/"metrics"/"logs"}
}

type ResourceAttributeExtractor struct {
    AttributeKey string
    DescriptorKey string
}
```

### 5.3 Algorithm Plugin System

#### **Algorithm Interface**
```go
type Algorithm interface {
    Allow(request LimitRequest) (bool, error)
    GetStats() AlgorithmStats
}

// Built-in algorithms
type TokenBucketAlgorithm struct {
    MaxTokens int64
    RefillRate int64
    RefillInterval time.Duration
}

type AdmissionControlAlgorithm struct {
    MaxAllowed int64
    MaxWaiting int64
    WaitTimeout time.Duration
}
```

### 5.4 Migration Compatibility Layer

#### **Tail Sampling Compatibility**
```go
// Automatic conversion from tail sampling config
func ConvertTailSamplingConfig(config TailSamplingConfig) (UnifiedConfig, error) {
    unified := UnifiedConfig{}
    
    for _, policy := range config.Policies {
        switch policy.Type {
        case "rate_limiting":
            // Convert to localrate extension
        case "string_attribute":
            // Convert to condition-based pattern
        case "numeric_attribute":
            // Convert to range-based condition
        }
    }
    
    return unified, nil
}
```

## 6. Conclusion

### 6.1 Strategic Recommendation

**Adopt the enhanced Envoy model (Option 2) as the foundation for OpenTelemetry's unified limiter architecture.** This approach provides:

1. **Proven Foundation**: Builds on Envoy's battle-tested architecture
2. **Enhanced Capabilities**: Adds multi-algorithm support, type safety, and resource limiting
3. **Clear Migration Path**: Provides compatibility with existing systems
4. **Future Extensibility**: Three degrees of freedom enable rich future enhancements

### 6.2 Implementation Priorities

1. **Immediate (Q1)**: Design unified schema and implement core local rate limiting
2. **Near-term (Q2-Q3)**: Add global coordination and resource limiting
3. **Medium-term (Q4-Q1)**: Migration tools and ecosystem integration  
4. **Long-term (Q2+)**: Advanced features and performance optimization

### 6.3 Success Metrics

- **Performance**: No regression vs current tail sampling for equivalent configs
- **Adoption**: 50% of new collector deployments using unified architecture within 1 year
- **Migration**: Clear migration path for 90% of existing tail sampling configurations
- **Expressiveness**: Support for advanced use cases not possible with current systems

### 6.4 Risk Mitigation

- **Complexity Risk**: Provide incremental complexity with simple defaults
- **Performance Risk**: Benchmark against existing systems throughout development
- **Migration Risk**: Maintain backward compatibility during transition period
- **Adoption Risk**: Extensive documentation and migration tooling

The unified limiter architecture represents a significant opportunity to consolidate and enhance OpenTelemetry's rate limiting capabilities while building on proven patterns from the broader ecosystem. With careful implementation and migration planning, this approach can provide a solid foundation for the next generation of OpenTelemetry observability infrastructure.
