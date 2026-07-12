# routatic-proxy

[![Go Version](https://img.shields.io/github/go-mod/go-version/routatic/proxy)](https://go.dev/)
[![License](https://img.shields.io/github/license/routatic/proxy)](./LICENSE)

[Join us on Discord](https://discord.gg/pUrfwfTFxM)

**[English](./README.md)** | [中文](./README-zh.md)

A Go CLI proxy that lets you route [Claude Code](https://docs.anthropic.com/en/docs/claude-code) requests through multiple upstream providers — [OpenCode Go](https://opencode.ai/docs/go/), [OpenCode Zen](https://opencode.ai/docs/zen/), and [AWS Bedrock](https://aws.amazon.com/bedrock/) — with automatic model selection and format transformation.

## Supported Providers

<div align="center">

[![OpenCode Go](https://img.shields.io/badge/OpenCode_Go-00C853?style=for-the-badge&logo=codeforces&logoColor=white)](https://opencode.ai/docs/go/)
[![OpenCode Zen](https://img.shields.io/badge/OpenCode_Zen-7C4DFF?style=for-the-badge&logo=codeforces&logoColor=white)](https://opencode.ai/docs/zen/)
[![AWS Bedrock](https://img.shields.io/badge/AWS_Bedrock-FF9900?style=for-the-badge&logo=amazon-aws&logoColor=white)](https://aws.amazon.com/bedrock/)
[![OpenRouter](https://img.shields.io/badge/OpenRouter-10A37F?style=for-the-badge&logo=openai&logoColor=white)](https://openrouter.ai/)
[![Anthropic](https://img.shields.io/badge/Anthropic-D4A574?style=for-the-badge&logo=anthropic&logoColor=black)](https://www.anthropic.com/)

</div>

| Provider | Description | Best For |
|----------|-------------|----------|
| **OpenCode Go** | High-performance open-source coding models with flat-rate pricing | Daily coding, complex reasoning, cost-effective workloads |
| **OpenCode Zen** | Curated, tested models with pay-as-you-go pricing | Claude/GPT/Gemini access without multiple API keys |
| **AWS Bedrock** | Enterprise-grade models on your own AWS infrastructure | Enterprises needing data sovereignty and compliance |
| **OpenRouter** | Unified API for 100+ LLMs with automatic failover | Experimenting with models from multiple providers |
| **Anthropic** | Native Claude models with anthropic-first failover mode | Claude-first workflows with OpenCode fallback |

---

## Supported Providers

<div align="center">

[![OpenCode Go](https://img.shields.io/badge/OpenCode_Go-00C853?style=for-the-badge&logo=codeforces&logoColor=white)](https://opencode.ai/docs/go/)
[![OpenCode Zen](https://img.shields.io/badge/OpenCode_Zen-7C4DFF?style=for-the-badge&logo=codeforces&logoColor=white)](https://opencode.ai/docs/zen/)
[![AWS Bedrock](https://img.shields.io/badge/AWS_Bedrock-FF9900?style=for-the-badge&logo=amazon-aws&logoColor=white)](https://aws.amazon.com/bedrock/)
[![OpenRouter](https://img.shields.io/badge/OpenRouter-10A37F?style=for-the-badge&logo=openai&logoColor=white)](https://openrouter.ai/)
[![Anthropic](https://img.shields.io/badge/Anthropic-D4A574?style=for-the-badge&logo=anthropic&logoColor=black)](https://www.anthropic.com/)

</div>

| Provider | Description | Best For |
|----------|-------------|----------|
| **OpenCode Go** | High-performance open-source coding models with flat-rate pricing | Daily coding, complex reasoning, cost-effective workloads |
| **OpenCode Zen** | Curated, tested models with pay-as-you-go pricing | Claude/GPT/Gemini access without multiple API keys |
| **AWS Bedrock** | Enterprise-grade models on your own AWS infrastructure | Enterprises needing data sovereignty and compliance |
| **OpenRouter** | Unified API for 100+ LLMs with automatic failover | Experimenting with models from multiple providers |
| **Anthropic** | Native Claude models with anthropic-first failover mode | Claude-first workflows with OpenCode fallback |

**Quick Links:** [Go Models](#supported-models) · [Zen Models](#opencodes-zen) · [OpenRouter Setup](#openrouter-integration) · [Anthropic Mode](#anthropic-first-failover)

`routatic-proxy` sits between Claude Code and your chosen providers, intercepting Anthropic API requests, transforming them to the appropriate format (OpenAI, Anthropic, Responses, or Gemini), and forwarding them upstream. Claude Code thinks it's talking to Anthropic — but your requests go to the models and providers you configure.

`oc-go-cc` remains available as a compatibility alias, and existing `OC_GO_CC_*` environment variables and `~/.config/oc-go-cc/config.json` files are still recognized.

---

## GUI Version

This repository provides a cross-platform GUI for `routatic-proxy` with platform-specific implementations:

### Features

- **System Tray Icon** — Control the proxy server directly from the system tray/menubar (Start, Stop, Autostart, Quit)
- **Interactive Dashboard** — A beautiful dashboard to view real-time request history, model usage metrics, and easily edit/save your configuration without editing JSON files
- **Three Dashboard Tabs:**
  - **Overview** — Real-time metrics showing requests received, streamed, succeeded, and failed; model distribution pie chart; current configuration summary
  - **History** — Last 1000 requests with timestamps, models used, token counts, durations, and status; filter and search capabilities
  - **Settings** — Edit all configuration options through form inputs with validation; hot-reload support (changes apply immediately without restart)
- **App DMG Installer** (macOS) — Package into a standard macOS app with custom icons and launch support

### Platform-Specific Behavior

**macOS:**
- Native window with system tray integration (requires CGO)
- Cocoa-based GUI framework
- Download the `.dmg` from the **Releases** page

**Linux:**
- Browser-based GUI opened via `xdg-open` (default, no CGO required)
- For system tray support, build with `CGO_ENABLED=1` after installing:
  - Fedora/RHEL: `sudo dnf install libappindicator-gtk3-devel`
  - Ubuntu/Debian: `sudo apt install libayatana-appindicator3-dev`

**Windows:**
- GUI is not supported; use CLI only

### How to Run

```bash
# Launch the GUI dashboard
routatic-proxy ui
```

On macOS, this opens a native window. On Linux, it opens your default browser. The GUI connects to the running proxy server automatically.

---

## Why?

OpenCode Go gives you access to powerful open coding models for **$5/month** (then $10/month). OpenCode Zen provides curated, tested models with pay-as-you-go pricing. AWS Bedrock lets you run models on your own AWS infrastructure. This proxy makes all three work seamlessly with Claude Code's interface — no patches, no forks, just set two environment variables and go.

## Features

- **Multi-Provider** — Route through OpenCode Go, OpenCode Zen, or AWS Bedrock from a single config
- **Transparent Proxy** — Claude Code sends Anthropic-format requests, proxy transforms to provider-native format and back
- **Model Routing** — Automatically routes to different models based on context (default, thinking, long context, background)
- **Streaming Scenario Routing** — Configurable routing for streaming requests; enables proper scenario selection for Claude Code multi-agent and review workflows (see [CONFIGURATION.md](CONFIGURATION.md#streaming-scenario-routing))
- **Fallback Chains** — If a model fails, automatically tries the next one in your configured chain
- **Anthropic-First Failover** — Keep Claude on Anthropic and use OpenCode only during rate limits or outages
- **Circuit Breaker** — Tracks model health and skips failing models to avoid latency spikes
- **Real-time Streaming** — Full SSE streaming with live format transformation
- **Tool Calling** — Proper Anthropic tool_use/tool_result <-> OpenAI/Gemini function calling translation
- **Token Counting** — Uses tiktoken (cl100k_base) for accurate token counting and context threshold detection
- **JSON Configuration** — Flexible config file with environment variable overrides and `${VAR}` interpolation
- **Hot Reload** — Watch config file for changes and reload automatically (off by default)
- **Background Mode** — Run as daemon detached from terminal
- **Auto-start on Login** — Launch on system startup via launchd (macOS)
- **Self-Update** — Check and install the latest release with one command

## Supported Models

### OpenCode Go Models

| Model              | Context      | Best For                                      |
| ------------------ | ------------ | --------------------------------------------- |
| **GLM-5.2**        | ~200K tokens | Critical architecture, production code review |
| **Kimi K2.7 Code** | ~256K tokens | Large code generation, 32K max output         |
| **Qwen3.7 Plus**   | ~128K tokens | General coding, better quality than Qwen3.6   |
| **Qwen3.7 Max**    | ~128K tokens | Complex coding, Qwen's best quality           |

See [MODELS.md](MODELS.md) for the complete model list including costs and routing recommendations.

### OpenCode Zen Models

Zen provides pay-as-you-go access to additional models:

- **Claude Models**: Claude Fable 5, Claude Opus 4.8/4.6/4.5/4.1, Claude Sonnet 4
- **Gemini Models**: Gemini 3.5 Flash, Gemini 3.1 Pro, Gemini 3 Flash
- **GPT Models**: GPT 5.5, GPT 5.4, GPT 5.3 Codex, and more
- **Free Tier**: Nemotron 3 Ultra Free, MiMo V2.5 Free, DeepSeek V4 Flash Free, and others

See [MODELS.md](MODELS.md#opencodes-zen) for the full Zen model list.

### OpenRouter Models

[OpenRouter](https://openrouter.ai) is a unified API for 100+ LLMs from OpenAI, Anthropic, Google, Meta, Mistral, and other leading AI providers. It provides a single endpoint for accessing models from multiple vendors without managing separate API keys and integrations for each provider.

#### What is OpenRouter?

OpenRouter acts as a universal gateway to the AI model ecosystem. Instead of maintaining separate accounts and API keys for OpenAI, Anthropic, Google, and dozens of other providers, you use a single OpenRouter API key to access them all. OpenRouter handles the routing, normalization, and billing, giving you:

- **Unified API**: One endpoint, one authentication method for 100+ models
- **Automatic failover**: If a provider is down, requests can route to alternatives
- **Standardized pricing**: Clear per-token costs across all providers
- **Model exploration**: Easily experiment with new models without new integrations
- **OpenAI-compatible format**: Works with existing OpenAI SDKs and tools

#### Getting Started

1. Sign up at [openrouter.ai](https://openrouter.ai)
2. Generate an API key at [https://openrouter.ai/keys](https://openrouter.ai/keys)
3. Set the environment variable:

```bash
export ROUTATIC_PROXY_OPENROUTER_API_KEY=sk-or-v1-your-key-here
```

For key rotation or load balancing across multiple keys, use a comma-separated list:

```bash
export ROUTATIC_PROXY_OPENROUTER_API_KEYS=key-1,key-2,key-3
```

#### Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `ROUTATIC_PROXY_OPENROUTER_API_KEY` | Single OpenRouter API key | `sk-or-v1-...` |
| `ROUTATIC_PROXY_OPENROUTER_API_KEYS` | Comma-separated list for rotation | `key-1,key-2,key-3` |

Precedence: `*_API_KEYS` → `*_API_KEY` → global `API_KEYS` → global `API_KEY`.

#### Configuration Example

Add the `openrouter` provider to your `config.json`:

```json
{
  "providers": {
    "openrouter": {
      "enabled": true,
      "api_key": "${ROUTATIC_PROXY_OPENROUTER_API_KEY}",
      "base_url": "https://openrouter.ai/api/v1"
    }
  },
  "models": {
    "openrouter/openai/gpt-4o": {
      "enabled": true,
      "display_name": "GPT-4o (via OpenRouter)"
    },
    "openrouter/anthropic/claude-3.5-sonnet": {
      "enabled": true,
      "display_name": "Claude 3.5 Sonnet (via OpenRouter)"
    },
    "openrouter/google/gemini-2.0-flash-exp": {
      "enabled": true,
      "display_name": "Gemini 2.0 Flash (via OpenRouter)"
    },
    "openrouter/meta-llama/llama-3.3-70b-instruct": {
      "enabled": true,
      "display_name": "Llama 3.3 70B (via OpenRouter)"
    },
    "openrouter/mistralai/mistral-large": {
      "enabled": true,
      "display_name": "Mistral Large (via OpenRouter)"
    }
  }
}
```

#### Cost-Based Routing with OpenRouter

When using `cost_routing`, you can apply a penalty to OpenRouter requests to account for routing overhead or prefer direct providers when costs are similar:

```json
{
  "cost_routing": {
    "enabled": true,
    "prefer_providers": ["opencode-go", "openrouter"],
    "penalty_per_provider": {
      "openrouter": 0.05
    }
  }
}
```

This adds a small cost penalty (e.g., 5 cents per million tokens) when selecting OpenRouter models, helping the router prefer direct providers when cost is comparable.

#### Model Selection

OpenRouter uses the `provider/model-name` format. Common model slugs:

| Model Key | Provider | Description | Best For |
|-----------|----------|-------------|----------|
| `openai/gpt-4o` | OpenAI | Latest GPT-4o multimodal model | General purpose, vision tasks |
| `openai/o1` | OpenAI | Reasoning model (o1) | Complex reasoning, math, coding |
| `anthropic/claude-3.5-sonnet` | Anthropic | Claude 3.5 Sonnet | Coding, analysis, writing |
| `anthropic/claude-3-opus` | Anthropic | Claude 3 Opus | Most capable Anthropic model |
| `anthropic/claude-3.5-haiku` | Anthropic | Claude 3.5 Haiku | Fast, cost-effective tasks |
| `google/gemini-2.0-flash-exp` | Google | Gemini 2.0 Flash (experimental) | Low latency, high throughput |
| `google/gemini-pro-1.5` | Google | Gemini 1.5 Pro | Long context (up to 2M tokens) |
| `meta-llama/llama-3.3-70b-instruct` | Meta | Llama 3.3 70B | Open source, self-hostable |

See the full catalog at [https://openrouter.ai/models](https://openrouter.ai/models).

#### Official Documentation

- **API Reference**: [https://openrouter.ai/docs](https://openrouter.ai/docs)
- **OpenAI Compatibility**: [https://openrouter.ai/docs#openai-compatibility](https://openrouter.ai/docs#openai-compatibility)
- **Provider Docs**: [https://openrouter.ai/docs#provider-routing](https://openrouter.ai/docs#provider-routing)

### Deprecated Models

The following models are deprecated and will be removed:

- GPT 5.2/5.1/5 Codex variants (replaced by GPT 5.3 Codex)
- Claude Sonnet 4 (replaced by Claude Sonnet 4.5/4.6)
- GLM 5/4.7/4.6 (replaced by GLM 5.1/5.2)
- MiniMax M2.1 (replaced by MiniMax M2.5/M2.7/M3)
- Gemini 3 Pro (replaced by Gemini 3.1 Pro)
- Kimi K2/K2 Thinking (replaced by Kimi K2.5/K2.6/K2.7 Code)

See [MODELS.md](MODELS.md#deprecated-zen-models) for the complete deprecation schedule.

## OpenRouter Integration

[OpenRouter](https://openrouter.ai) is a unified API for 100+ LLMs from OpenAI, Anthropic, Google, Meta, Mistral, and other leading AI providers. It provides a single endpoint for accessing models from multiple vendors without managing separate API keys and integrations for each provider.

### What is OpenRouter?

OpenRouter acts as a universal gateway to the AI model ecosystem. Instead of maintaining separate accounts and API keys for OpenAI, Anthropic, Google, and dozens of other providers, you use a single OpenRouter API key to access them all. OpenRouter handles the routing, normalization, and billing, giving you:

- **Unified API**: One endpoint, one authentication method for 100+ models
- **Automatic failover**: If a provider is down, requests can route to alternatives
- **Standardized pricing**: Clear per-token costs across all providers
- **Model exploration**: Easily experiment with new models without new integrations
- **OpenAI-compatible format**: Works with existing OpenAI SDKs and tools

### Getting Started

1. Sign up at [openrouter.ai](https://openrouter.ai)
2. Generate an API key at [https://openrouter.ai/keys](https://openrouter.ai/keys)
3. Set the environment variable:

```bash
export ROUTATIC_PROXY_OPENROUTER_API_KEY=sk-or-v1-your-key-here
```

For key rotation or load balancing across multiple keys, use a comma-separated list:

```bash
export ROUTATIC_PROXY_OPENROUTER_API_KEYS=key-1,key-2,key-3
```

### Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `ROUTATIC_PROXY_OPENROUTER_API_KEY` | Single OpenRouter API key | `sk-or-v1-...` |
| `ROUTATIC_PROXY_OPENROUTER_API_KEYS` | Comma-separated list for rotation | `key-1,key-2,key-3` |

Precedence: `*_API_KEYS` → `*_API_KEY` → global `API_KEYS` → global `API_KEY`.

### Complete Configuration Example

Add the `openrouter` provider to your `config.json`:

```json
{
  "providers": {
    "openrouter": {
      "enabled": true,
      "api_key": "${ROUTATIC_PROXY_OPENROUTER_API_KEY}",
      "base_url": "https://openrouter.ai/api/v1"
    }
  },
  "models": {
    "openrouter/openai/gpt-4o": {
      "enabled": true,
      "display_name": "GPT-4o (via OpenRouter)"
    },
    "openrouter/anthropic/claude-3.5-sonnet": {
      "enabled": true,
      "display_name": "Claude 3.5 Sonnet (via OpenRouter)"
    },
    "openrouter/anthropic/claude-3-opus": {
      "enabled": true,
      "display_name": "Claude 3 Opus (via OpenRouter)"
    },
    "openrouter/anthropic/claude-3.5-haiku": {
      "enabled": true,
      "display_name": "Claude 3.5 Haiku (via OpenRouter)"
    },
    "openrouter/google/gemini-2.0-flash-exp": {
      "enabled": true,
      "display_name": "Gemini 2.0 Flash (via OpenRouter)"
    },
    "openrouter/google/gemini-pro-1.5": {
      "enabled": true,
      "display_name": "Gemini 1.5 Pro (via OpenRouter)"
    },
    "openrouter/meta-llama/llama-3.3-70b-instruct": {
      "enabled": true,
      "display_name": "Llama 3.3 70B (via OpenRouter)"
    },
    "openrouter/meta-llama/llama-3.1-405b": {
      "enabled": true,
      "display_name": "Llama 3.1 405B (via OpenRouter)"
    },
    "openrouter/mistralai/mistral-large": {
      "enabled": true,
      "display_name": "Mistral Large (via OpenRouter)"
    },
    "openrouter/mistralai/mistral-medium": {
      "enabled": true,
      "display_name": "Mistral Medium (via OpenRouter)"
    },
    "openrouter/mistralai/mistral-small": {
      "enabled": true,
      "display_name": "Mistral Small (via OpenRouter)"
    },
    "openrouter/deepseek/deepseek-chat": {
      "enabled": true,
      "display_name": "DeepSeek V3 (via OpenRouter)"
    },
    "openrouter/perplexity/sonar-reasoning": {
      "enabled": true,
      "display_name": "Perplexity Sonar Reasoning (via OpenRouter)"
    }
  }
}
```

### Cost-Based Routing with OpenRouter

When using `cost_routing`, you can apply a penalty to OpenRouter requests to account for routing overhead or prefer direct providers when costs are similar:

```json
{
  "cost_routing": {
    "enabled": true,
    "prefer_providers": ["opencode-go", "openrouter"],
    "penalty_per_provider": {
      "openrouter": 0.05
    }
  }
}
```

This adds a small cost penalty (e.g., 5 cents per million tokens) when selecting OpenRouter models, helping the router prefer direct providers when cost is comparable.

### Model Selection

OpenRouter uses the `provider/model-name` format. Common model slugs:

| Model Key | Provider | Description | Best For |
|-----------|----------|-------------|----------|
| `openai/gpt-4o` | OpenAI | Latest GPT-4o multimodal model | General purpose, vision tasks |
| `openai/o1` | OpenAI | Reasoning model (o1) | Complex reasoning, math, coding |
| `anthropic/claude-3.5-sonnet` | Anthropic | Claude 3.5 Sonnet | Coding, analysis, writing |
| `anthropic/claude-3-opus` | Anthropic | Claude 3 Opus | Most capable Anthropic model |
| `anthropic/claude-3.5-haiku` | Anthropic | Claude 3.5 Haiku | Fast, cost-effective tasks |
| `google/gemini-2.0-flash-exp` | Google | Gemini 2.0 Flash (experimental) | Low latency, high throughput |
| `google/gemini-pro-1.5` | Google | Gemini 1.5 Pro | Long context (up to 2M tokens) |
| `meta-llama/llama-3.3-70b-instruct` | Meta | Llama 3.3 70B | Open source, self-hostable |
| `meta-llama/llama-3.1-405b` | Meta | Llama 3.1 405B | Largest open source model |
| `mistralai/mistral-large` | Mistral | Mistral Large | Strong multilingual performance |
| `mistralai/mistral-medium` | Mistral | Mistral Medium | Balanced performance/cost |
| `mistralai/mistral-small` | Mistral | Mistral Small | Fast, efficient tasks |
| `deepseek/deepseek-chat` | DeepSeek | DeepSeek V3 | Strong reasoning, coding |
| `perplexity/sonar-reasoning` | Perplexity | Sonar Reasoning | Research, citations |

See the full catalog at [https://openrouter.ai/models](https://openrouter.ai/models).

### Official Documentation

- **API Reference**: [https://openrouter.ai/docs](https://openrouter.ai/docs)
- **OpenAI Compatibility**: [https://openrouter.ai/docs#openai-compatibility](https://openrouter.ai/docs#openai-compatibility)
- **Provider Routing**: [https://openrouter.ai/docs#provider-routing](https://openrouter.ai/docs#provider-routing)
- **Models Catalog**: [https://openrouter.ai/models](https://openrouter.ai/models)

## Quick Start

### 1. Install

```bash
# macOS / Linux
brew tap routatic/tap && brew install routatic-proxy

# Windows
scoop bucket add routatic https://github.com/routatic/scoop-bucket && scoop install routatic-proxy

# Docker (with Makefile)
cp .env.example .env                    # then put your API key in .env
make docker-up

# Docker (manual)
cp .env.example .env
docker build -t routatic-proxy .
docker run -d --restart unless-stopped --name routatic-proxy --env-file .env -p 3456:3456 routatic-proxy

# Docker from GitHub Container Registry
docker pull ghcr.io/routatic/proxy:latest
docker run -d --restart unless-stopped --name routatic-proxy --env-file .env -p 3456:3456 ghcr.io/routatic/proxy:latest
```

Or see [INSTALLATION.md](INSTALLATION.md) for more options.

### 2. Initialize Configuration

```bash
routatic-proxy init
```

Creates a default config at `~/.config/routatic-proxy/config.json`. Edit it to add your API key, or set the environment variable:

```bash
export ROUTATIC_PROXY_API_KEY=sk-opencode-your-key-here
```

### 3. Start the Proxy

```bash
routatic-proxy serve
```

Stop the Docker container (if using Docker):

```bash
make docker-stop
```

### 4. Configure Claude Code

For the default OpenCode-only mode:

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:3456
export ANTHROPIC_AUTH_TOKEN=unused
```

For Anthropic-first mode, enable `anthropic_first` in the proxy config and set only the base URL. Do not set an API key or auth token: Claude Code will keep using its saved Claude subscription login.

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:3456
unset ANTHROPIC_AUTH_TOKEN ANTHROPIC_API_KEY
```

Anthropic-first mode falls back on HTTP 408, 429, 5xx, and connection failures. It honors `Retry-After` and uses one real request to detect recovery, so it does not spend tokens on health checks. See [CONFIGURATION.md](CONFIGURATION.md#anthropic-first-failover).

### 5. Run Claude Code

```bash
claude
```

## CLI Commands

```
routatic-proxy serve              Start the proxy server
routatic-proxy serve -b           Start in background (detached from terminal)
routatic-proxy serve --port 8080  Start on a custom port
routatic-proxy stop               Stop the running proxy server
routatic-proxy status             Check if the proxy is running
routatic-proxy init               Create default configuration file
routatic-proxy validate           Validate configuration file
routatic-proxy models             List all available models (Go, Zen, Bedrock)
routatic-proxy ui                 Launch the GUI dashboard (native on macOS, browser on Linux)
routatic-proxy autostart enable   Enable auto-start on login
routatic-proxy autostart disable  Disable auto-start on login
routatic-proxy autostart status   Check autostart status
routatic-proxy update              Update to the latest release
routatic-proxy update --check      Show if an update is available
routatic-proxy update --yes        Update without prompting
routatic-proxy --version          Show version
```

## Documentation

| Document                                                     | Description                                                     |
| ------------------------------------------------------------ | --------------------------------------------------------------- |
| [INSTALLATION.md](INSTALLATION.md)                           | Homebrew, Scoop, build from source, release binaries            |
| [CONFIGURATION.md](CONFIGURATION.md)                         | Config file reference, env vars, model routing, fallback chains |
| [MODELS.md](MODELS.md)                                       | Model capabilities, costs, and routing recommendations          |
| [CONTRIBUTING.md](CONTRIBUTING.md)                           | Development setup, architecture, how it works                   |
| [TROUBLESHOOTING.md](TROUBLESHOOTING.md)                     | Common issues and debug mode                                    |
| [docs/fedora-setup.md](docs/fedora-setup.md)                 | Fedora 44 setup guide (installation, systemd, SELinux)          |
| [docs/architecture.md](docs/architecture.md)                 | System design, request flow, module overview                    |
| [docs/reference-api.md](docs/reference-api.md)               | HTTP API reference (endpoints, streaming, errors)               |
| [docs/howto-add-model.md](docs/howto-add-model.md)           | Adding new models (zero code changes)                           |
| [docs/howto-custom-routing.md](docs/howto-custom-routing.md) | Customizing scenario detection and model selection              |
| [docs/howto-debug-routing.md](docs/howto-debug-routing.md)   | Debugging routing issues and common problems                    |

## Release Channels

This project uses a dual release channel system:

### Beta Channel (Automatic)

- **Trigger:** Every push to `main` branch
- **Version format:** `vX.Y.Z-beta-YYYYMMDD-HHMMSS`
- **Example:** `v1.2.3-beta-20260712-143022`
- **GitHub release:** Marked as prerelease
- **Docker tags:** `vX.Y.Z-beta-YYYYMMDD-HHMMSS` and `beta-X.Y.Z`
- **Use case:** Get the latest features and bug fixes immediately; ideal for testing and early adoption

Beta releases are automatically created when code is merged to `main`. They include all binaries, checksums, and macOS DMG.

### Production Channel (Manual)

- **Trigger:** Manual workflow_dispatch on `releases` branch
- **Version format:** `vX.Y.Z` (semantic versioning)
- **Example:** `v1.2.3`
- **GitHub release:** Marked as stable (not prerelease)
- **Docker tags:** `vX.Y.Z`, `vX.Y`, `vX`, `latest`
- **Use case:** Stable, tested releases for production use

To create a production release:

1. Ensure all changes are merged to `main` and tested via beta
2. Create or update the `releases` branch from `main`
3. Go to Actions → Release workflow
4. Click "Run workflow" and specify the version (e.g., `v1.2.3`)
5. The workflow will:
   - Run full test suite
   - Build cross-platform binaries
   - Generate AI-powered changelog
   - Create GitHub release
   - Publish Docker images
   - Update Homebrew tap and Scoop bucket

### Version Detection

The beta release workflow uses `.github/scripts/get-versions.sh` to:
- Detect the latest production version from the `releases` branch
- Generate a unique beta version with UTC timestamp
- Output both versions in JSON format for the CI workflow

### Upgrading

Beta releases can be upgraded to production releases simply by running the production release workflow with the appropriate version number. The beta tag remains in history for reference.

## Contributing

We welcome contributions! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, architecture overview, and how to submit pull requests.

## License

[AGPL-3.0](LICENSE)
