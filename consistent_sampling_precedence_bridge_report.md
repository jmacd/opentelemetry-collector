# Bridging OTEP-250 Consistent Sampling with Tail Sampling Precedence

## Executive Summary

This report addresses how to integrate OTEP-250's consistent probability sampling (based on OTEP-235 thresholds) with the OpenTelemetry Tail Sampling Processor's sophisticated precedence-based policy evaluation. The key insight is that **each decision path in the precedence tree must be accompanied by a sampling threshold** to enable consistent sampling decisions across SDKs and collectors.

## Problem Statement

### Current State
- **OTEP-250**: Defines consistent probability sampling using threshold-based decisions for SDKs
- **Tail Sampling Processor**: Uses precedence-based OR logic without threshold tracking
- **Gap**: No mechanism to compute consistent sampling thresholds from precedence-based decisions

### Goal
Enable the tail sampling processor's precedence mechanism to generate **consistent sampling thresholds** that are compatible with OTEP-235/250, allowing:
- **Cross-system consistency** between SDK head sampling and collector tail sampling
- **Statistical correctness** for sampling rate calculations
- **Threshold propagation** through trace context for downstream systems

## Core Architecture: Threshold-Aware Precedence

### Current Tail Sampling Decision Flow
```go
// Current implementation - no threshold tracking
func (tsp *tailSamplingSpanProcessor) makeDecision(trace *TraceData) (Decision, *Policy) {
    finalDecision := sampling.NotSampled
    var matchingPolicy *Policy = nil

    for i, policy := range tsp.policies {
        decision, err := policy.Evaluator.Evaluate(trace)
        trace.Decisions[i] = decision

        switch decision {
        case sampling.Sampled:
            finalDecision = sampling.Sampled  // OR logic
            if matchingPolicy == nil {
                matchingPolicy = policy       // First match provides context
            }
        }
    }
    return finalDecision, matchingPolicy
}
```

### Proposed Threshold-Aware Decision Flow
```go
// Enhanced implementation with threshold tracking
type ThresholdDecision struct {
    Decision  sampling.Decision
    Threshold string  // 14-character hex string or null for non-probabilistic
    Policy    *Policy
}

func (tsp *tailSamplingSpanProcessor) makeThresholdDecision(trace *TraceData) ThresholdDecision {
    var candidateDecisions []ThresholdDecision
    
    // Evaluate ALL policies and collect threshold decisions
    for i, policy := range tsp.policies {
        decision, threshold, err := policy.Evaluator.EvaluateWithThreshold(trace)
        trace.Decisions[i] = decision
        
        if decision == sampling.Sampled {
            candidateDecisions = append(candidateDecisions, ThresholdDecision{
                Decision:  decision,
                Threshold: threshold,
                Policy:    policy,
            })
        }
    }
    
    // Apply precedence rule with threshold computation
    return computeConsistentThreshold(candidateDecisions)
}

func computeConsistentThreshold(candidates []ThresholdDecision) ThresholdDecision {
    if len(candidates) == 0 {
        return ThresholdDecision{Decision: sampling.NotSampled, Threshold: "null"}
    }
    
    // Precedence rule: lexicographical minimum threshold (most restrictive)
    // This ensures consistent sampling across all scenarios
    minThreshold := "ffffffffffffff"  // Maximum threshold
    var selectedDecision ThresholdDecision
    
    for _, candidate := range candidates {
        if candidate.Threshold != "null" && candidate.Threshold < minThreshold {
            minThreshold = candidate.Threshold
            selectedDecision = candidate
        }
    }
    
    return selectedDecision
}
```

## Policy-Specific Threshold Generation

### Enhanced Policy Evaluator Interface
```go
type ThresholdPolicyEvaluator interface {
    // Existing method for backward compatibility
    Evaluate(traceID pdata.TraceID, trace *TraceData) (Decision, error)
    
    // New method for threshold-aware evaluation
    EvaluateWithThreshold(traceID pdata.TraceID, trace *TraceData) (Decision, string, error)
}
```

### Policy-Specific Threshold Implementations

#### **1. Always Sample Policy**
```go
func (a *alwaysSample) EvaluateWithThreshold(traceID pdata.TraceID, trace *TraceData) (Decision, string, error) {
    return sampling.Sampled, "00000000000000", nil  // Minimum threshold = always sample
}
```

#### **2. Rate Limiting Policy**
```go
func (r *rateLimiting) EvaluateWithThreshold(traceID pdata.TraceID, trace *TraceData) (Decision, string, error) {
    // Current rate limiting logic
    spansInSecondIfSampled := r.spansInCurrentSecond + trace.SpanCount
    if spansInSecondIfSampled < r.spansPerSecond {
        r.spansInCurrentSecond = spansInSecondIfSampled
        
        // Calculate threshold based on current rate limiting state
        // Higher utilization = higher threshold (more restrictive)
        utilizationRatio := float64(r.spansInCurrentSecond) / float64(r.spansPerSecond)
        threshold := calculateThresholdFromUtilization(utilizationRatio)
        
        return sampling.Sampled, threshold, nil
    }
    
    return sampling.NotSampled, "null", nil
}

func calculateThresholdFromUtilization(utilization float64) string {
    // Convert utilization ratio to 14-character hex threshold
    // Higher utilization = higher threshold value (more restrictive)
    thresholdValue := uint64(utilization * 0x3FFFFFFFFFFFFF)  // 56-bit threshold space
    return fmt.Sprintf("%014x", thresholdValue)
}
```

#### **3. Attribute-Based Policies**
```go
func (s *stringAttribute) EvaluateWithThreshold(traceID pdata.TraceID, trace *TraceData) (Decision, string, error) {
    // Check if attribute matches
    if s.matchesAttribute(trace) {
        // Attribute-based policies use trace ID for threshold
        // Extract randomness from trace ID (OTEP-235 approach)
        randomness := extractRandomnessFromTraceID(traceID)
        
        // For "always sample matching attributes", use minimum threshold
        threshold := "00000000000000"
        
        return sampling.Sampled, threshold, nil
    }
    
    return sampling.NotSampled, "null", nil
}

func (n *numericAttribute) EvaluateWithThreshold(traceID pdata.TraceID, trace *TraceData) (Decision, string, error) {
    if n.matchesNumericRange(trace) {
        // For error traces (status code >= 400), always sample
        threshold := "00000000000000"
        return sampling.Sampled, threshold, nil
    }
    
    return sampling.NotSampled, "null", nil
}
```

## Threshold Composition Rules

### Precedence-Based Threshold Selection

The key insight is that **OR logic with threshold-aware policies** requires selecting the **most restrictive threshold** (lexicographically minimum) among all positive decisions:

```go
// Example scenario with multiple policies firing
policies := []Policy{
    {name: "error_traces",    threshold: "00000000000000"},  // Always sample errors
    {name: "rate_limiter",    threshold: "8000000000000"},   // 50% current utilization
    {name: "critical_service", threshold: "00000000000000"},  // Always sample critical
}

// Result: threshold = "00000000000000" (minimum = most permissive)
// This ensures that if ANY policy wants to sample, we sample
// But threshold reflects the most restrictive policy that would sample
```

### Mathematical Foundation

The threshold composition follows **lexicographical minimum** rule:
- `threshold_final = min(threshold_1, threshold_2, ..., threshold_n)` for all policies returning `Sampled`
- This ensures **statistical consistency** across different policy combinations
- Preserves **OR semantics** while enabling **threshold propagation**

## Configuration Evolution

### Current Configuration (No Thresholds)
```yaml
policies:
  - name: error_traces
    type: numeric_attribute
    numeric_attribute: {key: http.status_code, min_value: 400, max_value: 599}
  - name: rate_limit
    type: rate_limiting
    rate_limiting: {spans_per_second: 1000}
  - name: critical_service
    type: string_attribute
    string_attribute: {key: service.name, values: [payment-service]}
```

### Enhanced Configuration (With Threshold Control)
```yaml
policies:
  - name: error_traces
    type: numeric_attribute
    numeric_attribute: {key: http.status_code, min_value: 400, max_value: 599}
    threshold_mode: always_sample  # threshold = "00000000000000"
    
  - name: rate_limit
    type: rate_limiting
    rate_limiting: {spans_per_second: 1000}
    threshold_mode: adaptive       # threshold based on current utilization
    
  - name: critical_service
    type: string_attribute
    string_attribute: {key: service.name, values: [payment-service]}
    threshold_mode: always_sample  # threshold = "00000000000000"
    
  - name: background_sampling
    type: probabilistic
    probabilistic: {sampling_rate: 0.01}  # threshold = "028f5c28f5c28f"
```

## Integration with OTEP-235/250

### Threshold Propagation in Trace Context

```go
// Enhanced trace processing with threshold propagation
func (tsp *tailSamplingSpanProcessor) processThresholdDecision(decision ThresholdDecision, spans []pdata.Span) {
    if decision.Decision == sampling.Sampled {
        // Propagate threshold in trace state (OTEP-235)
        for _, span := range spans {
            traceState := span.TraceState()
            
            // Update or set 'th' value in 'ot' key
            updatedTraceState := updateOTThreshold(traceState, decision.Threshold)
            span.SetTraceState(updatedTraceState)
        }
        
        // Forward to next consumer with policy context
        tsp.nextConsumer.ConsumeTraces(decision.Policy.ctx, spans)
    }
}

func updateOTThreshold(traceState string, threshold string) string {
    // Parse existing trace state
    entries := parseTraceState(traceState)
    
    // Update 'ot' key with threshold value
    entries["ot"] = "th:" + threshold
    
    return formatTraceState(entries)
}
```

### SDK Compatibility

```go
// SDK head sampling can now use same threshold for consistency
type ConsistentTailSamplingCompatibleSampler struct {
    policies []ConsistentSampler  // OTEP-250 samplers
}

func (c *ConsistentTailSamplingCompatibleSampler) ShouldSample(ctx SamplingContext) SamplingResult {
    var candidateThresholds []string
    
    // Evaluate policies similar to tail sampling
    for _, policy := range c.policies {
        intent := policy.GetSamplingIntent(ctx)
        if intent.Threshold != "null" {
            candidateThresholds = append(candidateThresholds, intent.Threshold)
        }
    }
    
    // Use same precedence rule as tail sampling
    finalThreshold := computeMinThreshold(candidateThresholds)
    
    // Make sampling decision based on trace ID randomness
    decision := compareThresholdWithTraceID(finalThreshold, ctx.TraceID)
    
    return SamplingResult{
        Decision:   decision,
        TraceState: updateTraceStateWithThreshold(ctx.TraceState, finalThreshold),
    }
}
```

## Equivalence with Envoy Configuration

### Tail Sampling Policy Tree
```yaml
# Tail sampling configuration
policies:
  - name: errors
    type: numeric_attribute
    numeric_attribute: {key: http.status_code, min_value: 400}
  - name: rate_limit
    type: rate_limiting  
    rate_limiting: {spans_per_second: 1000}
```

### Equivalent Envoy Configuration
```yaml
# Envoy rate limiting with equivalent logic
rate_limits:
- stage: 0  # Error sampling
  actions:
  - header_value_match:
      descriptor_value: "error_sample"
      headers: [{name: "http-status-code", safe_regex: {regex: "[4-5][0-9][0-9]"}}]
      
- stage: 1  # Rate limiting
  actions:  
  - generic_key: {descriptor_value: "rate_limit"}

# Local rate limit filter
descriptors:
- entries: [{key: header_match, value: "error_sample"}]
  token_bucket: {max_tokens: 999999999, tokens_per_fill: 999999999}  # Always sample
- entries: [{key: generic_key, value: "rate_limit"}]
  token_bucket: {max_tokens: 1000, tokens_per_fill: 1000, fill_interval: 1s}
```

### Key Equivalence Points

| Tail Sampling Feature | Envoy Equivalent | Threshold Mapping |
|----------------------|------------------|-------------------|
| **OR Logic** | Multiple stage evaluation | `min(threshold_1, threshold_2, ...)` |
| **Policy Precedence** | Stage ordering + descriptor matching | First matching threshold |
| **Rate Limiting** | Token bucket descriptors | Utilization-based threshold |
| **Attribute Matching** | Header/metadata descriptors | Fixed threshold per match |
| **Decision Context** | Policy context propagation | Threshold in trace state |

## Implementation Roadmap

### Phase 1: Threshold Infrastructure
1. **Enhanced PolicyEvaluator Interface**: Add `EvaluateWithThreshold` method
2. **Threshold Decision Types**: `ThresholdDecision` struct and computation logic
3. **Trace State Integration**: Threshold propagation in trace context

### Phase 2: Policy-Specific Thresholds
1. **Rate Limiting Thresholds**: Utilization-based threshold calculation
2. **Attribute Policy Thresholds**: Fixed thresholds for matching attributes
3. **Probabilistic Policy**: Direct threshold specification

### Phase 3: SDK Integration
1. **Head Sampling Compatibility**: SDK samplers using same precedence rules
2. **Threshold Consistency**: Cross-system threshold validation
3. **Migration Tools**: Convert existing configurations to threshold-aware versions

### Phase 4: Advanced Features
1. **Dynamic Threshold Adjustment**: Runtime threshold modification
2. **Threshold Monitoring**: Observability for threshold decisions
3. **Policy Composition**: Complex threshold combination rules

## Benefits of Threshold-Aware Precedence

### Statistical Consistency
- **Predictable sampling rates** across different policy combinations
- **Consistent behavior** between head and tail sampling
- **Mathematical foundation** for sampling rate calculations

### Operational Benefits
- **Unified sampling strategy** across SDKs and collectors
- **Threshold observability** for debugging sampling decisions
- **Gradual migration** from current tail sampling to consistent sampling

### OTEP-250 Compatibility
- **Direct integration** with consistent probability sampling
- **Threshold propagation** through trace context
- **Cross-system sampling consistency** for distributed traces

## Conclusion

The tail sampling processor's precedence-based policy evaluation can be enhanced to generate **consistent sampling thresholds** that are fully compatible with OTEP-235/250. The key innovation is:

1. **Each policy generates a threshold** when making positive sampling decisions
2. **Precedence rule becomes threshold composition**: lexicographical minimum of all positive thresholds
3. **OR logic preserved**: Any policy can trigger sampling, but threshold reflects most restrictive policy
4. **Statistical consistency**: Enables predictable sampling rates and cross-system compatibility

This approach **preserves the elegant precedence mechanism** of tail sampling while **enabling threshold-based consistency** required for OTEP-250 integration. The result is a unified sampling architecture that works consistently across SDKs and collectors while maintaining the operational simplicity of the current tail sampling processor.
