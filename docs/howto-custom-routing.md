# How to Customize Model Routing

routatic-proxy routes requests to different models based on request content. You can customize this behavior through configuration.

## Understanding Scenarios

Each request is classified into a scenario, which maps to a model:

| Scenario | Trigger | Default Model |
|----------|---------|---------------|
| `default` | No special patterns detected | Kimi K2.6 |
| `complex` | Architectural keywords, tool operations | GLM-5.1 |
| `think` | Reasoning keywords in system prompt | GLM-5 |
| `background` | Simple read-only ops (ls, cat, "what is") | Qwen3.5 Plus |
| `long_context` | Token count > threshold (default 100K) | MiniMax M2.5 |
| `vision` | Request contains images | (must configure) |
| `fast` | Streaming requests (when scenario routing disabled) | Qwen3.6 Plus |

## Override Scenario Models

Change which model handles each scenario:

```json
{
  "models": {
    "default": {
      "provider": "opencode-go",
      "model_id": "kimi-k2.6",
      "temperature": 0.7,
      "max_tokens": 4096
    },
    "complex": {
      "provider": "opencode-go",
      "model_id": "glm-5.1",
      "temperature": 0.7,
      "max_tokens": 4096
    }
  }
}
```

## Add Model Overrides

Model overrides let specific model names bypass scenario routing:

```json
{
  "model_overrides": {
    "deepseek-v4-pro": {
      "provider": "opencode-zen",
      "model_id": "deepseek-v4-pro",
      "temperature": 0.7,
      "max_tokens": 8192,
      "reasoning_effort": "max",
      "thinking": { "type": "enabled" }
    }
  }
}
```

When Claude Code requests `deepseek-v4-pro`, it goes directly to that model regardless of scenario.

## Customize Fallback Chains

Define per-scenario fallback chains:

```json
{
  "fallbacks": {
    "default": [
      { "provider": "opencode-go", "model_id": "mimo-v2.5-pro" },
      { "provider": "opencode-go", "model_id": "qwen3.6-plus" }
    ],
    "complex": [
      { "provider": "opencode-go", "model_id": "glm-5" },
      { "provider": "opencode-go", "model_id": "kimi-k2.6" }
    ],
    "long_context": [
      { "provider": "opencode-go", "model_id": "minimax-m2.7" },
      { "provider": "opencode-go", "model_id": "kimi-k2.6" }
    ]
  }
}
```

If a model in the chain fails (5xx error, timeout), the next model is tried automatically.

## Adjust Context Threshold

The long-context threshold determines when the proxy switches to a 1M-context model:

```json
{
  "models": {
    "long_context": {
      "provider": "opencode-go",
      "model_id": "minimax-m2.5",
      "context_threshold": 80000
    }
  }
}
```

## Enable Streaming Scenario Routing

By default, streaming requests bypass scenario routing and use the `fast` model. Enable scenario-based routing for streaming:

```json
{
  "enable_streaming_scenario_routing": true
}
```

This is useful for multi-agent and review workflows where streaming requests need capability, not just speed.

## Disable Requested Model Routing

By default, the proxy respects the `model` field from Claude Code. Disable this to force scenario routing:

```json
{
  "respect_requested_model": false}
```

## Enable Cost-Based Routing

By default, each scenario maps to a single statically configured primary model. Cost-based routing replaces this with automatic cheapest-model selection using a model pricing catalog:

```json
{
  "cost_routing": {
    "enabled": true
  }
}
```

### Restrict to Preferred Providers

Limit cost-based selection to a subset of providers:

```json
{
  "cost_routing": {
    "enabled": true,
    "prefer_providers": ["opencode-go", "aws-bedrock"]
  }
}
```

When a scenario also has per-scenario `preferred_providers`, the two lists are intersected.

### Cap the Context Window

Exclude models with context windows larger than a threshold:

```json
{
  "cost_routing": {
    "enabled": true,
    "max_context_window": 500000
  }
}
```

Models with a context window exceeding the cap are filtered out. Set to `0` (default) for no limit.

### Penalize Providers

Add an artificial cost penalty to specific providers to bias selection away from them:

```json
{
  "cost_routing": {
    "enabled": true,
    "penalty_per_provider": {
      "openrouter": 0.05,
      "opencode-go": 0.1
    }
  }
}
```

The penalty is added to the raw per-million-token cost during sorting. A model with base cost 2.0 on a provider with a 0.1 penalty has effective cost 2.1.

### Legacy Flag

The top-level `enable_cost_based_routing` flag also enables cost routing:

```json
{
  "enable_cost_based_routing": true
}
```

If both `enable_cost_based_routing` and `cost_routing.enabled` are set, either being `true` activates the feature.

## Custom Scenario Detection

Scenario detection is keyword-based. To add custom patterns, edit `internal/router/scenarios.go`:

- `hasComplexPattern()` — keywords that trigger the `complex` scenario
- `hasThinkingPattern()` — keywords that trigger the `think` scenario
- `hasBackgroundPattern()` — keywords that trigger the `background` scenario
- Vision detection — automatically triggered when the latest user message contains a new image (deduplicated by hash via `imageHashesAreNewForLatest()`, not keyword-based)

## Verify Routing

Check which scenario was selected in the logs:

```
INFO routing request scenario=complex model=glm-5.1 provider=opencode-go tokens=1500
```

Or use the validate command to check config:

```bash
routatic-proxy validate
```
