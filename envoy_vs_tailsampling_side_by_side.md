# Envoy-Style vs Tail Sampling: Side-by-Side Configuration Comparison

## Executive Summary

This document provides a practical comparison between the proposed Envoy-inspired unified collector architecture and the existing OpenTelemetry Tail Sampling Processor. We examine equivalent configurations to highlight the architectural differences, trade-offs, and use cases where each approach excels.

## Comparison Methodology

We'll examine several common sampling scenarios:
1. **Basic Rate Limiting** - Simple spans per second limiting
2. **User Tier Differentiation** - Different limits for different user classes
3. **Error Prioritization** - Always sample errors, rate limit everything else
4. **Multi-Dimensional Control** - Complex policy combinations
5. **Resource Management** - Memory and throughput controls

For each scenario, we'll show:
- **Configuration syntax** for both approaches
- **Evaluation logic** and decision flow
- **Resource management** characteristics
- **Operational complexity** considerations

---

## Scenario 1: Basic Rate Limiting

**Requirement**: Limit to 1000 spans per second across all traces.

### Envoy-Style Collector Configuration

```yaml
# Receiver (Requestor Side)
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
    limiters:
      - request:
          - extractor: opentelemetry_signal
            key: signal_type
        limiter_names: [localrate/basic]

# Extension (Implementor Side) 
extensions:
  localrate/basic:
    unit: spans/second
    limiters:
      - conditions:
          - key: signal_type
            value: trace
        token_bucket:
          rated: 1000
          burst: 1500
        cardinality:
          max_count: 1
          behavior: replace

processors:
  batch: {}

exporters:
  otlp:
    endpoint: http://jaeger:4317

service:
  extensions: [localrate/basic]
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp]
```

**Evaluation Logic**:
```
1. Extract signal_type = "trace" from all incoming requests
2. Match against localrate/basic extension
3. Check token_bucket(1000 spans/sec) 
4. Accept if tokens available, reject otherwise
5. All limiters must pass (AND logic)
```

**Resource Management**:
- **Memory**: Fixed cardinality (max_count: 1)
- **CPU**: O(1) hash lookup + token bucket calculation
- **Predictability**: Explicit resource bounds

### Tail Sampling Configuration

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317

processors:
  tail_sampling:
    decision_wait: 30s
    num_traces: 50000
    expected_new_traces_per_sec: 1000
    policies:
      - name: rate_limit_all
        type: rate_limiting
        rate_limiting:
          spans_per_second: 1000

  batch: {}

exporters:
  otlp:
    endpoint: http://jaeger:4317

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [tail_sampling, batch]
      exporters: [otlp]
```

**Evaluation Logic**:
```
1. Buffer all traces for 30s (decision_wait)
2. Evaluate rate_limiting policy on complete traces
3. Track spans_per_second across all traces
4. Accept traces if cumulative span count < 1000/sec
5. Process decisions in batches
```

**Resource Management**:
- **Memory**: Automatic (50,000 traces max with ring buffer eviction)
- **CPU**: Batch processing optimization
- **Predictability**: Self-tuning with intelligent defaults

### Comparison: Basic Rate Limiting

| Aspect | Envoy-Style | Tail Sampling |
|--------|-------------|---------------|
| **Configuration Lines** | 25 lines | 15 lines |
| **Conceptual Complexity** | Medium (requestor/implementor) | Low (single processor) |
| **Memory Control** | Manual (cardinality limits) | Automatic (ring buffer) |
| **Decision Latency** | Immediate | 30 seconds |
| **Throughput** | Per-request evaluation | Batch processing |
| **Accuracy** | Token bucket approximation | Exact span counting |

---

## Scenario 2: User Tier Differentiation

**Requirement**: Premium users get 5000 spans/sec, standard users get 1000 spans/sec.

### Envoy-Style Collector Configuration

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
    limiters:
      - request:
          - extractor: trace_header
            header_name: "x-user-tier"
            key: user_tier
        limiter_names: [localrate/users]

extensions:
  localrate/users:
    unit: spans/second
    limiters:
      # Premium user pattern
      - conditions:
          - key: user_tier
            value: premium
        token_bucket:
          rated: 5000
          burst: 7500
        cardinality:
          max_count: 100
          behavior: replace
      
      # Standard user pattern  
      - conditions:
          - key: user_tier
            value: standard
        token_bucket:
          rated: 1000
          burst: 1500
        cardinality:
          max_count: 1000
          behavior: replace
      
      # Default pattern (no user tier)
      - conditions:
          - key: user_tier
            # Empty value = wildcard match
        token_bucket:
          rated: 500
          burst: 750
        cardinality:
          max_count: 10000
          behavior: replace

processors:
  batch: {}

exporters:
  otlp:
    endpoint: http://jaeger:4317

service:
  extensions: [localrate/users]
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp]
```

**Evaluation Logic**:
```
1. Extract x-user-tier header value
2. Match against user tier patterns:
   - "premium" → 5000 spans/sec bucket
   - "standard" → 1000 spans/sec bucket  
   - anything else → 500 spans/sec bucket
3. Check appropriate token bucket
4. Track separate buckets per user tier
```

### Tail Sampling Configuration

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317

processors:
  tail_sampling:
    decision_wait: 30s
    num_traces: 50000
    expected_new_traces_per_sec: 2000
    policies:
      # Premium users - higher precedence (first in list)
      - name: premium_users
        type: string_attribute
        string_attribute:
          key: user.tier
          values: [premium]
      
      # Premium user rate limiting
      - name: premium_rate_limit
        type: rate_limiting
        rate_limiting:
          spans_per_second: 5000
      
      # Standard users
      - name: standard_users  
        type: string_attribute
        string_attribute:
          key: user.tier
          values: [standard]
          
      # Standard user rate limiting
      - name: standard_rate_limit
        type: rate_limiting
        rate_limiting:
          spans_per_second: 1000
          
      # Default rate limiting for everyone
      - name: default_rate_limit
        type: rate_limiting  
        rate_limiting:
          spans_per_second: 500

processors:
  batch: {}

exporters:
  otlp:
    endpoint: http://jaeger:4317

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [tail_sampling, batch]
      exporters: [otlp]
```

**Evaluation Logic**:
```
1. Buffer traces for 30s
2. Evaluate ALL policies for each trace:
   - premium_users: Check if user.tier = "premium"
   - premium_rate_limit: Global 5000 spans/sec check
   - standard_users: Check if user.tier = "standard"  
   - standard_rate_limit: Global 1000 spans/sec check
   - default_rate_limit: Global 500 spans/sec check
3. OR logic: If ANY policy says "sample", trace is sampled
4. Problem: No per-user-tier rate limiting, only global limits
```

### Comparison: User Tier Differentiation

| Aspect | Envoy-Style | Tail Sampling |
|--------|-------------|---------------|
| **Per-Tier Rate Limiting** | ✅ Separate buckets per tier | ❌ Global rate limiting only |
| **Configuration Clarity** | ✅ Clear tier → limit mapping | ❌ Confusing policy interactions |
| **Default Handling** | ✅ Explicit wildcard pattern | ❌ Unclear default behavior |
| **Cardinality Control** | ✅ Explicit per-pattern limits | ❌ No per-tier memory control |
| **Accuracy** | ✅ True per-tier limiting | ❌ Incorrect limiting behavior |

**Critical Issue**: Tail sampling **cannot properly implement per-user rate limiting** because all rate_limiting policies share global counters. The OR logic means a premium user could be blocked by standard user limits.

---

## Scenario 3: Error Prioritization

**Requirement**: Always sample error traces, but rate limit everything else to 1000 spans/sec.

### Envoy-Style Collector Configuration

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
    limiters:
      # Request 1: Check for errors
      - request:
          - extractor: span_attribute
            attribute_name: "http.status_code"
            key: status_code
        limiter_names: [localrate/errors]
      
      # Request 2: Rate limit everything
      - request:
          - extractor: opentelemetry_signal
            key: signal_type
        limiter_names: [localrate/general]

extensions:
  localrate/errors:
    unit: spans/second
    limiters:
      # Always allow errors (very high limits)
      - conditions:
          - key: status_code
            value_range: [400, 599]
        token_bucket:
          rated: 1000000
          burst: 1000000
        cardinality:
          max_count: 1000
          behavior: replace

  localrate/general:
    unit: spans/second  
    limiters:
      - conditions:
          - key: signal_type
            value: trace
        token_bucket:
          rated: 1000
          burst: 1500
        cardinality:
          max_count: 1
          behavior: replace

processors:
  batch: {}

exporters:
  otlp:
    endpoint: http://jaeger:4317

service:
  extensions: [localrate/errors, localrate/general]
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp]
```

**Evaluation Logic**:
```
1. Request 1: Extract http.status_code → check error limiter
   - Status 4xx/5xx: Pass (high limits)
   - No status code: No match, pass
2. Request 2: Extract signal_type → check general limiter  
   - All traces: Check 1000 spans/sec bucket
3. AND logic: Both limiters must pass
4. Result: Errors always pass, non-errors rate limited
```

### Tail Sampling Configuration

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317

processors:
  tail_sampling:
    decision_wait: 30s
    num_traces: 50000
    expected_new_traces_per_sec: 1000
    policies:
      # Error sampling (highest precedence)
      - name: sample_errors
        type: numeric_attribute
        numeric_attribute:
          key: http.status_code
          min_value: 400
          max_value: 599
      
      # Rate limit everything else
      - name: rate_limit_general
        type: rate_limiting
        rate_limiting:
          spans_per_second: 1000

processors:
  batch: {}

exporters:
  otlp:
    endpoint: http://jaeger:4317

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [tail_sampling, batch]
      exporters: [otlp]
```

**Evaluation Logic**:
```
1. Buffer traces for 30s
2. Evaluate policies for each trace:
   - sample_errors: Check if any span has status_code 400-599
   - rate_limit_general: Check global 1000 spans/sec limit
3. OR logic: Sample if (error OR under rate limit)
4. Result: Errors always sampled, others rate limited
```

### Comparison: Error Prioritization

| Aspect | Envoy-Style | Tail Sampling |
|--------|-------------|---------------|
| **Logic Correctness** | ✅ AND ensures both checks | ✅ OR provides error priority |
| **Configuration Clarity** | ❌ Complex multi-limiter setup | ✅ Simple precedence rules |
| **Resource Efficiency** | ❌ Evaluates all requests | ✅ Efficient OR short-circuit |
| **Decision Latency** | ✅ Immediate | ❌ 30 second delay |
| **Memory Usage** | ✅ Predictable bounds | ✅ Automatic management |

**Winner**: Tail sampling excels at this pattern due to its elegant precedence mechanism.

---

## Scenario 4: Multi-Dimensional Control

**Requirement**: Critical services get unlimited sampling, premium users get 5000 spans/sec, everyone else gets 1000 spans/sec, but never exceed 10,000 spans/sec total.

### Envoy-Style Collector Configuration

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
    limiters:
      # Request 1: Service-based limiting
      - request:
          - extractor: span_attribute
            attribute_name: "service.name"
            key: service_name
          - extractor: trace_header
            header_name: "x-user-tier"
            key: user_tier
        limiter_names: [localrate/services]
      
      # Request 2: Global throughput cap
      - request:
          - extractor: opentelemetry_signal
            key: signal_type
        limiter_names: [localrate/global_cap]

extensions:
  localrate/services:
    unit: spans/second
    limiters:
      # Critical services - unlimited
      - conditions:
          - key: service_name
            value: critical-payment-service
        token_bucket:
          rated: 1000000
          burst: 1000000
        cardinality:
          max_count: 10
          behavior: replace
      
      # Premium users
      - conditions:
          - key: user_tier
            value: premium
        token_bucket:
          rated: 5000
          burst: 7500
        cardinality:
          max_count: 100
          behavior: replace
      
      # Standard users (wildcard)
      - conditions:
          - key: user_tier
            # Empty = wildcard
        token_bucket:
          rated: 1000
          burst: 1500
        cardinality:
          max_count: 10000
          behavior: replace

  localrate/global_cap:
    unit: spans/second
    limiters:
      - conditions:
          - key: signal_type
            value: trace
        token_bucket:
          rated: 10000
          burst: 15000
        cardinality:
          max_count: 1
          behavior: replace

processors:
  batch: {}

exporters:
  otlp:
    endpoint: http://jaeger:4317

service:
  extensions: [localrate/services, localrate/global_cap]
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp]
```

**Evaluation Logic**:
```
1. Request 1: Extract service.name + user_tier
   - Critical service: Pass (unlimited)
   - Premium user: Check 5000 spans/sec bucket
   - Others: Check 1000 spans/sec bucket
2. Request 2: Check global 10,000 spans/sec cap
3. AND logic: Both must pass
4. Result: Hierarchical limiting with global cap
```

### Tail Sampling Configuration

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317

processors:
  tail_sampling:
    decision_wait: 30s
    num_traces: 100000
    expected_new_traces_per_sec: 5000
    policies:
      # Critical services (highest precedence)
      - name: critical_services
        type: string_attribute
        string_attribute:
          key: service.name
          values: [critical-payment-service, critical-auth-service]
      
      # Premium users
      - name: premium_users
        type: string_attribute
        string_attribute:
          key: user.tier
          values: [premium]
      
      # Premium rate limiting
      - name: premium_rate_limit
        type: rate_limiting
        rate_limiting:
          spans_per_second: 5000
      
      # General rate limiting
      - name: general_rate_limit
        type: rate_limiting
        rate_limiting:
          spans_per_second: 1000
          
      # Global cap (most restrictive)
      - name: global_cap
        type: rate_limiting
        rate_limiting:
          spans_per_second: 10000

processors:
  batch: {}

exporters:
  otlp:
    endpoint: http://jaeger:4317

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [tail_sampling, batch]
      exporters: [otlp]
```

**Evaluation Logic**:
```
1. Evaluate ALL policies:
   - critical_services: Sample if service.name matches
   - premium_users: Sample if user.tier = premium
   - premium_rate_limit: Sample if global count < 5000/sec
   - general_rate_limit: Sample if global count < 1000/sec
   - global_cap: Sample if global count < 10000/sec
2. OR logic: Sample if ANY policy passes
3. Problem: Global rate limiting conflicts, no per-tier enforcement
```

### Comparison: Multi-Dimensional Control

| Aspect | Envoy-Style | Tail Sampling |
|--------|-------------|---------------|
| **Hierarchical Limiting** | ✅ True hierarchy with AND logic | ❌ Cannot implement properly |
| **Global Cap Enforcement** | ✅ Enforced across all tiers | ❌ OR logic breaks cap |
| **Per-Tier Accounting** | ✅ Separate buckets | ❌ Shared global counters |
| **Configuration Complexity** | ❌ High (multi-limiter setup) | ✅ Simple policy list |
| **Correctness** | ✅ Implements requirement exactly | ❌ Incorrect behavior |

**Critical Issue**: Tail sampling fundamentally **cannot implement hierarchical rate limiting** due to its OR-based evaluation and shared rate limiting counters.

---

## Scenario 5: Resource Management

**Requirement**: Protect collector from memory exhaustion while maintaining predictable performance.

### Envoy-Style Collector Configuration

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
    limiters:
      - request:
          - extractor: trace_header
            header_name: "x-tenant-id"
            key: tenant_id
        limiter_names: [localresource/memory, localrate/throughput]

extensions:
  localresource/memory:
    unit: request_bytes
    limiters:
      - conditions:
          - key: tenant_id
            # Wildcard for all tenants
        admission:
          allowed: 100000000  # 100MB total
          waiting: 50000000   # 50MB queue
        cardinality:
          max_count: 1000     # Max 1000 tenants
          behavior: refuse    # Refuse new tenants when full

  localrate/throughput:
    unit: spans/second
    limiters:
      - conditions:
          - key: tenant_id
        token_bucket:
          rated: 10000
          burst: 15000
        cardinality:
          max_count: 1000
          behavior: replace

processors:
  batch:
    send_batch_size: 1024
    timeout: 1s

exporters:
  otlp:
    endpoint: http://jaeger:4317

service:
  extensions: [localresource/memory, localrate/throughput]
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp]
```

**Resource Management**:
- **Memory**: Explicit 100MB limit with admission control
- **Cardinality**: Max 1000 tenants, refuse new ones when full
- **Throughput**: 10,000 spans/sec with burst handling
- **Predictability**: All limits explicit and configurable

### Tail Sampling Configuration

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317

processors:
  tail_sampling:
    decision_wait: 30s
    num_traces: 100000           # Memory control
    expected_new_traces_per_sec: 5000  # Throughput hint
    policies:
      - name: rate_limit_all
        type: rate_limiting
        rate_limiting:
          spans_per_second: 10000

  batch:
    send_batch_size: 1024
    timeout: 1s

exporters:
  otlp:
    endpoint: http://jaeger:4317

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [tail_sampling, batch]
      exporters: [otlp]
```

**Resource Management**:
- **Memory**: Automatic (100,000 traces × ~5KB = ~500MB estimated)
- **Eviction**: Ring buffer FIFO eviction when capacity exceeded
- **Throughput**: Built-in batch processing optimization
- **Predictability**: Self-tuning with intelligent defaults

### Comparison: Resource Management

| Aspect | Envoy-Style | Tail Sampling |
|--------|-------------|---------------|
| **Memory Control** | ✅ Explicit byte limits | ❌ Trace count approximation |
| **Cardinality Management** | ✅ Explicit tenant limits | ❌ No tenant isolation |
| **Resource Predictability** | ✅ Deterministic bounds | ❌ Estimated behavior |
| **Operational Complexity** | ❌ Requires capacity planning | ✅ Self-tuning defaults |
| **Multi-Tenant Safety** | ✅ Per-tenant resource limits | ❌ Shared resource pool |

---

## Overall Architecture Comparison

### Configuration Complexity

| Scenario | Envoy-Style Lines | Tail Sampling Lines | Winner |
|----------|------------------|-------------------|---------|
| Basic Rate Limiting | 25 | 15 | Tail Sampling |
| User Tier Differentiation | 45 | 25 | Neither (TS broken) |
| Error Prioritization | 40 | 20 | Tail Sampling |
| Multi-Dimensional | 70 | 30 | Neither (TS broken) |
| Resource Management | 35 | 20 | Tail Sampling |

### Functional Capabilities

| Capability | Envoy-Style | Tail Sampling |
|------------|-------------|---------------|
| **Per-Entity Rate Limiting** | ✅ Full support | ❌ Global counters only |
| **Hierarchical Limiting** | ✅ AND logic enables | ❌ OR logic prevents |
| **Error Prioritization** | ✅ Complex but works | ✅ Elegant and simple |
| **Multi-Dimensional Control** | ✅ Full expressiveness | ❌ Fundamental limitations |
| **Resource Management** | ✅ Explicit control | ❌ Approximations only |
| **Decision Latency** | ✅ Immediate | ❌ Configurable delay |
| **Memory Efficiency** | ❌ Manual tuning required | ✅ Automatic optimization |
| **Operational Complexity** | ❌ High expertise required | ✅ Self-tuning |

### Performance Characteristics

| Aspect | Envoy-Style | Tail Sampling |
|--------|-------------|---------------|
| **CPU Overhead** | Medium (per-request evaluation) | Low (batch processing) |
| **Memory Overhead** | Low (hash tables + counters) | High (trace buffering) |
| **Latency** | Low (immediate decisions) | High (30s decision wait) |
| **Throughput** | High (optimized evaluation) | High (batch processing) |
| **Scalability** | Linear with request rate | Linear with trace rate |

---

## Recommendations by Use Case

### **Choose Envoy-Style When:**
- ✅ **Per-entity rate limiting** is required (per-user, per-tenant, per-service)
- ✅ **Hierarchical limiting** with global caps is needed
- ✅ **Immediate decisions** are critical (real-time processing)
- ✅ **Explicit resource control** is required for multi-tenant environments
- ✅ **Complex AND logic** combinations are needed

### **Choose Tail Sampling When:**
- ✅ **Simple precedence rules** suffice (error prioritization)
- ✅ **Decision latency is acceptable** (batch processing scenarios)
- ✅ **Operational simplicity** is prioritized over configuration flexibility
- ✅ **Automatic resource management** is preferred
- ✅ **Complete trace context** is needed for decisions

### **Avoid Tail Sampling When:**
- ❌ Per-entity rate limiting is required
- ❌ Hierarchical limiting with caps is needed
- ❌ Immediate decisions are critical
- ❌ Multi-tenant resource isolation is required

---

## Conclusion

The comparison reveals **complementary strengths** rather than a clear winner:

### **Envoy-Style Excels At:**
- **Complex rate limiting scenarios** requiring per-entity accounting
- **Hierarchical control** with global caps and local limits
- **Multi-tenant environments** with resource isolation needs
- **Real-time decision making** with immediate feedback

### **Tail Sampling Excels At:**
- **Simple precedence-based decisions** with elegant OR logic
- **Operational simplicity** with self-tuning characteristics
- **Batch processing efficiency** for high-throughput scenarios
- **Automatic resource management** reducing operational overhead

### **Unified Architecture Recommendation**

The ideal solution would **combine both approaches**:

1. **Envoy-style extensions** for complex rate limiting scenarios
2. **Tail sampling processor** for simple precedence-based decisions  
3. **Configuration-driven selection** between evaluation strategies
4. **Shared resource management** infrastructure

This would provide the **expressiveness of Envoy-style configuration** with the **operational simplicity of tail sampling** where appropriate.
