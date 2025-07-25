# Envoy Rate Limiting Architecture Guide

## Executive Summary

This guide explains Envoy's rate limiting system through its fundamental architectural patterns. The key insight is understanding the **Requestor/Implementor pattern** with **asymmetric nesting complexity** that creates a **cross-product evaluation challenge**. We'll start with core concepts, then dive into implementation details.

## 1. Core Architectural Concepts

### 1.1 Requestor/Implementor Pattern

Envoy's rate limiting follows a clear **Requestor/Implementor** architecture that separates data collection from policy enforcement:

**Requestor Side (Route Configuration):**
Rate limit requests are built during route processing through configurable **request extractors** that form key-value descriptors (e.g., by extracting request headers, static metadata, source/dest, etc.). All rate limiting filters receive and process the same set of rate limit requests.

**Implementor Side (Filter Configuration):**
- **Local limiters**: Configured with **rate limit patterns**, each containing matching conditions (key specifications with optional exact values) and token bucket parameters. Incoming rate limit requests are matched against these patterns - all matching patterns are evaluated, and all must pass.
- **Global limiters**: Forward all rate limit requests to external services which handle the limiting logic.

Both types configure instrumentation (stats prefix) and define failure behavior (local: LRU overflow, global: service timeout/failure modes).

### 1.2 Asymmetric Nesting Structure

The rate limiting system has **asymmetric nesting depth** reflecting different responsibilities:

#### **Requestor Side - 2 Levels (Data Collection)**

```
Route
└── Rate Limit Requests (multiple, all applied)
    └── Request Extractors (multiple per request, combined into single descriptor)
```

**Logic**: 
- Between Requests: AND (all requests must pass all filters)
- Within Request: COMBINE (all extractors contribute to single descriptor)

#### **Implementor Side - 3 Levels (Pattern Matching + Policy)**

```
Filter Chain
└── Rate Limiting Filters (multiple, all must pass)
    └── Rate Limit Patterns (multiple per filter, all must pass)
        └── Key Conditions (multiple per pattern, all must match)
```

**Logic**:
- Between Filters: AND (all filters must approve)
- Between Patterns: AND (all matching patterns must pass)  
- Within Pattern: AND (all conditions must match for pattern to apply)

### 1.3 Cross-Product Algorithmic Complexity

The asymmetric nesting structure creates a **cross-product matching problem** where Requestor-generated data must be evaluated against Implementor pattern configurations:

#### **The Cross-Product Challenge**

```
For each Rate Limit Filter:
  For each Rate Limit Pattern in filter:
    For each Rate Limit Request from route:
      Check if ALL key conditions in pattern match the request
```

#### **Why Multiple Items at Each Level?**

Understanding why we need multiple items at each nesting level reveals the architectural reasoning:

**Multiple Filters (F > 1):**
- **Local + Global**: Fast local checks followed by coordinated global limits
- **Different Services**: Infrastructure limits vs application limits vs business rules
- **Performance Tiers**: Microsecond local → millisecond network → second business logic
- **Failure Modes**: Fail-open monitoring vs fail-closed security

```yaml
http_filters:
- name: local_ratelimit    # Filter 1: Fast local protection
- name: global_ratelimit   # Filter 2: Coordinated global limits
```

**Multiple Patterns per Filter (P > 1):**
- **Different User Tiers**: Standard vs premium vs enterprise rate limits
- **Resource Granularity**: Per-user AND per-tenant AND per-operation limits
- **Conditional Logic**: Different limits based on request characteristics
- **Hierarchical Controls**: Broad limits + specific exceptions

```yaml
descriptors:  # Multiple patterns in same filter
- entries: [{key: user_tier, value: "premium"}]   # Pattern 1: Premium users
  token_bucket: {max_tokens: 1000, ...}
- entries: [{key: user_tier, value: "standard"}]  # Pattern 2: Standard users  
  token_bucket: {max_tokens: 100, ...}
- entries: [{key: operation, value: "upload"}]    # Pattern 3: Upload operations
  token_bucket: {max_tokens: 50, ...}
```

#### **Key Limitation: No "Catch-All" or "Default" Patterns**

**You CANNOT create mutually exclusive patterns with wildcards.** This is a major limitation:

```yaml
# THIS DOESN'T WORK - You can't specify "all other users"
descriptors:
- entries: [{key: user_id, value: "alice"}]     # Specific user
  token_bucket: {max_tokens: 1000, ...}
- entries: [{key: user_id, value: "*"}]         # ❌ INVALID - no wildcard matching
  token_bucket: {max_tokens: 100, ...}
```

**The Problem**: Envoy's local rate limiting requires **exact value matches** or **wildcard (empty value) matches**, but you can't create "everything except X" patterns.

#### **Workarounds for Mutually Exclusive Patterns**

**1. Route-Level Separation (Recommended):**

```yaml
# Route 1: Special user gets different rate limiting
routes:
- match: 
    prefix: "/api"
    headers: [{name: "x-user-id", exact_match: "alice"}]
  route: {cluster: backend}
  typed_per_filter_config:
    envoy.filters.http.local_ratelimit:
      descriptors:
      - entries: [{key: user_id, value: "alice"}]
        token_bucket: {max_tokens: 1000, ...}

# Route 2: All other users get standard rate limiting  
- match: {prefix: "/api"}
  route: {cluster: backend}
  typed_per_filter_config:
    envoy.filters.http.local_ratelimit:
      descriptors:
      - entries: [{key: user_id}]  # Wildcard - matches any user_id
        token_bucket: {max_tokens: 100, ...}
```

**2. Use Different Header/Key for Tiers:**

```yaml
# Route creates user_tier instead of user_id
rate_limits:
- actions:
  - header_value_match:
      descriptor_value: "premium"
      headers: [{name: "x-user-id", exact_match: "alice"}]
- actions:  
  - header_value_match:
      descriptor_value: "standard"
      headers: [{name: "x-user-id", present_match: true}]  # Anyone with header

# Filter matches on tier
descriptors:
- entries: [{key: user_tier, value: "premium"}]
  token_bucket: {max_tokens: 1000, ...}
- entries: [{key: user_tier, value: "standard"}]
  token_bucket: {max_tokens: 100, ...}
```

**3. Global Rate Limiting with External Logic:**

```yaml
# Send all requests to external service
rate_limits:
- actions:
  - request_headers: {header_name: "x-user-id", descriptor_key: "user_id"}

# External rate limit service handles the logic:
# if user_id == "alice": return 1000 tokens
# else: return 100 tokens
```

#### **Why This Limitation Exists**

Local rate limiting is designed for **high performance** with **hash-based lookups**:

- **Exact matches**: O(1) hash lookup
- **Wildcard matches**: Single token bucket for all values
- **"Everything except X"**: Would require O(n) scanning or complex logic

The trade-off is **performance** vs **configuration flexibility**.

#### **Descriptor Consistency Requirements**

**No, there are NO consistency requirements** between descriptors. You can freely mix exact matches and wildcards:

```yaml
descriptors:
- entries: [{key: user_id, value: "alice"}]        # Exact match for specific user
  token_bucket: {max_tokens: 1000, ...}
- entries: [{key: user_id}]                        # Wildcard for all other users  
  token_bucket: {max_tokens: 100, ...}
- entries: [{key: tenant_id, value: "company_a"}]  # Exact match for specific tenant
  token_bucket: {max_tokens: 500, ...}
- entries: [{key: operation}]                      # Wildcard for all operations
  token_bucket: {max_tokens: 200, ...}
```

**IMPORTANT: Documentation vs Implementation Discrepancy**

The **official Envoy documentation** says that matching descriptors are "sorted by tokens per second and try to consume tokens in order, in most cases if one of them is limited, the remaining descriptors will not consume their tokens."

However, examining the **actual source code**, I found that:

1. **All matching descriptors are found** and collected
2. **Descriptors are sorted by fill rate** (most restrictive first)  
3. **Each descriptor's token bucket is checked and consumed in order**
4. **If any descriptor fails, the request is rejected** (but tokens from previous descriptors were already consumed)

**This means:**
- **Alice with `user_id: "alice"`** would match BOTH the specific descriptor AND the wildcard descriptor
- **Both token buckets would be consumed** (if they have tokens available)
- **If either bucket is empty, the request fails**
- **Tokens from the first bucket are consumed even if the second bucket fails**

**Therefore, you CANNOT create "alice gets 1000, everyone else gets 100" patterns** because alice would be subject to BOTH limits and need to pass the more restrictive one.

**The pattern you want requires using route-level separation or external rate limiting services.**

#### **Practical Solution: Classification Filter Chain Pattern**

To achieve "alice gets 1000, everyone else gets 100" behavior, you need to **classify users into exhaustive categories** earlier in the filter chain:

**Step 1: User Classification Filter**
```yaml
http_filters:
# Classification filter runs BEFORE rate limiting
- name: envoy.filters.http.lua  # or header_manipulation, etc.
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.lua.v3.Lua
    inline_code: |
      function envoy_on_request(request_handle)
        local user_id = request_handle:headers():get("x-user-id")
        if user_id == "alice" or user_id == "bob" then
          request_handle:headers():add("x-user-tier", "premium")
        else
          request_handle:headers():add("x-user-tier", "standard") 
        end
      end

# Rate limiting filter runs AFTER classification
- name: envoy.filters.http.local_ratelimit
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
    descriptors:
    - entries: [{key: user_tier, value: "premium"}]   # Alice, Bob, etc.
      token_bucket: {max_tokens: 1000, ...}
    - entries: [{key: user_tier, value: "standard"}]  # Everyone else
      token_bucket: {max_tokens: 100, ...}
```

**Step 2: Route Actions Extract the Classification**
```yaml
routes:
- match: {prefix: "/api"}
  route: {cluster: backend}
  rate_limits:
  - actions:
    - request_headers:
        header_name: "x-user-tier"      # Uses the classification header
        descriptor_key: "user_tier"     # Not the original user_id
```

**Result**: Each user gets classified into exactly ONE tier, avoiding overlapping descriptors.

**Alternative: Header Value Match Actions**
```yaml
# Classification happens in route actions themselves using header_value_match
rate_limits:
- actions:
  - header_value_match:
      descriptor_value: "premium"         # Category, not specific user
      headers: [{name: "x-user-id", string_match: {exact: "alice"}}]
- actions:  
  - header_value_match:
      descriptor_value: "premium"
      headers: [{name: "x-user-id", string_match: {exact: "bob"}}]
- actions:
  - header_value_match:
      descriptor_value: "standard" 
      headers: [{name: "x-user-id", string_match: {safe_regex: {regex: ".*"}}}]
```

**Key Insight**: Transform the **overlapping problem** (specific user + wildcard) into a **partitioning problem** (mutually exclusive categories) before rate limiting sees it.

#### **Action Type Comparison: header_value_match vs generic_key**

You're absolutely right - `header_value_match` and `generic_key` are very similar! Both create descriptors with **predetermined values** rather than extracting dynamic values from requests:

**generic_key - Unconditional Static Descriptor:**
```yaml
rate_limits:
- actions:
  - generic_key:
      descriptor_value: "api_service"    # Always creates {generic_key: "api_service"}
```

**header_value_match - Conditional Static Descriptor:**
```yaml
rate_limits:
- actions:
  - header_value_match:
      descriptor_value: "premium"       # Creates {header_match: "premium"} IF header matches
      headers: [{name: "x-user-tier", exact: "premium"}]
```

**Key Differences:**

| Feature | `generic_key` | `header_value_match` |
|---------|---------------|----------------------|
| **Condition** | Always fires | Only if header(s) match |
| **Use Case** | Static classification | Conditional classification |
| **Result** | Always creates descriptor | May create descriptor (or none) |
| **Flexibility** | Simple, reliable | Complex matching logic |

**When to Use Which:**

- **generic_key**: When you want to **always** apply a specific rate limit
- **header_value_match**: When you want to **conditionally** apply rate limits based on request characteristics

**Combined Pattern for Exhaustive Classification:**
```yaml
rate_limits:
# Conditional descriptors - only ONE will fire per request
- actions: [{header_value_match: {descriptor_value: "premium", headers: [...]}}]
- actions: [{header_value_match: {descriptor_value: "standard", headers: [...]}}]
# Unconditional descriptor - ALWAYS fires
- actions: [{generic_key: {descriptor_value: "api_endpoint"}}]
```

This creates **two independent constraints**: user tier (conditional) AND endpoint type (always).

**Multiple Requests per Route (R > 1):**
- **Multi-Dimensional Limiting**: User quotas AND tenant quotas AND operation quotas
- **Independent Checks**: Each request creates separate enforcement decisions
- **Orthogonal Concerns**: Authentication limits vs usage limits vs abuse protection
- **Granular Control**: Fine-grained descriptor targeting

```yaml
rate_limits:  # Multiple requests from same route
- actions: [{request_headers: {header_name: "x-user-id", descriptor_key: "user_id"}}]      # Request 1
- actions: [{request_headers: {header_name: "x-tenant-id", descriptor_key: "tenant_id"}}]  # Request 2  
- actions: [{generic_key: {descriptor_value: "file_upload"}}]                              # Request 3
```

**Multiple Conditions per Pattern (C > 1):**
- **Compound Keys**: Multi-dimensional rate limiting (user + operation)
- **Hierarchical Matching**: Namespace + resource + action combinations
- **Context Specificity**: Match multiple request attributes simultaneously
- **Precise Targeting**: Exact combination matching for specific scenarios

```yaml
descriptors:
- entries:  # Multiple conditions in same pattern
  - key: tenant_id     # Condition 1: Must match tenant
  - key: operation     # Condition 2: Must match operation  
  - key: resource_type # Condition 3: Must match resource
  token_bucket: {...}
```

**Algorithmic Complexity**: `F × P × R × C` operations where:

- `F` = Number of rate limit filters per stage
- `P` = Rate limit patterns per filter  
- `R` = Rate limit requests per route (from requestor side)
- `C` = Key conditions per pattern (entries in pattern)

#### **Concrete Example**

**Requestor Side (Route) generates:**

```yaml
# 3 Rate Limit Requests created by route extractors
rate_limits:
- actions: [{user_id extraction}]      # Request 1: {user_id: "alice"}
- actions: [{tenant_id extraction}]    # Request 2: {tenant_id: "company_a"}  
- actions: [{operation extraction}]    # Request 3: {operation: "upload"}
```

**Implementor Side (Filter) evaluates:**

```yaml
# 2 Rate Limit Patterns in local filter
descriptors:
- entries: [{key: user_id}, {key: operation}]    # Pattern A: 2 conditions
- entries: [{key: tenant_id}]                    # Pattern B: 1 condition
```

**Cross-Product Evaluation Matrix:**

```
         │ Request 1    │ Request 2      │ Request 3
         │ {user_id}    │ {tenant_id}    │ {operation}
─────────┼──────────────┼────────────────┼─────────────
Pattern A│ Check user_id│ Check user_id  │ Check user_id
(2 cond) │ Check operation│ Check operation│ Check operation
─────────┼──────────────┼────────────────┼─────────────
Pattern B│ Check tenant_id│ Check tenant_id│ Check tenant_id
(1 cond) │              │                │
```

**Total Operations**: 1 filter × 2 patterns × 3 requests × avg(1.5 conditions) = **9 condition checks**

#### **Performance Implications**

**Typical Real-World Complexity:**

- **F (filters)**: 1-3 per stage (most deployments)
- **P (patterns)**: 1-10 per filter (simple configurations)  
- **R (requests)**: 1-5 per route (focused rate limiting)
- **C (conditions)**: 1-3 per pattern (simple key matching)

**Realistic total**: ~30-450 operations per HTTP request

**Performance Impact Factors:**

- **Hash-based key lookups**: O(1) per condition check
- **Early termination**: Failed matches stop immediately  
- **Pattern compilation**: Pre-optimized matching functions
- **Memory locality**: Efficient data structures for cache performance

### 1.4 Information Flow

Rate limiting uses **unidirectional, stage-based broadcasting**:

```
Route Extractors → Rate Limit Requests → HTTP Connection Manager → Filters
```

- **Routes are data producers**: Execute extractors and create rate limit requests
- **Connection Manager is distributor**: Broadcasts requests to appropriate filters
- **Filters are consumers**: Process requests independently using their patterns
- **No inter-filter communication**: Each filter processes the same data independently

### 1.5 Key Architectural Insights

1. **Pure Data Flow**: Information flows one direction only - no feedback loops
2. **Filter Independence**: Filters operate independently on the same input data
3. **Asymmetric Complexity**: Requestors focus on extraction, implementors handle matching
4. **Conjunctive Logic**: All rate limiting operates with AND logic throughout
5. **No Transaction Semantics**: Rate limiting optimized for performance over consistency

### 1.6 Improved Terminology

The standard Envoy terminology can be confusing. Here are clearer alternatives:

| Current Term | Better Term | Description |
|--------------|-------------|-------------|
| "Descriptor Instance" | **"Rate Limit Request"** | Runtime key-value data created by route actions from actual request context |
| "Descriptor Definition" | **"Rate Limit Pattern"** | Configuration templates in local filters that specify which requests to limit and how |
| "Actions" | **"Request Extractors"** | Active components that extract data from HTTP requests to build rate limit requests |

**Complete Flow with Better Terminology:**

```
HTTP Request → Request Extractors → Rate Limit Requests → Rate Limiting Filters

Example:
GET /api HTTP/1.1
x-user-id: alice
x-api-key: key123
         ↓
Request Extractors:
- request_headers extractor → {user_id: "alice"}
- request_headers extractor → {api_key: "key123"}  
- generic_key extractor → {service: "api_service"}
         ↓
Rate Limit Requests: [{user_id: "alice"}, {api_key: "key123"}, {service: "api_service"}]
         ↓
Local Filter (Rate Limit Patterns) + Global Filter (Forward All)
```

## 2. Multi-Layer Architecture

Envoy applies rate limiting in a **natural hierarchical order** based on the network stack processing flow:

#### 1. **Listener-Level Rate Limiting** (Earliest - Socket Accept)
Applied when a new connection is **first accepted** by the listener, before any protocol processing:

- **Filter Type**: Listener Filter (`envoy.filters.listener.local_ratelimit`)
- **Scope**: Controls incoming socket acceptance rate
- **Granularity**: Per-listener (applies to all connections on that listener)
- **Actions**: No complex actions - simple token bucket per socket
- **Configuration Location**: Listener filter chain

```yaml
listeners:
- filter_chains:
  - filters: [...]
  listener_filters:  # APPLIED FIRST - at socket accept
  - name: envoy.filters.listener.local_ratelimit
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.filters.listener.local_ratelimit.v3.LocalRateLimit
      stat_prefix: listener_rate_limiter
      token_bucket:
        max_tokens: 1000      # 1000 connections
        tokens_per_fill: 100  # Allow 100 new connections
        fill_interval: 60s    # Per minute
```

#### 2. **Network-Level Rate Limiting** (Second - Connection Processing)
Applied after connection is accepted but before HTTP processing:

- **Filter Type**: Network Filter (`envoy.filters.network.local_ratelimit`)
- **Scope**: Controls connection-level processing rate
- **Granularity**: Per-connection (each connection consumes one token)
- **Actions**: No complex actions - simple token bucket per connection
- **Configuration Location**: Network filter chain

```yaml
listeners:
- filter_chains:
  - filters:  # APPLIED SECOND - per connection
  - name: envoy.filters.network.local_ratelimit
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.filters.network.local_ratelimit.v3.LocalRateLimit
      stat_prefix: network_rate_limiter  
      token_bucket:
        max_tokens: 500       # 500 active connections
        tokens_per_fill: 50   # Allow 50 connections
        fill_interval: 60s    # Per minute
```

#### 3. **HTTP-Level Rate Limiting** (Third - Request Processing)
Applied to individual HTTP requests after connection and protocol processing:

- **Filter Type**: HTTP Filter (`envoy.filters.http.local_ratelimit`, `envoy.filters.http.ratelimit`)
- **Scope**: Controls HTTP request processing rate
- **Granularity**: Per-request with complex descriptor-based routing
- **Actions**: Full action system with route-based descriptor generation
- **Configuration Location**: HTTP filter chain + Route configuration

### 1.2 Local Rate Limiting
Local rate limiting is performed entirely within each Envoy instance using token bucket algorithms:

- **Listener Local Rate Limit Filter** - Applied at socket acceptance level
- **Network Local Rate Limit Filter** - Applied at connection level  
- **HTTP Local Rate Limit Filter** - Applied at HTTP request level

### 1.3 Global Rate Limiting
Global rate limiting uses a centralized rate limit service for coordinated decisions across multiple Envoy instances:

- **HTTP Rate Limit Filter** - Communicates with external rate limit service via gRPC
- **Network Rate Limit Filter** - Network-level global rate limiting
- **Thrift Rate Limit Filter** - Protocol-specific rate limiting

### 1.4 Rate Limit Quota Service
A newer bidirectional streaming approach for quota-based rate limiting:

- **Rate Limit Quota Filter** - Uses streaming quotas for dynamic rate limiting

### 1.5 Key Differences Across Layers

| Layer | When Applied | What's Limited | Configuration Complexity | Actions Support |
|-------|-------------|----------------|-------------------------|-----------------|
| **Listener** | Socket accept | New connections | Simple token bucket | No - just connection count |
| **Network** | Connection processing | Active connections | Simple token bucket | No - just connection count |
| **HTTP** | Request processing | HTTP requests | Complex descriptors + actions | Yes - full action system |

### 1.6 Complete Multi-Layer Example

```yaml
static_resources:
  listeners:
  - address: { socket_address: { address: 0.0.0.0, port_value: 8080 } }
    
    # LAYER 1: Listener-level rate limiting (socket acceptance)
    listener_filters:
    - name: envoy.filters.listener.local_ratelimit
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.filters.listener.local_ratelimit.v3.LocalRateLimit
        stat_prefix: socket_limiter
        token_bucket: { max_tokens: 1000, tokens_per_fill: 100, fill_interval: 60s }
    
    filter_chains:
    - filters:
      # LAYER 2: Network-level rate limiting (connection processing)  
      - name: envoy.filters.network.local_ratelimit
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.local_ratelimit.v3.LocalRateLimit
          stat_prefix: connection_limiter
          token_bucket: { max_tokens: 500, tokens_per_fill: 50, fill_interval: 60s }
      
      # LAYER 3: HTTP-level rate limiting (request processing)
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          http_filters:
          - name: envoy.filters.http.local_ratelimit
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
              stat_prefix: request_limiter
              token_bucket: { max_tokens: 100, tokens_per_fill: 50, fill_interval: 60s }
              # Complex descriptor-based rate limiting available here
              descriptors:
              - entries:
                - key: tenant_id
                token_bucket: { max_tokens: 10, tokens_per_fill: 5, fill_interval: 60s }
          
          route_config:
            virtual_hosts:
            - name: api
              domains: ["*"]  
              routes:
              - match: { prefix: "/" }
                route: { cluster: backend }
                # ACTIONS only work at HTTP layer
                rate_limits:
                - actions:
                  - request_headers:
                      header_name: "x-tenant-id"
                      descriptor_key: "tenant_id"
```

### 1.7 Processing Flow Summary

For an incoming request, the **natural processing order** is:

1. **Listener Filter**: "Can I accept this socket?" → Token bucket check
2. **Network Filter**: "Can I process this connection?" → Token bucket check  
3. **HTTP Filter**: "Can I process this request?" → Descriptor-based check with actions

Each layer provides a different level of protection:
- **Listener**: Protects against connection storms
- **Network**: Protects against connection abuse
- **HTTP**: Protects against request abuse with fine-grained control

**Key Insight**: Lower layers use simple token buckets, while HTTP layer supports the full descriptor/action system for sophisticated rate limiting strategies.

## 1.8 Rate Limiting Conceptual Overview

### 1.8.1 Requestor/Implementor Pattern

Envoy's rate limiting follows a clear **Requestor/Implementor** architecture:

**Requestor Side (Route Configuration):**
Rate limit requests are built during route processing through a configurable list of **request extractors** that form key-value descriptors (e.g., by extracting request headers, static metadata, source/dest, etc.). All rate limiting filters receive and process the same set of rate limit requests.

**Implementor Side (Filter Configuration):**
- **Local limiters**: Configured with a list of **rate limit patterns**, each containing matching conditions (key specifications with optional exact values) and token bucket parameters. Incoming rate limit requests are matched against these patterns - all matching patterns are evaluated, and all must pass.
- **Global limiters**: Forward all rate limit requests to external services which handle the limiting logic.

Both types configure instrumentation (stats prefix) and define failure behavior (local: LRU overflow, global: service timeout/failure modes).

### 1.8.2 Nesting Structure

The rate limiting system has **asymmetric nesting depth** reflecting different responsibilities:

#### **Requestor Side - 2 Levels (Data Collection)**
```
Route
└── Rate Limit Requests (multiple, all applied)
    └── Request Extractors (multiple per request, combined into single descriptor)
```

**Logic**: 
- Between Requests: AND (all requests must pass all filters)
- Within Request: COMBINE (all extractors contribute to single descriptor)

#### **Implementor Side - 3 Levels (Pattern Matching + Policy)**
```
Filter Chain
└── Rate Limiting Filters (multiple, all must pass)
    └── Rate Limit Patterns (multiple per filter, all must pass)
        └── Key Conditions (multiple per pattern, all must match)
```

**Logic**:
- Between Filters: AND (all filters must approve)
- Between Patterns: AND (all matching patterns must pass)  
- Within Pattern: AND (all conditions must match for pattern to apply)

### 1.8.3 Information Flow

Rate limiting uses **unidirectional, stage-based broadcasting**:

```
Route Extractors → Rate Limit Requests → HTTP Connection Manager → Filters
```

- **Routes are data producers**: Execute extractors and create rate limit requests
- **Connection Manager is distributor**: Broadcasts requests to appropriate filters
- **Filters are consumers**: Process requests independently using their patterns
- **No inter-filter communication**: Each filter processes the same data independently

### 1.8.4 Key Architectural Insights

1. **Pure Data Flow**: Information flows one direction only - no feedback loops
2. **Filter Independence**: Filters operate independently on the same input data
3. **Asymmetric Complexity**: Requestors focus on extraction, implementors handle matching
4. **Conjunctive Logic**: All rate limiting operates with AND logic throughout
5. **No Transaction Semantics**: Rate limiting optimized for performance over consistency

### 1.8.5 Cross-Product Algorithmic Complexity

The asymmetric nesting structure creates a **cross-product matching problem** where Requestor-generated data must be evaluated against Implementor pattern configurations:

#### **The Cross-Product Challenge**

```
For each Rate Limit Filter:
  For each Rate Limit Pattern in filter:
    For each Rate Limit Request from route:
      Check if ALL key conditions in pattern match the request
```

**Algorithmic Complexity**: `F × P × R × C` operations where:
- `F` = Number of rate limit filters per stage
- `P` = Rate limit patterns per filter  
- `R` = Rate limit requests per route (from requestor side)
- `C` = Key conditions per pattern (entries in pattern)

#### **Concrete Example**

**Requestor Side (Route) generates:**
```yaml
# 3 Rate Limit Requests created by route extractors
rate_limits:
- actions: [{user_id extraction}]      # Request 1: {user_id: "alice"}
- actions: [{tenant_id extraction}]    # Request 2: {tenant_id: "company_a"}  
- actions: [{operation extraction}]    # Request 3: {operation: "upload"}
```

**Implementor Side (Filter) evaluates:**
```yaml
# 2 Rate Limit Patterns in local filter
descriptors:
- entries: [{key: user_id}, {key: operation}]    # Pattern A: 2 conditions
- entries: [{key: tenant_id}]                    # Pattern B: 1 condition
```

**Cross-Product Evaluation Matrix:**
```
         │ Request 1    │ Request 2      │ Request 3
         │ {user_id}    │ {tenant_id}    │ {operation}
─────────┼──────────────┼────────────────┼─────────────
Pattern A│ Check user_id│ Check user_id  │ Check user_id
(2 cond) │ Check operation│ Check operation│ Check operation
─────────┼──────────────┼────────────────┼─────────────
Pattern B│ Check tenant_id│ Check tenant_id│ Check tenant_id
(1 cond) │              │                │
```

**Total Operations**: 1 filter × 2 patterns × 3 requests × avg(1.5 conditions) = **9 condition checks**

## 3. Design Trade-offs for Minimizing Cross-Product

**1. Combine Related Checks (Reduce R)**

```yaml
# PROBLEMATIC: Many separate requests
rate_limits:
- actions: [{user_id}]     # Request 1
- actions: [{tenant_id}]   # Request 2  
- actions: [{operation}]   # Request 3

# OPTIMIZED: Single combined request
rate_limits:
- actions: [{user_id}, {tenant_id}, {operation}]  # Single request
```

**2. Simplify Patterns (Reduce C)**

```yaml
# COMPLEX: Multi-condition patterns
descriptors:
- entries: [{key: user_id}, {key: tenant_id}, {key: operation}, {key: region}]

# SIMPLER: Focused patterns  
descriptors:
- entries: [{key: user_id}]        # Single condition
- entries: [{key: tenant_id}]      # Single condition
```

**3. Use Stages for Complexity Management (Reduce F×P)**

```yaml
# Stage 0: Simple local checks (few patterns)
- stage: 0  
  filters: [fast_local_filter with 2 simple patterns]

# Stage 1: Complex logic only if stage 0 passes  
- stage: 1
  filters: [complex_global_filter with many patterns]
```

**Key Insight: Manageable Complexity**

While the theoretical cross-product seems concerning, Envoy's **hash-based matching**, **early termination**, and **typical small values** for F, P, R, C keep the complexity manageable. The key is **thoughtful configuration design** that balances granularity with performance by:

1. **Minimizing unnecessary rate limit requests** (reduce R)
2. **Designing focused patterns** (reduce C) 
3. **Using stages strategically** (distribute F×P across stages)
4. **Leveraging Envoy's built-in optimizations** (hash lookups, early exits)

## 2. Rate Limit Descriptors

Rate limit descriptors are the core mechanism for categorizing and identifying requests. They consist of key-value pairs that describe request characteristics.

### 2.1 Descriptor Producers
Envoy supports various descriptor producers for generating rate limit keys:

1. **Source Cluster** - Rate limit based on originating cluster
2. **Destination Cluster** - Rate limit based on target cluster
3. **Remote Address** - Rate limit based on client IP
4. **Generic Key** - Static key-value pairs
5. **Request Headers** - Extract values from HTTP headers
6. **Query Parameters** - Extract values from URL parameters
7. **Dynamic Metadata** - Use request metadata for descriptors
8. **Header Value Match** - Conditional descriptors based on header matching
9. **Masked Remote Address** - CIDR-based IP rate limiting

### 2.2 Configuration Example

```yaml
rate_limits:
- actions:
  - source_cluster: {}
- actions:
  - destination_cluster: {}
  - request_headers:
      header_name: "x-user-id"
      descriptor_key: "user_id"
- actions:
  - generic_key:
      descriptor_value: "premium_users"
  - remote_address: {}
```

## 3. Dynamic Descriptors and Wildcard Matching

### 3.1 Wildcard Descriptor Functionality
Dynamic descriptors with wildcard matching allow flexible rate limiting without pre-defining every possible descriptor combination.

**Key Features:**
- Empty descriptor values act as wildcards matching any request value
- Dynamic token bucket creation for new descriptor patterns
- LRU cache management for memory control

### 3.2 Configuration Example

```yaml
descriptors:
- entries:
  - key: user_id        # Wildcard - matches any user_id value
  - key: operation
    value: "upload"     # Exact match for upload operations
  token_bucket:
    max_tokens: 10
    tokens_per_fill: 5
    fill_interval: 60s
```

### 3.3 Memory Management
- **LRU Cache Size**: Configurable via `max_dynamic_descriptors` (default: 20)
- **Eviction Strategy**: Always uses LRU eviction - no failure options available
- **Monitoring**: Limited to trace-level logging for evictions

## 4. Processing Stages: Coordination Between Routes and Filters

### 4.1 What Are Stages?

**Stages** are simple numeric identifiers (0-10) that serve as **coordination keys** between route-level rate limit requests and filter-level rate limit implementations. Think of stages as **logical channels** that connect requestors with implementors.

**Key Concept:** A stage is a **matching mechanism** - routes specify which stages they need, and filters specify which stages they handle.

### 4.2 Stage Appears in Two Places

#### Route Level (Requestor)
Routes use stages to **request** specific types of rate limiting:

```yaml
routes:
- match: { prefix: "/api" }
  route:
    cluster: backend
    rate_limits:
    - stage: 1        # "I need stage 1 rate limiting"
      actions:
      - request_headers:
          header_name: "x-user-id"
          descriptor_key: "user_id"
    - stage: 2        # "I also need stage 2 rate limiting"
      actions:
      - request_headers:
          header_name: "x-tenant-id"
          descriptor_key: "tenant_id"
```

#### Filter Level (Implementor)
Filters use stages to **declare** which type of rate limiting they provide:

```yaml
http_filters:
- name: envoy.filters.http.ratelimit
  typed_config:
    domain: "user_limits"
    stage: 1          # "I handle stage 1 requests"
    rate_limit_service: {...}

- name: envoy.filters.http.local_ratelimit
  typed_config:
    stage: 2          # "I handle stage 2 requests"
    descriptors: [...]
```

### 4.3 Stage Matching Logic

**The binding rule is simple:** Filters only process rate limits that match their configured stage.

```
Route rate_limits with stage: 1  →  Processed by filters with stage: 1
Route rate_limits with stage: 2  →  Processed by filters with stage: 2
No stage specified               →  Defaults to stage: 0
```

### 4.4 Processing Order vs Stage Numbers

**Important:** Stages do **NOT** control execution order. Filters process in **filter chain order**, regardless of stage numbers.

```yaml
http_filters:
# This filter runs FIRST (filter chain order)
- name: envoy.filters.http.ratelimit
  typed_config:
    stage: 2          # High stage number, but runs FIRST

# This filter runs SECOND (filter chain order)  
- name: envoy.filters.http.local_ratelimit
  typed_config:
    stage: 1          # Lower stage number, but runs SECOND
```

**Processing Flow:**
1. **Filter with stage 2** runs first, processes only stage 2 rate limits
2. **Filter with stage 1** runs second, processes only stage 1 rate limits

### 4.5 Complete Stage Example

```yaml
static_resources:
  listeners:
  - filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          http_filters:
          # Filter A: User-level rate limiting (stage 1)
          - name: envoy.filters.http.ratelimit
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimit
              domain: "user_limits"
              stage: 1                    # Handles stage 1
              rate_limit_service:
                grpc_service:
                  envoy_grpc:
                    cluster_name: rate_limit_service

          # Filter B: Tenant-level rate limiting (stage 2)
          - name: envoy.filters.http.local_ratelimit
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
              stage: 2                    # Handles stage 2
              descriptors:
              - entries:
                - key: tenant_id
                token_bucket:
                  max_tokens: 100
                  tokens_per_fill: 50
                  fill_interval: 60s

          route_config:
            virtual_hosts:
            - name: api_service
              domains: ["*"]
              routes:
              - match: { prefix: "/api" }
                route:
                  cluster: backend
                  rate_limits:
                  # Request stage 1 rate limiting
                  - stage: 1              # Processed by Filter A
                    actions:
                    - request_headers:
                        header_name: "x-user-id"
                        descriptor_key: "user_id"
                  # Request stage 2 rate limiting  
                  - stage: 2              # Processed by Filter B
                    actions:
                    - request_headers:
                        header_name: "x-tenant-id"
                        descriptor_key: "tenant_id"
```

### 4.6 Request Processing Flow

For a request to `/api` with headers `x-user-id: alice` and `x-tenant-id: company_a`:

1. **Route matching** determines rate_limits apply:
   - Stage 1 actions create descriptor: `{user_id: "alice"}`
   - Stage 2 actions create descriptor: `{tenant_id: "company_a"}`

2. **Filter A processes** (stage 1):
   - Sees stage 1 descriptor → **PROCESSES** → Sends to user_limits domain
   - Sees stage 2 descriptor → **IGNORES**

3. **Filter B processes** (stage 2):
   - Sees stage 1 descriptor → **IGNORES**
   - Sees stage 2 descriptor → **PROCESSES** → Checks local token bucket

### 4.7 Stage Characteristics

#### Stage Numbers Are Arbitrary
The actual numbers don't matter - only that they match:

```yaml
# These work identically:
route: stage: 99    ←→    filter: stage: 99
route: stage: 0     ←→    filter: stage: 0  
```

#### Default Stage is 0
If no stage is specified, it defaults to 0:

```yaml
# These are equivalent:
rate_limits:
- actions: [...]

rate_limits:
- stage: 0
  actions: [...]
```

#### Multiple Filters Can Share Stages
Multiple filters can handle the same stage:

```yaml
# Both filters process stage 1 descriptors
- name: envoy.filters.http.ratelimit
  typed_config:
    stage: 1
- name: envoy.filters.http.local_ratelimit
  typed_config:
    stage: 1
```

#### Range is 0-10
Stage numbers must be between 0 and 10 (inclusive).

### 4.8 Common Stage Patterns

#### Processing Order Pattern
Organize stages by processing priority:

```yaml
- stage: 0    # Global/cluster limits (processed first in filter chain)
- stage: 1    # Service-specific limits  
- stage: 2    # User/tenant-specific limits
```

#### Responsibility Pattern
Organize stages by limiting responsibility:

```yaml
- stage: 0    # Infrastructure limits (connections, bandwidth)
- stage: 1    # Application limits (API calls, operations)
- stage: 2    # Business limits (user quotas, tenant limits)
```

#### Environment Pattern
Organize stages by deployment environment:

```yaml
- stage: 0    # Development (logging only)
- stage: 1    # Staging (warnings)
- stage: 2    # Production (enforcement)
```

### 4.9 Why Use Stages?

**Without stages**, you'd need separate routes for different rate limiting strategies:

```yaml
# Without stages - complex route duplication
routes:
- match: { prefix: "/api/users" }      # User limits only
  rate_limits: [user actions]
- match: { prefix: "/api/tenants" }    # Tenant limits only  
  rate_limits: [tenant actions]
```

**With stages**, you can apply multiple rate limiting strategies to the same route:

```yaml
# With stages - one route, multiple strategies
routes:
- match: { prefix: "/api" }
  rate_limits:
  - stage: 0: [global actions]
  - stage: 1: [user actions]
  - stage: 2: [tenant actions]
```

### 4.10 Best Practices

1. **Align filter order with intended processing order** when stage sequence matters
2. **Use meaningful stage numbers** that reflect your rate limiting architecture
3. **Document your stage strategy** so teams understand the pattern
4. **Start with stage 0** for basic limits, use higher stages for specialized limits
5. **Test stage interactions** to ensure multiple stages work together as expected

### 4.11 Advanced Stage Strategy: When and Why to Use Different Stages

Understanding when to use the same stage versus different stages is crucial for designing effective rate limiting architectures.

#### 4.11.1 Multiple Descriptors on Same Stage vs Different Stages

**Same Stage - Multiple Independent Checks:**
```yaml
rate_limits:
- stage: 1  # All processed by same filter(s)
  actions: [{user_id extraction}]
- stage: 1  # Independent check
  actions: [{tenant_id extraction}]  
- stage: 1  # Another independent check
  actions: [{api_key extraction}]
```

**Different Stages - Different Processing:**
```yaml
rate_limits:
- stage: 0  # Processed by stage 0 filters
  actions: [{basic_check}]
- stage: 1  # Processed by stage 1 filters
  actions: [{detailed_check}]
- stage: 2  # Processed by stage 2 filters
  actions: [{business_check}]
```

#### 4.11.2 How Multiple Descriptors Work in Rate Limit Services

When multiple descriptors are sent to a global rate limit service, the service processes them **independently** without additional identification:

```yaml
# Rate limit service receives:
RateLimitRequest {
  domain: "api_limits"
  descriptors: [
    { entries: [{key: "user_id", value: "alice"}] },
    { entries: [{key: "tenant_id", value: "company_a"}] },
    { entries: [{key: "api_key", value: "xyz123"}] }
  ]
}
```

**Service Processing Logic:**
1. **Descriptor Self-Identification**: Each descriptor identifies itself through its key patterns
2. **Independent Rule Matching**: Service matches each descriptor to corresponding rules
3. **Independent Evaluation**: Each descriptor is evaluated against its specific rate limits
4. **Combined Result**: All descriptor checks must pass for overall approval

**Rate Limit Service Configuration Example:**
```yaml
api_limits:  # Domain
  # Rule 1: Matches {user_id: "alice"}
  - key: user_id
    value: alice
    descriptors:
    - rate_limit: { unit: minute, requests_per_unit: 100 }
  
  # Rule 2: Matches {tenant_id: "company_a"}  
  - key: tenant_id
    value: company_a
    descriptors:
    - rate_limit: { unit: minute, requests_per_unit: 1000 }
  
  # Rule 3: Matches {api_key: "xyz123"}
  - key: api_key
    descriptors:
    - rate_limit: { unit: minute, requests_per_unit: 500 }
```

#### 4.11.3 When to Use Same Stage (Multiple Descriptors)

**Use same stage when you want:**

##### **Independent Multi-Dimensional Checks:**
```yaml
# All processed by same filter - multiple independent limits
rate_limits:
- stage: 1  # User quota check
  actions:
  - request_headers: {header_name: "x-user-id", descriptor_key: "user_id"}
- stage: 1  # Tenant quota check  
  actions:
  - request_headers: {header_name: "x-tenant-id", descriptor_key: "tenant_id"}
- stage: 1  # Operation quota check
  actions:
  - generic_key: {descriptor_value: "upload"}
```

**Result**: Request must pass ALL three independent quota checks.

##### **Hierarchical Granularity:**
```yaml
rate_limits:
- stage: 1  # Broad limit: all uploads
  actions:
  - generic_key: {descriptor_value: "upload"}
- stage: 1  # Specific limit: user uploads
  actions:
  - request_headers: {header_name: "x-user-id", descriptor_key: "user_id"}
  - generic_key: {descriptor_value: "upload"}
```

**Result**: Both general upload limits AND user-specific upload limits apply.

##### **Conditional Logic:**
```yaml
rate_limits:
- stage: 1  # Premium user limits
  actions:
  - header_value_match:
      descriptor_value: "premium"
      headers: [{name: "x-user-tier", exact_match: "premium"}]
- stage: 1  # Standard user limits
  actions:
  - header_value_match:
      descriptor_value: "standard"  
      headers: [{name: "x-user-tier", exact_match: "standard"}]
```

**Result**: Different limits based on user tier, but processed together.

#### 4.11.4 When to Use Different Stages

**Use different stages for:**

##### **Filter Specialization and Performance Optimization:**
```yaml
http_filters:
# Stage 0: Fast local checks (microseconds)
- name: envoy.filters.http.local_ratelimit
  typed_config:
    stage: 0
    descriptors: [simple token buckets]

# Stage 1: Network-based checks (milliseconds)
- name: envoy.filters.http.ratelimit
  typed_config:
    stage: 1
    domain: "global_limits"
    rate_limit_service: {cluster: "fast_ratelimit_service"}

# Stage 2: Complex business logic (seconds)  
- name: envoy.filters.http.ratelimit
  typed_config:
    stage: 2
    domain: "business_rules"
    rate_limit_service: {cluster: "business_ratelimit_service"}

route_config:
  rate_limits:
  - stage: 0  # Fast local check first
    actions: [{basic tenant check}]
  - stage: 1  # Network call only if stage 0 passes
    actions: [{user quota check}]
  - stage: 2  # Expensive logic only if stages 0-1 pass
    actions: [{billing/compliance check}]
```

**Benefits:**
- **Fail Fast**: Reject requests early with cheap local checks
- **Performance**: Avoid expensive operations when possible
- **Scalability**: Reduce load on expensive services

##### **Different Rate Limit Services:**
```yaml
# Stage 1: Infrastructure rate limiting
- name: envoy.filters.http.ratelimit
  typed_config:
    stage: 1
    domain: "infrastructure"
    rate_limit_service: {cluster: "infrastructure_ratelimit"}

# Stage 2: Application rate limiting
- name: envoy.filters.http.ratelimit
  typed_config:
    stage: 2  
    domain: "application"
    rate_limit_service: {cluster: "application_ratelimit"}

# Stage 3: Business rules rate limiting
- name: envoy.filters.http.ratelimit
  typed_config:
    stage: 3
    domain: "business"
    rate_limit_service: {cluster: "business_ratelimit"}
```

##### **Different Failure Modes:**
```yaml
# Stage 0: Fail open (non-critical)
- name: envoy.filters.http.ratelimit
  typed_config:
    stage: 0
    failure_mode_deny: false  # Continue on service failure
    domain: "monitoring"

# Stage 1: Fail closed (critical)  
- name: envoy.filters.http.ratelimit
  typed_config:
    stage: 1
    failure_mode_deny: true   # Block on service failure
    domain: "security"
```

##### **Operational Separation:**
```yaml
# Different teams own different stages:
# Stage 0: Platform team (infrastructure limits)
# Stage 1: Application team (feature limits)  
# Stage 2: Business team (billing limits)
```

#### 4.11.5 Stage Design Patterns

##### **Progressive Filtering Pattern:**
```yaml
# Stage 0: Basic validation (99% of bad requests filtered)
# Stage 1: Authentication checks (95% of remaining filtered)
# Stage 2: Authorization checks (90% of remaining filtered)
# Stage 3: Business logic checks (final validation)
```

##### **Layered Defense Pattern:**
```yaml
# Stage 0: DDoS protection (local)
# Stage 1: API quotas (global coordination)
# Stage 2: Business rules (complex logic)
```

##### **Performance Tier Pattern:**
```yaml
# Stage 0: Millisecond response (local cache)
# Stage 1: Sub-second response (fast external service)
# Stage 2: Multi-second response (complex business logic)
```

#### 4.11.6 Decision Matrix: Same Stage vs Different Stages

| **Scenario** | **Use Same Stage** | **Use Different Stages** |
|--------------|-------------------|-------------------------|
| **Independent checks by same service** | ✅ Yes | ❌ No |
| **Different performance requirements** | ❌ No | ✅ Yes |
| **Different failure mode requirements** | ❌ No | ✅ Yes |
| **Different teams/services own logic** | ❌ No | ✅ Yes |
| **Want to fail fast** | ❌ No | ✅ Yes |
| **All checks equally important** | ✅ Yes | ❌ No |
| **Complex hierarchical rules** | ✅ Yes | ❌ No |

#### 4.11.7 Key Architectural Insights

##### **Descriptor Self-Identification:**
- Rate limit services don't need external identifiers for descriptors
- Each descriptor's key pattern determines which rules apply
- Multiple descriptors = multiple independent rate limiting dimensions

##### **Stage-Based Routing:**
- Stages route rate limiting requests to appropriate filters
- Same stage = processed together by same filters
- Different stages = processed separately by different filters

##### **Performance and Reliability:**
- Use stages to implement performance tiers
- Earlier stages should be faster and more reliable
- Later stages can be more complex and potentially slower

### 4.12 Logical Operators and Transaction Semantics

#### 4.12.1 Rate Limiting Logic: Conjunction Only (AND)

**Envoy rate limiting operates exclusively with conjunction (AND) logic** - there are **no disjunction (OR) operators** available.

##### **All Limits Must Pass (AND Logic):**

```yaml
rate_limits:
- stage: 1  # Descriptor 1: User limit
  actions: [{user_id extraction}]
- stage: 1  # Descriptor 2: Tenant limit  
  actions: [{tenant_id extraction}]
- stage: 1  # Descriptor 3: Operation limit
  actions: [{operation extraction}]
```

**Result**: Request must pass **ALL THREE** rate limit checks to proceed.

##### **No OR Logic Available:**

Envoy does **not** provide mechanisms for:
- "Pass if ANY of these limits allow"
- "Allow if user limit OR tenant limit passes"
- "Permit if basic limit OR premium limit succeeds"

##### **Architectural Reasoning:**

Rate limiting is designed as a **protection mechanism** where:
- **Multiple independent protections** should all apply
- **Each limit represents a different resource constraint**
- **Bypassing any protection** would defeat the purpose
- **AND logic ensures comprehensive protection**

#### 4.12.2 Workarounds for OR-like Behavior

While native OR operators don't exist, you can achieve OR-like behavior through configuration patterns:

##### **Conditional Actions (Pseudo-OR):**
```yaml
rate_limits:
- stage: 1
  actions:
  - header_value_match:
      descriptor_value: "premium_user"
      headers: [{name: "x-user-tier", exact_match: "premium"}]
- stage: 1  
  actions:
  - header_value_match:
      descriptor_value: "standard_user"
      headers: [{name: "x-user-tier", exact_match: "standard"}]
```

**Result**: Only ONE of these descriptors will be generated per request (based on header value), providing mutually exclusive rate limiting.

##### **Route-Level Separation:**
```yaml
routes:
# Premium users - higher limits
- match: 
    prefix: "/api"
    headers: [{name: "x-user-tier", exact_match: "premium"}]
  rate_limits: [{premium actions}]

# Standard users - lower limits  
- match:
    prefix: "/api"
    headers: [{name: "x-user-tier", exact_match: "standard"}] 
  rate_limits: [{standard actions}]
```

**Result**: Different routes apply different rate limiting rules, achieving OR-like selection.

#### 4.12.3 Transaction Semantics: No Rollback Available

**Envoy rate limiting has NO transaction semantics or rollback mechanisms.**

##### **Global Rate Limiting - No Atomicity:**

When multiple descriptors are sent to a global rate limit service:

```yaml
rate_limits:
- stage: 1
  actions: [{user_id extraction}]    # Descriptor 1
- stage: 1  
  actions: [{tenant_id extraction}]  # Descriptor 2
- stage: 1
  actions: [{api_key extraction}]    # Descriptor 3
```

**What happens:**
1. **All descriptors sent together** in single RateLimitRequest
2. **Rate limit service evaluates each independently**
3. **Tokens consumed for ALL descriptors** that have available quota
4. **No rollback** if some descriptors pass and others fail

##### **Example Transaction Issue:**

```
Request with 3 descriptors:
1. user_id: "alice" → Has quota, consumes 1 token
2. tenant_id: "company_a" → Has quota, consumes 1 token  
3. api_key: "xyz123" → NO quota available, fails

Result: 
- Descriptors 1 & 2 consumed tokens (NOT rolled back)
- Overall request REJECTED due to descriptor 3 failure
- Tokens from descriptors 1 & 2 are LOST
```

##### **Rate Limit Service Response Format:**

```yaml
RateLimitResponse {
  overall_code: OVER_LIMIT  # Overall decision (AND of all descriptors)
  statuses: [
    { code: OK },         # Descriptor 1 passed (token consumed)
    { code: OK },         # Descriptor 2 passed (token consumed)
    { code: OVER_LIMIT }  # Descriptor 3 failed (no token available)
  ]
}
```

**Key Issue**: Tokens from passing descriptors are consumed even when overall request fails.

##### **Local Rate Limiting - No Atomicity:**

Similar behavior occurs with local rate limiting:

```yaml
descriptors:
- entries: [{key: user_id}]     # Token bucket 1
- entries: [{key: tenant_id}]   # Token bucket 2  
- entries: [{key: api_key}]     # Token bucket 3
```

**Processing:**
1. **Check token bucket 1** → Has tokens, consumes 1
2. **Check token bucket 2** → Has tokens, consumes 1
3. **Check token bucket 3** → No tokens available, fails
4. **Overall request rejected**, but tokens from buckets 1 & 2 already consumed

#### 4.12.4 Implications and Best Practices

##### **Design for Token Loss:**

```yaml
# PROBLEM: High-value tokens consumed even when request fails
rate_limits:
- stage: 1
  actions: [{expensive_user_quota}]    # Valuable tokens
- stage: 1
  actions: [{cheap_tenant_quota}]      # Less valuable tokens
```

**Better approach - Use stages for fail-fast:**

```yaml
# Stage 0: Cheap checks first (fail fast)
rate_limits:
- stage: 0
  actions: [{cheap_tenant_quota}]      # Check this first

# Stage 1: Expensive checks only if stage 0 passes  
rate_limits:
- stage: 1
  actions: [{expensive_user_quota}]    # Only if tenant quota passes
```

##### **Minimize Descriptor Count:**

```yaml
# PROBLEMATIC: Many independent descriptors
rate_limits:
- stage: 1
  actions: [{user_id}]
- stage: 1  
  actions: [{tenant_id}]
- stage: 1
  actions: [{api_key}]
- stage: 1
  actions: [{operation}]

# BETTER: Combined descriptors  
rate_limits:
- stage: 1
  actions:
  - {user_id}
  - {tenant_id} 
  - {api_key}
  - {operation}
```

**Result**: Single descriptor evaluation, atomic success/failure.

##### **Use Local Rate Limiting for Atomic Behavior:**

```yaml
# Local rate limiting with combined descriptors
descriptors:
- entries:
  - key: user_id
  - key: tenant_id  
  - key: api_key
  token_bucket: {...}
```

**Result**: Single token bucket check, no partial consumption.

#### 4.12.5 Key Architectural Insights

##### **Rate Limiting is Protection-Oriented:**
- **AND logic ensures comprehensive protection**
- **Multiple limits represent independent resource constraints**
- **All constraints must be satisfied for safe operation**

##### **No Transaction Support by Design:**
- **Rate limiting optimized for performance, not ACID properties**
- **Distributed nature makes transactions complex and expensive**
- **Token loss acceptable trade-off for scalability**

##### **Mitigation Strategies:**
- **Use stages for fail-fast patterns**
- **Combine related checks in single descriptors**
- **Design with token loss tolerance**
- **Use local rate limiting for atomic behavior when needed**

**Key Insight**: Envoy's rate limiting is designed for **high-performance protection** rather than **transactional consistency**. Understanding this trade-off is crucial for designing effective rate limiting strategies that account for potential token loss in multi-descriptor scenarios.

## 5. Per-Route Configuration and Hierarchical Overrides

### 5.1 Configuration Hierarchy
Rate limiting follows a hierarchical configuration model:

1. **Global filter configuration** - Base settings
2. **Virtual host configuration** - Host-specific settings
3. **Route-specific configuration** - Most granular overrides

### 5.2 Virtual Host Rate Limit Options
The `vh_rate_limits` field controls how virtual host and route configurations interact:

- **OVERRIDE**: Route config replaces virtual host config (if route config exists)
- **INCLUDE**: Both virtual host and route configs apply
- **IGNORE**: Only route config applies, virtual host ignored

### 5.3 Per-Route Override Example

```yaml
routes:
- match: { path: "/api/premium" }
  route: { cluster: "premium_cluster" }
  typed_per_filter_config:
    envoy.filters.http.ratelimit:
      "@type": type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimitPerRoute
      domain: "premium-api"
      vh_rate_limits: OVERRIDE
      rate_limits:
      - actions:
        - generic_key: { descriptor_value: "premium_endpoint" }
        - request_headers: { header_name: "x-user-id", descriptor_key: "user_id" }

- match: { path: "/api/standard" }
  route: { cluster: "standard_cluster" }
  typed_per_filter_config:
    envoy.filters.http.local_ratelimit:
      "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
      token_bucket:
        max_tokens: 100
        tokens_per_fill: 50
        fill_interval: 60s
      vh_rate_limits: INCLUDE  # Combine with virtual host limits
```

## 6. Resource Management Beyond Rate Limiting

### 6.1 Circuit Breakers
Circuit breakers provide upstream cluster-level resource protection:

```yaml
circuit_breakers:
  thresholds:
  - priority: DEFAULT
    max_connections: 1024
    max_pending_requests: 256
    max_requests: 1024
    max_connection_pools: 4
```

### 6.2 Connection Management
Various connection-level limits and controls:

```yaml
# Connection limit filter
typed_config:
  "@type": type.googleapis.com/envoy.extensions.filters.network.connection_limit.v3.ConnectionLimit
  max_connections: 1000

# Cluster connection limits
max_requests_per_connection: 100
max_connection_duration: 300s
```

### 6.3 Buffer Management and Flow Control
Sophisticated buffer management with watermark-based flow control:

```yaml
# Connection manager buffer limits
connection_buffer_limit_bytes: 32768
stream_idle_timeout: 300s
request_timeout: 60s
```

## 7. Overload Manager

### 7.1 Resource Monitoring
The overload manager provides centralized resource pressure monitoring:

```yaml
overload_manager:
  refresh_interval: 1s
  resource_monitors:
  - name: "envoy.resource_monitors.fixed_heap"
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.resource_monitors.fixed_heap.v3.FixedHeapConfig
      max_heap_size_bytes: 2147483648  # 2GB
  - name: "envoy.resource_monitors.cpu"
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.resource_monitors.cpu.v3.CpuConfig
```

### 7.2 Overload Actions
Actions triggered based on resource pressure:

```yaml
overload_manager:
  actions:
  - name: "envoy.overload_actions.stop_accepting_requests"
    triggers:
    - name: "envoy.resource_monitors.fixed_heap"
      threshold:
        value: 0.95
  - name: "envoy.overload_actions.disable_http_keepalive"
    triggers:
    - name: "envoy.resource_monitors.fixed_heap"
      threshold:
        value: 0.80
  - name: "envoy.overload_actions.reset_high_memory_stream"
    triggers:
    - name: "envoy.resource_monitors.fixed_heap"
      threshold:
        value: 0.85
```

### 7.3 Load Shedding
Probabilistic request rejection under resource pressure:

```yaml
overload_manager:
  loadshed_points:
  - name: "envoy.load_shed_points.http_connection_manager"
    triggers:
    - name: "envoy.resource_monitors.cpu"
      threshold:
        value: 0.75
```

## 8. Configuration Consistency Analysis

### 8.1 Rate Limiting vs Memory Management
**Limited Direct Consistency:** Rate limiting and memory management operate as largely separate systems with different design philosophies:

- **Rate limiting**: Granular, route-based, hierarchical configuration
- **Memory management**: Global/system-level with stream-based accounting

### 8.2 Per-Tenant/Per-Route Memory Accounting
**Current State:**
- Per-stream memory accounting available via Buffer Memory Accounts
- No explicit per-tenant or per-route memory limits
- Memory tracking is global with stream-level granularity
- No route-specific memory configuration equivalent to rate limiting

**Memory Classes:**
- Streams classified into 8 memory classes based on usage
- Automatic stream reset during memory pressure targeting largest consumers first

## 9. Monitoring and Observability

### 9.1 Rate Limiting Metrics
```yaml
# Available rate limiting stats
enabled: <counter>
enforced: <counter>  
rate_limited: <counter>
ok: <counter>
```

### 9.2 Overload Manager Metrics
```yaml
# Overload action metrics
overload.<action_name>.active: <gauge>
overload.<action_name>.scale_percent: <gauge>

# Resource monitor metrics  
overload.<resource_name>.pressure: <gauge>
overload.<resource_name>.failed_updates: <counter>
overload.<resource_name>.skipped_updates: <counter>
```

### 9.3 Dynamic Descriptor Monitoring Limitations
- No built-in metrics for dynamic descriptor usage
- Only trace-level logging available for LRU evictions
- No cache hit/miss ratio tracking
- No dynamic descriptor creation rate metrics

## 10. Operational Recommendations

### 10.1 Rate Limiting Best Practices
1. **Start with conservative limits** and monitor actual traffic patterns
2. **Use stages strategically** to organize different types of limits
3. **Leverage per-route overrides** for endpoints with special requirements
4. **Size dynamic descriptor LRU caches** based on expected cardinality
5. **Monitor trace logs** for dynamic descriptor evictions

### 10.2 Resource Management Best Practices
1. **Configure overload actions** for graceful degradation under pressure
2. **Set appropriate buffer limits** based on expected traffic patterns
3. **Use circuit breakers** to protect upstream services
4. **Monitor resource pressure metrics** to understand system behavior
5. **Test overload scenarios** to validate load shedding behavior

### 10.3 Memory Management Considerations
1. **No per-route memory limits available** - design applications accordingly
2. **Memory pressure triggers global actions** affecting all traffic
3. **Stream-level accounting** provides granular visibility but limited control
4. **Overload manager** provides the primary mechanism for memory-based load shedding

## 11. Future Considerations

### 11.1 Potential Enhancements
- Per-route memory limit configuration similar to rate limiting
- Enhanced monitoring for dynamic descriptor usage
- Tenant-based memory isolation and accounting
- More sophisticated load shedding algorithms
- Better integration between rate limiting and memory management systems

### 11.2 Architectural Trade-offs
Envoy's current design prioritizes:
- **Global stability** over fine-grained memory control
- **Availability** over strict resource limiting (LRU eviction vs failures)
- **Performance** over comprehensive per-request accounting
- **Flexibility** in rate limiting configuration vs consistency across systems

## 12. Rate Limiting Configuration Architecture: Requestor and Implementor Pattern

### 12.1 Understanding the Requestor/Implementor Pattern

Envoy's rate limiting system follows a clear **Requestor/Implementor** pattern where:

- **Requestor**: The HTTP connection manager, routes, and virtual hosts that *request* rate limiting decisions
- **Implementor**: The rate limiting filters and services that *implement* the limiting logic
- **Binding**: Configuration that connects requestors to implementors
- **Rule Engine**: Logic that selects which limits apply based on request context

### 12.2 Where Configuration Happens

#### Requestor Configuration (Route-level)
```yaml
# In route configuration - THIS IS THE REQUESTOR
routes:
- match: { prefix: "/api" }
  route:
    cluster: api_cluster
    rate_limits:  # REQUESTOR specifies WHAT to limit
    - actions:
      - destination_cluster: {}
    - actions:
      - request_headers:
          header_name: "x-user-id"
          descriptor_key: "user_id"
```

#### Implementor Configuration (Filter-level)
```yaml
# In HTTP connection manager - THIS IS THE IMPLEMENTOR
http_filters:
- name: envoy.filters.http.ratelimit
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimit
    domain: "my_service"  # IMPLEMENTOR specifies HOW to limit
    timeout: 1s
    failure_mode_deny: false
    rate_limit_service:
      grpc_service:
        envoy_grpc:
          cluster_name: rate_limit_service
```

### 12.3 Complete Minimal Configuration Example

Here's a **minimal, focused example** showing the complete picture of local and global rate limiting with one wildcard descriptor:

```yaml
static_resources:
  listeners:
  - address:
      socket_address: { address: 0.0.0.0, port_value: 8080 }
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: minimal_example
          
          # IMPLEMENTORS: Filters that enforce rate limits
          http_filters:
          # Local rate limiting implementor
          - name: envoy.filters.http.local_ratelimit
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
              stat_prefix: local_limiter
              # Default fallback bucket
              token_bucket:
                max_tokens: 100
                tokens_per_fill: 50
                fill_interval: 60s
              # Wildcard descriptor definition
              descriptors:
              - entries:
                - key: tenant_id    # Wildcard - matches any tenant_id value
                token_bucket:
                  max_tokens: 10
                  tokens_per_fill: 5
                  fill_interval: 60s
          
          # Global rate limiting implementor
          - name: envoy.filters.http.ratelimit
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimit
              domain: "api_limits"
              timeout: 1s
              failure_mode_deny: false
              rate_limit_service:
                grpc_service:
                  envoy_grpc:
                    cluster_name: rate_limit_service
          
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
          
          # REQUESTOR: Route that requests rate limiting
          route_config:
            name: api_routes
            virtual_hosts:
            - name: api
              domains: ["*"]
              routes:
              - match: { prefix: "/" }
                route:
                  cluster: backend
                  # Actions create descriptors for both local and global filters
                  rate_limits:
                  - actions:
                    - request_headers:
                        header_name: "x-tenant-id"
                        descriptor_key: "tenant_id"

  clusters:
  - name: backend
    connect_timeout: 5s
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    load_assignment:
      cluster_name: backend
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address: { address: backend-service, port_value: 8080 }
  
  - name: rate_limit_service
    connect_timeout: 1s
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    http2_protocol_options: {}
    load_assignment:
      cluster_name: rate_limit_service
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address: { address: ratelimit-service, port_value: 8081 }
```

### 12.3.1 How This Minimal Example Works

**Request Flow:**
1. **Request arrives** with header `x-tenant-id: company_a`
2. **Route matches** and `rate_limits` actions execute
3. **Action creates descriptor**: `{tenant_id: "company_a"}`
4. **Local filter** checks wildcard descriptor, creates dynamic token bucket for `company_a`
5. **Global filter** sends descriptor to external rate limit service
6. **Both decisions** are enforced before request continues

**Dynamic Behavior:**
- First request with `x-tenant-id: company_a` → Creates token bucket for `company_a`
- First request with `x-tenant-id: company_b` → Creates separate token bucket for `company_b`
- Each tenant gets independent rate limiting with 10 tokens, refilled at 5 per minute

**Key Insight:** One descriptor definition handles unlimited tenants through wildcard matching.

XXX

### 12.4 How the Binding Works

#### Rule-Based Selection Engine
The binding between requestors and implementors happens through:

1. **Descriptor Matching**: Route-level `rate_limits` create descriptors that filters evaluate
2. **Domain Binding**: Global rate limiting uses `domain` to group related limits
3. **Filter Processing Order**: Filters process requests in configured order
4. **Hierarchical Override**: Route configs can override filter defaults via `typed_per_filter_config`

#### Request Processing Flow
```
1. Request arrives at listener
2. HTTP connection manager processes filters in order:
   a. Local rate limiter checks token buckets for matching descriptors
   b. Global rate limiter sends descriptors to external service
   c. Fault filter applies bandwidth limits
3. Route matching determines which rate_limits apply
4. Descriptors are generated from request context
5. Rate limiting decisions are made and enforced
```

### 12.5 Actions: The Transformation Engine

You're absolutely correct! The "actions" list is the **transformation mechanism** that converts route context into rate limit descriptors. Each action is a small processor that extracts specific information from the request and creates descriptor entries.

#### How Actions Work

Each action implements a `populateDescriptor()` method that:

1. **Extracts context** from the request (headers, metadata, IP, etc.)
2. **Transforms it** into a key-value pair
3. **Adds it** to the descriptor that will be sent to the rate limit service

```yaml
rate_limits:
- actions:  # Each action transforms request context into descriptors
  - request_headers:           # ACTION: Extract header value
      header_name: "x-user-id"
      descriptor_key: "user_id"
  - generic_key:               # ACTION: Add static value
      descriptor_value: "premium_api"
  - remote_address: {}         # ACTION: Extract client IP
```

#### Dynamic vs Static Descriptors Through Actions

**Dynamic Descriptors** are created when actions extract **variable values** from request context:

**Dynamic Actions (Create Variable Descriptors):**
```yaml
rate_limits:
- actions:
  - request_headers:           # DYNAMIC: Value changes per request
      header_name: "x-user-id"  # Could be "alice", "bob", "charlie", etc.
      descriptor_key: "user_id"
  - remote_address: {}         # DYNAMIC: Different IP per client
  - query_parameters:          # DYNAMIC: Different query values
      parameter_name: "api_key"
      descriptor_key: "key"
```

**Static Actions (Create Fixed Descriptors):**
```yaml
rate_limits:
- actions:
  - generic_key:               # STATIC: Always same value
      descriptor_value: "premium_api"
  - destination_cluster: {}    # STATIC: Same for all requests to this route
  - source_cluster: {}         # STATIC: Same for all requests from this Envoy
```

#### How Dynamic Descriptors Enable Wildcard Matching

You're absolutely right to be confused! I mixed up two different configuration locations. Let me clarify where `descriptors` and `entries` actually appear:

**The `descriptors` field appears in the LOCAL RATE LIMIT FILTER configuration, NOT in the main route configuration I showed earlier.**

**Local Rate Limit Filter Configuration (IMPLEMENTOR side):**
```yaml
# This goes in the HTTP filter configuration
- name: envoy.filters.http.local_ratelimit
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
    stat_prefix: local_rate_limiter
    token_bucket:
      max_tokens: 100
      tokens_per_fill: 50
      fill_interval: 60s
    # THIS is where descriptors appear - in the filter config
    descriptors:
    - entries:
      - key: user_id        # Wildcard - empty value matches ANY user_id
      - key: operation
        value: "upload"     # Exact match for upload operations
      token_bucket:
        max_tokens: 10
        tokens_per_fill: 5
        fill_interval: 60s
```

**Route Configuration (REQUESTOR side - what I showed in the big example):**
```yaml
# This is in the route configuration (what was in the big example)
routes:
- match: { prefix: "/api/upload" }
  route:
    cluster: upload_cluster
    rate_limits:  # THIS creates descriptors sent to the filter above
    - actions:
      - request_headers:           # Creates dynamic user_id values
          header_name: "x-user-id"
          descriptor_key: "user_id"  # This key matches the wildcard above
      - generic_key:               # Creates the operation descriptor entry
          descriptor_key: "operation"
          descriptor_value: "upload"  # This matches exact value above
```

**How They Connect:**
1. Route `actions` create descriptors: `{user_id: alice, operation: upload}`
2. Local rate limit filter checks its `descriptors` list for matches
3. Wildcard descriptor matches because `user_id` key matches (value is wildcard)
4. Creates dynamic token bucket for `{user_id: alice, operation: upload}`

**Runtime Behavior:**
1. **Request 1**: `x-user-id: alice` → Descriptor: `{user_id: alice, operation: upload}`
2. **Request 2**: `x-user-id: bob` → Descriptor: `{user_id: bob, operation: upload}`
3. **Request 3**: `x-user-id: charlie` → Descriptor: `{user_id: charlie, operation: upload}`

Each creates a **separate token bucket** dynamically managed by the LRU cache.

#### Action Processing Flow

```
1. Request matches route with rate_limits
2. For each rate_limit entry:
   a. Process each action in the actions list
   b. Each action calls populateDescriptor()
   c. Action extracts relevant data from request context
   d. Action creates DescriptorEntry {key, value}
   e. All entries combined into final Descriptor
3. Descriptor sent to rate limiting implementor
4. Rate limiting decision made based on descriptor
```

#### Example Action Implementations

**Request Headers Action (Dynamic):**
```cpp
bool RequestHeadersAction::populateDescriptor(RateLimit::DescriptorEntry& descriptor_entry,
                                              const std::string&, 
                                              const Http::RequestHeaderMap& headers,
                                              const StreamInfo::StreamInfo&) const {
  const auto header_value = headers.get(header_name_);
  if (header_value.empty()) {
    return skip_if_absent_;  // Skip if header missing
  }
  // DYNAMIC: Value extracted from actual request header
  descriptor_entry = {descriptor_key_, std::string(header_value[0]->value().getStringView())};
  return true;
}
```

**Remote Address Action (Dynamic):**
```cpp
bool RemoteAddressAction::populateDescriptor(RateLimit::DescriptorEntry& descriptor_entry,
                                             const std::string&,
                                             const Http::RequestHeaderMap&,
                                             const StreamInfo::StreamInfo& info) const {
  const auto& remote_address = info.downstreamAddressProvider().remoteAddress();
  if (remote_address->type() != Network::Address::Type::Ip) {
    return false;
  }
  // DYNAMIC: Value extracted from actual client IP
  descriptor_entry = {"remote_address", remote_address->ip()->addressAsString()};
  return true;
}
```

**Generic Key Action (Static):**
```cpp
bool GenericKeyAction::populateDescriptor(RateLimit::DescriptorEntry& descriptor_entry,
                                          const std::string&,
                                          const Http::RequestHeaderMap&,
                                          const StreamInfo::StreamInfo&) const {
  // STATIC: Always returns the same configured value
  descriptor_entry = {"generic_key", descriptor_value_};
  return true;
}
```

#### Action Transformation Examples

| Action Type | Input Context | Output Descriptor | Dynamic? |
|-------------|---------------|-------------------|----------|
| `request_headers` | `x-user-id: alice` | `{user_id: alice}` | ✅ Dynamic |
| `remote_address` | Client IP: `192.168.1.100` | `{remote_address: 192.168.1.100}` | ✅ Dynamic |
| `query_parameters` | `?tier=gold` | `{tier: gold}` | ✅ Dynamic |
| `generic_key` | Static config | `{generic_key: premium_api}` | ❌ Static |
| `destination_cluster` | Route cluster: `api-v1` | `{destination_cluster: api-v1}` | ❌ Static |
| `source_cluster` | Local cluster | `{source_cluster: frontend}` | ❌ Static |

#### Multiple Actions = Combined Descriptor

```yaml
rate_limits:
- actions:
  - request_headers:          # DYNAMIC
      header_name: "x-user-id"
      descriptor_key: "user_id"
  - generic_key:              # STATIC
      descriptor_value: "api_endpoint"
  - remote_address: {}        # DYNAMIC
```

**Results in descriptor:**
```
{
  entries: [
    {key: "user_id", value: "alice"},           # Dynamic from header
    {key: "generic_key", value: "api_endpoint"}, # Static from config
    {key: "remote_address", value: "192.168.1.100"} # Dynamic from IP
  ]
}
```

#### Dynamic Descriptor Management

**For Local Rate Limiting:**
- Dynamic values create **new token buckets** when first encountered
- **LRU cache** manages memory usage (default 20 buckets)
- **Wildcard matching** allows unlimited cardinality with controlled memory

**For Global Rate Limiting:**
- Dynamic values sent to **external rate limit service**
- Service determines limits based on full descriptor context
- No local memory management needed

#### Formatting and Substitution

Some actions support **advanced dynamic behavior** through formatting:

```yaml
rate_limits:
- actions:
  - metadata:
      metadata_key:
        key: "dynamic.rate_limit"
        path: ["user_tier"]
      descriptor_key: "tier"
  - request_headers:
      header_name: "x-request-id"
      descriptor_key: "request"
      # Can include substitution patterns
```

This descriptor is then sent to the rate limiting service which uses it to look up the appropriate rate limit rules and make limiting decisions.

#### Key Insight: Actions Enable Cardinality Control

The **choice of actions** determines the **cardinality** of your rate limiting:

- **High cardinality**: `request_headers` (user IDs), `remote_address` (client IPs)
- **Medium cardinality**: `query_parameters` (API keys), `metadata` (tenant IDs)  
- **Low cardinality**: `destination_cluster`, `source_cluster`, `generic_key`

Understanding this helps you design rate limiting strategies that balance **granularity** with **memory usage** and **performance**.

### 12.6 Configuration Binding Summary

| Component | Role | Configuration Location | Binding Mechanism |
|-----------|------|----------------------|-------------------|
| Route `rate_limits` | Requestor | Route/VirtualHost | Descriptor generation via actions |
| Actions List | Transformer | Within rate_limits | Request context → descriptor entries |
| HTTP Filters | Implementor | HttpConnectionManager | Filter chain processing |
| Per-route Config | Override | Route level | `typed_per_filter_config` |
| Rate Limit Service | External Implementor | Cluster definition | gRPC service binding |

This architecture provides clear separation of concerns:
- **Routes declare intent** (what should be limited)
- **Actions transform context** (request → descriptors)
- **Filters implement policy** (how to limit)
- **Configuration binds them together** (descriptor matching and domain grouping)
- **Hierarchical overrides** allow fine-tuning per route

## 13. Descriptor Instances vs Descriptor Definitions: Complete Example

To clarify the critical distinction between **descriptor instances** (created by routes) and **descriptor definitions** (defined in local filters), here's a complete minimal example that demonstrates exactly how different filters interact with the same descriptor instances.

### 13.1 Terminology Clarification

The current terminology around "descriptor instances" and "descriptor definitions" can be confusing. Here are clearer alternatives:

#### **Current vs Better Terminology:**

| Current Term | Better Term | Description |
|--------------|-------------|-------------|
| "Descriptor Instance" | **"Rate Limit Request"** | Runtime key-value data created by route actions from actual request context |
| "Descriptor Definition" | **"Rate Limit Pattern"** | Configuration templates in local filters that specify which requests to limit and how |
| "Actions" | **"Request Extractors"** | Active components that extract data from HTTP requests to build rate limit requests |

#### **Alternative Terminology Options:**

**For "Descriptor Instance" (Runtime Data):**
- **Rate Limit Request** ✅ (most descriptive)
- **Descriptor Payload** 
- **Rate Limit Context**
- **Descriptor Data**
- **Limit Request**

**For "Descriptor Definition" (Configuration Pattern):**
- **Rate Limit Pattern** ✅ (most descriptive)
- **Descriptor Template**
- **Rate Limit Rule**  
- **Limit Pattern**
- **Descriptor Matcher**

**For "Actions" (Data Extractors):**
- **Request Extractors** ✅ (most descriptive - shows active extraction)
- **Data Builders** 
- **Context Extractors**
- **Request Processors**
- **Data Collectors**
- **Field Extractors**

#### **Why "Request Extractors" is Better:**

**Problems with "Actions":**
- Sounds passive or like a side effect
- Doesn't indicate the extraction/building nature
- Could be confused with HTTP actions (GET, POST, etc.)
- Doesn't show the direction of data flow

**Benefits of "Request Extractors":**
- **Active verb**: Shows they actively extract data
- **Clear purpose**: Extract information from requests
- **Data flow**: Request → Extractor → Rate Limit Request
- **Intuitive**: Developers understand "extracting" data

#### **Using Better Terminology:**

**Rate Limit Request**: Runtime key-value data created by route extractors during request processing
- Example: `{user_id: "alice", api_key: "key123", service: "api_service"}`
- **Created by**: Request extractors from actual HTTP request context
- **Used by**: All rate limiting filters in the same stage

**Rate Limit Pattern**: Configuration templates defined in local rate limit filters that specify how to handle rate limit requests
- Example: `entries: [{key: user_id}]` with token bucket configuration
- **Defined in**: Local rate limit filter configuration
- **Purpose**: Match incoming rate limit requests and apply local token bucket limits

**Request Extractors**: Active components that extract data from HTTP requests to build rate limit requests
- Example: `request_headers: {header_name: "x-user-id", descriptor_key: "user_id"}`
- **Function**: Extract `x-user-id` header value and create `{user_id: "alice"}` rate limit request
- **Types**: `request_headers`, `remote_address`, `generic_key`, `query_parameters`, etc.

> **Note for System Designers**: When implementing similar rate limiting systems, consider terminology that better reflects the actual behavior. For the conjunctive matching elements within patterns (Envoy's "entries"), alternatives like **"key_conditions"** (emphasizing the conditional logic) or **"key_specs"** (concise and neutral) may be clearer than generic terms like "entries". The key insight is that these elements function as **conjunctive clauses** that must all match for a pattern to apply, rather than simple list entries.

#### **Complete Flow with Better Terminology:**

```
HTTP Request → Request Extractors → Rate Limit Requests → Rate Limiting Filters

Example:
GET /api HTTP/1.1
x-user-id: alice
x-api-key: key123
         ↓
Request Extractors:
- request_headers extractor → {user_id: "alice"}
- request_headers extractor → {api_key: "key123"}  
- generic_key extractor → {service: "api_service"}
         ↓
Rate Limit Requests: [{user_id: "alice"}, {api_key: "key123"}, {service: "api_service"}]
         ↓
Local Filter (Rate Limit Patterns) + Global Filter (Forward All)
```

### 13.2 Global Rate Limiting Without Local Filters

**Important**: Global rate limiters can work independently without any local rate limiters because they get descriptor instances directly from route actions, not from local filters.

#### 13.2.1 Global-Only Example

```yaml
static_resources:
  listeners:
  - address: { socket_address: { address: 0.0.0.0, port_value: 8080 } }
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          
          http_filters:
          # GLOBAL FILTER ONLY - no local filters needed
          - name: envoy.filters.http.ratelimit
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimit
              domain: "global_api_limits"
              stage: 0
              rate_limit_service:
                grpc_service:
                  envoy_grpc:
                    cluster_name: rate_limit_service
          
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
          
          # ROUTES: Create descriptor instances for global filter
          route_config:
            virtual_hosts:
            - name: api
              domains: ["*"]
              routes:
              - match: { prefix: "/api" }
                route:
                  cluster: backend
                  # DESCRIPTOR INSTANCES - global filter will receive these
                  rate_limits:
                  - stage: 0    # Same stage as global filter
                    actions:
                    - request_headers:
                        header_name: "x-user-id"
                        descriptor_key: "user_id"
                    - request_headers:
                        header_name: "x-api-key"
                        descriptor_key: "api_key"
                    - generic_key:
                        descriptor_key: "service"
                        descriptor_value: "api_service"

  clusters:
  - name: backend
    connect_timeout: 5s
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    load_assignment:
      cluster_name: backend
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address: { address: backend-service, port_value: 8080 }
  
  - name: rate_limit_service
    connect_timeout: 1s
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    http2_protocol_options: {}
    load_assignment:
      cluster_name: rate_limit_service
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address: { address: ratelimit-service, port_value: 8081 }
```

#### 13.2.2 Processing Flow for Global-Only Setup

For a request with headers `x-user-id: alice` and `x-api-key: key123`:

**Step 1: Route Creates Rate Limit Requests**
```
Route actions create THREE rate limit requests:
1. {user_id: "alice"}
2. {api_key: "key123"}
3. {service: "api_service"}
```

**Step 2: Global Filter Processes All Rate Limit Requests**
```
Global Filter (Stage 0):
- Receives all 3 rate limit requests
- Forwards ALL to "global_api_limits" domain
- External service receives: [
    {user_id: "alice"},
    {api_key: "key123"},
    {service: "api_service"}
  ]
```

**Step 3: External Service Makes Decision**
```yaml
# Rate limit service configuration handles all logic
global_api_limits:
  - key: user_id
    descriptors:
    - rate_limit: { unit: minute, requests_per_unit: 100 }
  - key: api_key
    descriptors:
    - rate_limit: { unit: minute, requests_per_unit: 1000 }
  - key: service
    value: "api_service"
    descriptors:
    - rate_limit: { unit: minute, requests_per_unit: 5000 }
```

**Result**: Request passes only if external service approves all descriptors.

#### 13.2.3 Key Insights for Global-Only Setup

1. **No Local Filters Required**: Global filters work independently of local filters
2. **Route Actions Are Sufficient**: Rate limit requests come from route actions, not local filters
3. **External Service Handles Logic**: All rate limiting decisions made by external service
4. **Simpler Configuration**: No need to configure local rate limit patterns
5. **Centralized Control**: All rate limiting logic centralized in external service

#### 13.2.4 When to Use Global-Only

**Global-only is ideal for:**
- **Centralized Rate Limiting**: All decisions made by external service
- **Complex Business Logic**: External service can implement sophisticated rules
- **Multi-Service Coordination**: Rate limits shared across multiple Envoy instances
- **Dynamic Configuration**: External service can change limits without Envoy restart
- **Audit and Compliance**: Centralized logging and monitoring

**Local + Global is better for:**
- **Performance**: Local checks avoid network calls
- **Fail-Safe**: Local limits provide backup when external service is down
- **Layered Defense**: Multiple rate limiting strategies

### 13.3 Complete Example: Two Local + One Global Filter, Same Stage

```yaml
static_resources:
  listeners:
  - address: { socket_address: { address: 0.0.0.0, port_value: 8080 } }
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          
          http_filters:
          # LOCAL FILTER 1: Handles user-based rate limiting
          - name: envoy.filters.http.local_ratelimit
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
              stat_prefix: user_limiter
              stage: 1
              # DESCRIPTOR DEFINITIONS - patterns this filter can handle
              descriptors:
              - entries:
                - key: user_id        # Matches descriptor instances with user_id key
                token_bucket:
                  max_tokens: 10
                  tokens_per_fill: 5
                  fill_interval: 60s
          
          # LOCAL FILTER 2: Handles tenant-based rate limiting
          - name: envoy.filters.http.local_ratelimit
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
              stat_prefix: tenant_limiter
              stage: 1
              # DESCRIPTOR DEFINITIONS - different patterns than filter 1
              descriptors:
              - entries:
                - key: tenant_id      # Matches descriptor instances with tenant_id key
                token_bucket:
                  max_tokens: 100
                  tokens_per_fill: 50
                  fill_interval: 60s
          
          # GLOBAL FILTER: Forwards all descriptor instances to external service
          - name: envoy.filters.http.ratelimit
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimit
              domain: "api_limits"
              stage: 1
              # NO DESCRIPTOR DEFINITIONS - global filters don't define patterns
              rate_limit_service:
                grpc_service:
                  envoy_grpc:
                    cluster_name: rate_limit_service
          
          - name: envoy.filters.http.router
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
          
          # ROUTES: Create descriptor instances that filters process
          route_config:
            virtual_hosts:
            - name: api
              domains: ["*"]
              routes:
              - match: { prefix: "/api" }
                route:
                  cluster: backend
                  # DESCRIPTOR INSTANCE CREATORS - these create runtime data
                  rate_limits:
                  - stage: 1    # Creates descriptor instance for user
                    actions:
                    - request_headers:
                        header_name: "x-user-id"
                        descriptor_key: "user_id"
                  - stage: 1    # Creates descriptor instance for tenant  
                    actions:
                    - request_headers:
                        header_name: "x-tenant-id"
                        descriptor_key: "tenant_id"
                  - stage: 1    # Creates descriptor instance for operation
                    actions:
                    - generic_key:
                        descriptor_key: "operation"
                        descriptor_value: "api_call"
              
              # ADMIN ROUTE: Per-route override for stricter limits
              - match: { prefix: "/admin" }
                route:
                  cluster: backend
                  # DESCRIPTOR INSTANCE CREATORS - same as /api route
                  rate_limits:
                  - stage: 1
                    actions:
                    - request_headers:
                        header_name: "x-user-id"
                        descriptor_key: "user_id"
                  - stage: 1
                    actions:
                    - request_headers:
                        header_name: "x-tenant-id"
                        descriptor_key: "tenant_id"
                  - stage: 1
                    actions:
                    - generic_key:
                        descriptor_key: "operation"
                        descriptor_value: "admin_call"
                
                # PER-ROUTE FILTER OVERRIDES - stricter limits for admin endpoints
                typed_per_filter_config:
                  # Override Local Filter 1 (User Limiter) for admin route
                  envoy.filters.http.local_ratelimit:
                    "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
                    stat_prefix: admin_user_limiter
                    stage: 1
                    descriptors:
                    - entries:
                      - key: user_id
                      token_bucket:
                        max_tokens: 2      # Much stricter than default (10)
                        tokens_per_fill: 1
                        fill_interval: 60s
                  
                  # Override Global Filter for admin route  
                  envoy.filters.http.ratelimit:
                    "@type": type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimitPerRoute
                    domain: "admin_limits"  # Different domain for admin
                    stage: 1

  clusters:
  - name: backend
    connect_timeout: 5s
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    load_assignment:
      cluster_name: backend
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address: { address: backend-service, port_value: 8080 }
  
  - name: rate_limit_service
    connect_timeout: 1s
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    http2_protocol_options: {}
    load_assignment:
      cluster_name: rate_limit_service
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address: { address: ratelimit-service, port_value: 8081 }
```

### 13.3 Request Processing Flow

#### **For /api requests** with headers `x-user-id: alice` and `x-tenant-id: company_a`:

##### **Step 1: Route Creates Descriptor Instances**
```
Route actions create THREE descriptor instances:
1. {user_id: "alice"}
2. {tenant_id: "company_a"}  
3. {operation: "api_call"}
```

##### **Step 2: All Stage 1 Filters Process Same Descriptor Instances (Default Configuration)**

**Local Filter 1 (User Limiter):**
```
Uses DEFAULT configuration:
- {user_id: "alice"} → MATCHES → Checks token bucket (10 tokens max, 5 per fill)
- {tenant_id: "company_a"} → NO MATCH → Ignores
- {operation: "api_call"} → NO MATCH → Ignores
```

**Local Filter 2 (Tenant Limiter):**
```
Uses DEFAULT configuration:
- {user_id: "alice"} → NO MATCH → Ignores
- {tenant_id: "company_a"} → MATCHES → Checks token bucket (100 tokens max, 50 per fill)
- {operation: "api_call"} → NO MATCH → Ignores
```

**Global Filter:**
```
Uses DEFAULT configuration:
- Forwards ALL instances to "api_limits" domain
- Service receives: [
    {user_id: "alice"},
    {tenant_id: "company_a"},
    {operation: "api_call"}
  ]
```

#### **For /admin requests** with headers `x-user-id: alice` and `x-tenant-id: company_a`:

##### **Step 1: Route Creates Descriptor Instances**
```
Route actions create THREE descriptor instances:
1. {user_id: "alice"}
2. {tenant_id: "company_a"}  
3. {operation: "admin_call"}  # Different operation value
```

##### **Step 2: All Stage 1 Filters Process Same Descriptor Instances (Per-Route Overrides)**

**Local Filter 1 (User Limiter):**
```
Uses PER-ROUTE OVERRIDE configuration:
- {user_id: "alice"} → MATCHES → Checks STRICTER token bucket (2 tokens max, 1 per fill)
- {tenant_id: "company_a"} → NO MATCH → Ignores  
- {operation: "admin_call"} → NO MATCH → Ignores
```

**Local Filter 2 (Tenant Limiter):**
```
Uses DEFAULT configuration (no override specified):
- {user_id: "alice"} → NO MATCH → Ignores
- {tenant_id: "company_a"} → MATCHES → Checks default token bucket (100 tokens max, 50 per fill)
- {operation: "admin_call"} → NO MATCH → Ignores
```

**Global Filter:**
```
Uses PER-ROUTE OVERRIDE configuration:
- Forwards ALL instances to "admin_limits" domain (different from default)
- Service receives: [
    {user_id: "alice"},
    {tenant_id: "company_a"},
    {operation: "admin_call"}
  ]
```

##### **Step 3: Combined Result**
```
/api request passes IF AND ONLY IF:
✓ Local Filter 1: user_id token bucket (10 max) has tokens
✓ Local Filter 2: tenant_id token bucket (100 max) has tokens  
✓ Global Filter: "api_limits" service approves all 3 descriptors

/admin request passes IF AND ONLY IF:
✓ Local Filter 1: user_id token bucket (2 max) has tokens  # STRICTER
✓ Local Filter 2: tenant_id token bucket (100 max) has tokens  # SAME
✓ Global Filter: "admin_limits" service approves all 3 descriptors  # DIFFERENT DOMAIN
```

### 13.4 Key Insights from Per-Route Override Example

#### **Per-Route Override Mechanism:**
1. **Route-specific configuration** overrides filter default configuration
2. **Only specified filters** get overridden - others use defaults
3. **Same descriptor instances** flow to all filters regardless of overrides
4. **Different processing logic** applied based on route-specific config

#### **Local Filter Override Behavior:**
- **Local Filter 1**: Gets stricter limits (2 tokens vs 10) for `/admin` route
- **Local Filter 2**: Uses default configuration for both routes (no override specified)
- **Same descriptor matching**: Both routes create `{user_id: "alice"}` and `{tenant_id: "company_a"}`

#### **Global Filter Override Behavior:**
- **Domain Change**: `/admin` route uses `"admin_limits"` domain vs `"api_limits"`
- **Same Descriptor Forwarding**: All descriptor instances forwarded to external service
- **External Service Differentiation**: Different domains allow different rate limit rules

#### **RateLimitPerRoute vs RateLimit Types:**
- **`RateLimit`**: Used for main filter configuration in `http_filters`
- **`RateLimitPerRoute`**: Used specifically for per-route overrides in `typed_per_filter_config`
- **Different Configuration Options**: Per-route type has subset of main filter options

#### **Selective Override Strategy:**
```yaml
/api route:   [Default Filter 1] + [Default Filter 2] + [Default Global → "api_limits"]
/admin route: [Override Filter 1] + [Default Filter 2] + [Override Global → "admin_limits"]
```

**Benefits:**
- **Granular Control**: Different limits for different endpoints
- **Configuration Reuse**: Most filters keep default configuration
- **Service Segregation**: Different external services for different route types

#### **Descriptor Instance Flow:**
1. **Routes create** descriptor instances from request context
2. **All same-stage filters receive** the same descriptor instances
3. **Local filters selectively process** instances matching their definitions
4. **Global filters forward all** instances to external services

#### **Local Filter Selectivity:**
- **Local Filter 1** only processes `{user_id: "alice"}` because it has a matching descriptor definition
- **Local Filter 2** only processes `{tenant_id: "company_a"}` because it has a matching descriptor definition
- **Neither local filter** processes `{operation: "api_call"}` because neither has a matching definition

#### **Global Filter Behavior:**
- **Receives all descriptor instances** regardless of keys
- **Forwards everything** to external rate limit service
- **No filtering or selection** - the external service handles matching

#### **External Rate Limit Service:**
```yaml
# Service configuration (example)
api_limits:  # Domain from global filter
  - key: user_id
    descriptors:
    - rate_limit: { unit: minute, requests_per_unit: 100 }
  - key: tenant_id  
    descriptors:
    - rate_limit: { unit: minute, requests_per_unit: 1000 }
  - key: operation
    value: "api_call"
    descriptors:
    - rate_limit: { unit: minute, requests_per_unit: 500 }
```

### 13.5 What "Cannot Access" Actually Means

When we say **"Global filters cannot access descriptors defined by local filters"**, we mean:

**Global filters cannot:**
- Access the **descriptor definitions** (patterns) configured in local filters
- Use local filter token buckets or state
- Filter descriptor instances based on local filter patterns

**Global filters CAN:**
- Receive the **same descriptor instances** that local filters receive
- Forward all descriptor instances to external services
- Process descriptor instances independently of local filters

### 13.6 Summary of Data Flow

```
REQUEST → Route Actions → Descriptor Instances → All Stage 1 Filters

Descriptor Instances Created:
[{user_id: "alice"}, {tenant_id: "company_a"}, {operation: "api_call"}]
                                    ↓
                           All go to all stage 1 filters
                                    ↓
            ┌─────────────────────┬─────────────────────┬─────────────────────┐
            ↓                     ↓                     ↓                     ↓
    Local Filter 1        Local Filter 2        Global Filter
    (User Limiter)        (Tenant Limiter)      (API Limits)
            ↓                     ↓                     ↓
    Processes only        Processes only        Forwards all to
    {user_id: "alice"}    {tenant_id: "company_a"}    external service
    based on its          based on its          (no filtering)
    descriptor            descriptor
    definitions           definitions
```

**Key Insight**: All filters with the same stage receive the **same descriptor instances**, but **local filters selectively process** them based on their **descriptor definitions**, while **global filters forward all** instances to external services for processing.

## 14. Envoy Rate Limiting Type Reference

Envoy uses strongly typed configuration with `@type` annotations to ensure type safety and validation. Here are all the important types related to rate limiting that we've discussed:

### 14.1 HTTP Filter Types

#### **Local Rate Limiting Filter:**
```yaml
- name: envoy.filters.http.local_ratelimit
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
    # Configuration fields for local rate limiting
```

#### **Global Rate Limiting Filter:**
```yaml
- name: envoy.filters.http.ratelimit
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimit
    # Configuration fields for global rate limiting
```

#### **Quota Filter (Streaming Rate Limiting):**
```yaml
- name: envoy.filters.http.quota
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.quota.v3.Quota
    # Configuration fields for quota-based rate limiting
```

#### **HTTP Connection Manager (Container for HTTP Filters):**
```yaml
- name: envoy.filters.network.http_connection_manager
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
    # Contains http_filters array
```

### 14.2 Network Filter Types

#### **Local Rate Limiting Network Filter:**
```yaml
- name: envoy.filters.network.local_ratelimit
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.network.local_ratelimit.v3.LocalRateLimit
    # Network-level rate limiting
```

#### **Global Rate Limiting Network Filter:**
```yaml
- name: envoy.filters.network.ratelimit
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.network.ratelimit.v3.RateLimit
    # Network-level global rate limiting
```

### 14.3 Listener Filter Types

#### **Connection Limiting Listener Filter:**
```yaml
- name: envoy.filters.listener.connection_limit
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.listener.connection_limit.v3.ConnectionLimit
    # Listener-level connection limiting
```

### 14.4 Route Configuration Types

#### **Route Configuration:**
```yaml
route_config:
  "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.Rds
  # OR inline:
  "@type": type.googleapis.com/envoy.config.route.v3.RouteConfiguration
```

#### **Route Rate Limit Actions:**
```yaml
# Within RouteConfiguration
rate_limits:
- actions:
  - "@type": type.googleapis.com/envoy.config.route.v3.RateLimit.Action
    # Specific action types below
```

### 14.5 Rate Limit Action Types

#### **Request Headers Action:**
```yaml
- request_headers:
    "@type": type.googleapis.com/envoy.config.route.v3.RateLimit.Action.RequestHeaders
    header_name: "x-user-id"
    descriptor_key: "user_id"
```

#### **Remote Address Action:**
```yaml
- remote_address:
    "@type": type.googleapis.com/envoy.config.route.v3.RateLimit.Action.RemoteAddress
```

#### **Generic Key Action:**
```yaml
- generic_key:
    "@type": type.googleapis.com/envoy.config.route.v3.RateLimit.Action.GenericKey
    descriptor_value: "api_endpoint"
```

#### **Destination Cluster Action:**
```yaml
- destination_cluster:
    "@type": type.googleapis.com/envoy.config.route.v3.RateLimit.Action.DestinationCluster
```

#### **Source Cluster Action:**
```yaml
- source_cluster:
    "@type": type.googleapis.com/envoy.config.route.v3.RateLimit.Action.SourceCluster
```

#### **Header Value Match Action:**
```yaml
- header_value_match:
    "@type": type.googleapis.com/envoy.config.route.v3.RateLimit.Action.HeaderValueMatch
    descriptor_value: "premium"
    headers:
    - name: "x-tier"
      exact_match: "premium"
```

#### **Query Parameters Action:**
```yaml
- query_parameters:
    "@type": type.googleapis.com/envoy.config.route.v3.RateLimit.Action.QueryParameters
    descriptor_key: "api_key"
    query_parameters:
    - name: "key"
```

#### **Metadata Action:**
```yaml
- metadata:
    "@type": type.googleapis.com/envoy.config.route.v3.RateLimit.Action.Metadata
    metadata_key:
      key: "envoy.common"
      path: ["user_id"]
    descriptor_key: "user"
```

### 14.6 Token Bucket Configuration Types

#### **Token Bucket (Used in Local Rate Limiting):**
```yaml
token_bucket:
  "@type": type.googleapis.com/envoy.type.v3.TokenBucket
  max_tokens: 100
  tokens_per_fill: 50
  fill_interval: 60s
```

### 14.7 Rate Limit Service Types

#### **Rate Limit Service Configuration:**
```yaml
rate_limit_service:
  "@type": type.googleapis.com/envoy.config.ratelimit.v3.RateLimitServiceConfig
  grpc_service:
    envoy_grpc:
      cluster_name: "rate_limit_service"
  # OR
  transport_api_version: V3
```

#### **gRPC Service Configuration:**
```yaml
grpc_service:
  "@type": type.googleapis.com/envoy.config.core.v3.GrpcService
  envoy_grpc:
    cluster_name: "rate_limit_service"
  timeout: 1s
```

### 14.8 Per-Route Filter Configuration Types

#### **Per-Route Local Rate Limit Override:**
```yaml
typed_per_filter_config:
  envoy.filters.http.local_ratelimit:
    "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
    # Override configuration for this route
```

#### **Per-Route Global Rate Limit Override:**
```yaml
typed_per_filter_config:
  envoy.filters.http.ratelimit:
    "@type": type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimitPerRoute
    # Override configuration for this route
```

### 14.9 Cluster Configuration Types

#### **Cluster for Rate Limit Service:**
```yaml
clusters:
- name: rate_limit_service
  "@type": type.googleapis.com/envoy.config.cluster.v3.Cluster
  connect_timeout: 1s
  type: STRICT_DNS
  lb_policy: ROUND_ROBIN
  http2_protocol_options: {}
```

### 14.10 Common Data Types

#### **Duration:**
```yaml
fill_interval:
  "@type": type.googleapis.com/google.protobuf.Duration
  seconds: 60
  nanos: 0
# OR simplified:
fill_interval: 60s
```

#### **Header Matcher:**
```yaml
headers:
- "@type": type.googleapis.com/envoy.config.route.v3.HeaderMatcher
  name: "x-user-tier"
  exact_match: "premium"
```

#### **Metadata Key:**
```yaml
metadata_key:
  "@type": type.googleapis.com/envoy.type.metadata.v3.MetadataKey
  key: "envoy.common"
  path: ["user_id"]
```

### 14.11 Type Hierarchy Summary

```
Rate Limiting Filter Types:
├── HTTP Filters
│   ├── envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
│   ├── envoy.extensions.filters.http.ratelimit.v3.RateLimit
│   ├── envoy.extensions.filters.http.ratelimit.v3.RateLimitPerRoute
│   └── envoy.extensions.filters.http.quota.v3.Quota
├── Network Filters
│   ├── envoy.extensions.filters.network.local_ratelimit.v3.LocalRateLimit
│   └── envoy.extensions.filters.network.ratelimit.v3.RateLimit
└── Listener Filters
    └── envoy.extensions.filters.listener.connection_limit.v3.ConnectionLimit

Route Configuration Types:
├── envoy.config.route.v3.RouteConfiguration
├── envoy.config.route.v3.RateLimit
└── envoy.config.route.v3.RateLimit.Action
    ├── .RequestHeaders
    ├── .RemoteAddress
    ├── .GenericKey
    ├── .DestinationCluster
    ├── .SourceCluster
    ├── .HeaderValueMatch
    ├── .QueryParameters
    └── .Metadata

Supporting Types:
├── envoy.type.v3.TokenBucket
├── envoy.config.ratelimit.v3.RateLimitServiceConfig
├── envoy.config.core.v3.GrpcService
├── envoy.config.cluster.v3.Cluster
├── envoy.config.route.v3.HeaderMatcher
├── envoy.type.metadata.v3.MetadataKey
└── google.protobuf.Duration
```

### 14.12 Important Notes on Types

1. **Version Consistency**: Always use consistent API versions (v3 is current)
2. **Type Safety**: The `@type` field ensures configuration validation
3. **Proto Imports**: Each type corresponds to a specific .proto file
4. **Backwards Compatibility**: Older v2 types are deprecated but may still appear
5. **Extension Types**: Rate limiting types are extensions, not core Envoy types

### 14.13 Common Type Mistakes

#### **Missing @type Field:**
```yaml
# WRONG - will fail validation
typed_config:
  stat_prefix: "rate_limiter"
  
# RIGHT - includes required type
typed_config:
  "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
  stat_prefix: "rate_limiter"
```

#### **Wrong Version:**
```yaml
# DEPRECATED - v2 API
"@type": type.googleapis.com/envoy.config.filter.http.rate_limit.v2.RateLimit

# CURRENT - v3 API  
"@type": type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimit
```

#### **Missing Field Types:**
```yaml
# WRONG - actions need explicit typing in complex configs
actions:
- request_headers:
    header_name: "x-user-id"

# RIGHT - explicit typing (though often inferred)
actions:
- request_headers:
    "@type": type.googleapis.com/envoy.config.route.v3.RateLimit.Action.RequestHeaders
    header_name: "x-user-id"
```

This type reference should help you navigate Envoy's strongly typed configuration system for rate limiting!

## Conclusion

Envoy provides a sophisticated and flexible rate limiting system with extensive configuration options, hierarchical overrides, and dynamic capabilities. The resource management system complements rate limiting with global stability mechanisms, though it operates with different design principles focused on system-wide protection rather than granular per-route controls. Understanding these systems' capabilities and limitations is crucial for effective deployment and operation of Envoy in production environments.
