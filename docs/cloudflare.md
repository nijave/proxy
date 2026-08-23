# Cloudflare Workers AI Provider

[Cloudflare Workers AI](https://developers.cloudflare.com/workers-ai/) runs serverless inference on Cloudflare's global network. It exposes an [OpenAI-compatible chat completions endpoint](https://developers.cloudflare.com/workers-ai/configuration/open-ai-compatibility/), so the proxy talks to it through the standard Chat Completions transform path — no Anthropic-format passthrough.

## Overview

Workers AI hosts a growing catalog of open models (Llama, Kimi, Qwen, and more) billed per token. Because inference runs at the edge, latency is low and there are no rate-plan subscriptions: you pay as you go from your Cloudflare account. The proxy routes to it like any other OpenAI-compatible provider — set `"provider": "cloudflare"` on a model and the router handles the rest.

## Getting Started

### 1. Create an API Token

1. Log in to the [Cloudflare dashboard](https://dash.cloudflare.com)
2. Go to **My Profile → API Tokens → Create Token**
3. Give the token the **Account → Workers AI → Edit** permission
4. Copy the generated token

### 2. Copy Your Account ID

Your Account ID is shown on the right side of any domain's **Overview** page in the Cloudflare dashboard (or under **Workers & Pages**). The proxy needs it to build the endpoint URL:

```
https://api.cloudflare.com/client/v4/accounts/{account_id}/ai/v1/chat/completions
```

### 3. Set Environment Variables

```bash
export ROUTATIC_PROXY_CLOUDFLARE_API_KEY=cf-token-here
export ROUTATIC_PROXY_CLOUDFLARE_ACCOUNT_ID=your-account-id
```

For key rotation or load balancing across multiple tokens:

```bash
export ROUTATIC_PROXY_CLOUDFLARE_API_KEYS=token-1,token-2
```

To point the provider at a different endpoint entirely (for example an AI Gateway URL), override the full chat-completions URL:

```bash
export ROUTATIC_PROXY_CLOUDFLARE_BASE_URL=https://gateway.ai.cloudflare.com/v1/{account_id}/{gateway_id}/workers-ai/v1/chat/completions
```

## Configuration

Generate a ready-made config with:

```bash
routatic-proxy init --provider cloudflare
```

Or add the `cloudflare` block and models to your `~/.config/routatic-proxy/config.json` by hand:

```json
{
  "cloudflare": {
    "account_id": "${ROUTATIC_PROXY_CLOUDFLARE_ACCOUNT_ID}",
    "gateway_id": "",
    "api_key": "${ROUTATIC_PROXY_CLOUDFLARE_API_KEY}",
    "timeout_ms": 300000,
    "stream_timeout_ms": 60000,
    "streaming_timeout_ms": 600000
  },
  "models": {
    "cf-default": {
      "provider": "cloudflare",
      "model_id": "@cf/meta/llama-3.3-70b-instruct-fp8",
      "temperature": 0.7,
      "max_tokens": 8192
    }
  }
}
```

### Configuration Schema

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `account_id` | `string` | Yes* | Cloudflare Account ID used to build the endpoint. Required unless `base_url` is set |
| `base_url` | `string` | No | Full chat-completions URL override. When set, it replaces the account-derived endpoint entirely |
| `gateway_id` | `string` | No | AI Gateway ID for caching, logging, and analytics (see below) |
| `api_key` | `string` | Yes* | Single Workers AI API token. Required if `api_keys` not set |
| `api_keys` | `string[]` | Yes* | Multiple tokens for round-robin rotation. Required if `api_key` not set |
| `timeout_ms` | `int` | No | Request timeout in milliseconds. Default: `300000` (5 minutes) |
| `stream_timeout_ms` | `int` | No | Per-chunk timeout during streaming. Falls back to `timeout_ms` when unset; default here: `60000` (1 minute) |
| `streaming_timeout_ms` | `int` | No | Total timeout for long-running streams. Default: `600000` (10 minutes) |

*At least one of `api_key`/`api_keys` must be configured; `account_id` is required unless `base_url` is set.

### Environment Variables

Precedence order: `*_API_KEYS` → `*_API_KEY` → config file values. Environment variables override config file values; config values also support `${VAR}` interpolation.

## Model Naming

Workers AI model IDs include the task prefix (`@cf/`, `@hf/`, ...) and are used verbatim as `model_id`. There is no extra prefixing on top:

```json
{ "provider": "cloudflare", "model_id": "@cf/meta/llama-3.1-8b-instruct" }
```

Discover available IDs via the models list API or the model catalog pages:

- REST: `GET https://api.cloudflare.com/client/v4/accounts/{account_id}/ai/models`
- Docs: [developers.cloudflare.com/workers-ai/models/](https://developers.cloudflare.com/workers-ai/models/)

## Catalog and Cost Routing

With `cost_routing.enabled`, the `Selector` can pick Cloudflare models automatically from the catalog. Catalog keys follow the `provider/model-name` pattern, so a Workers AI entry looks like:

```
cloudflare/@cf/meta/llama-3.1-8b-instruct
```

Catalog refs use the same shape, e.g. `@cf/meta/llama@cloudflare`.

> **Note:** parsing keys with multiple `/` segments requires the catalog parser fix from PR #1. Older builds may mis-resolve deeply nested model names like `cloudflare/@cf/moonshotai/kimi-k2.6`.

## Thinking Mode Guidance

Leave `thinking_mode` unset (or set it to `"strip"`) on Cloudflare models. Setting `"thinking_mode": "enabled"` makes the proxy emit a `reasoning_effort` parameter that the Workers AI OpenAI-compatibility layer may reject outright. If you need reasoning behavior, prefer models that think by default rather than forcing it through config.

## Tool Calling and Vision

Tool calling and image input are supported per model — each Workers AI model declares what it accepts on its own docs page. Check the model's page before enabling either:

- For tool support, verify the model advertises function/tool calling.
- For vision, set `"vision": true` on the model entry only after confirming the model accepts image inputs; otherwise requests with images will fail upstream.

## AI Gateway

Set `gateway_id` to route requests through [AI Gateway](https://developers.cloudflare.com/ai-gateway/) for caching, retries, rate limiting, and logging:

```json
{
  "cloudflare": {
    "account_id": "${ROUTATIC_PROXY_CLOUDFLARE_ACCOUNT_ID}",
    "gateway_id": "my-gateway",
    "api_key": "${ROUTATIC_PROXY_CLOUDFLARE_API_KEY}"
  }
}
```

Request logs, cache hit rates, and cost analytics appear in the Cloudflare dashboard under **AI Gateway**, where you can inspect every proxied request.

## Fallback Chains

Cloudflare mixes cleanly with other providers in fallback chains — keep a fast Workers AI model primary and fall back to OpenCode Go when it fails:

```json
{
  "fallbacks": {
    "default": [
      { "provider": "cloudflare", "model_id": "@cf/meta/llama-3.3-70b-instruct-fp8" },
      { "provider": "opencode-go", "model_id": "kimi-k2.6" },
      { "provider": "opencode-go", "model_id": "qwen3.7-plus" }
    ]
  }
}
```

The circuit breaker skips a model after repeated failures, so a degraded Workers AI region fails over to OpenCode Go without client-visible errors.
