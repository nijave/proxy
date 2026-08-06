# Per-model thinking override (`thinking_mode`)

## Problem

`resolveThinkingAndEffort` (`internal/transformer/request.go`) lets the **client's**
`thinking` field win over config: the `requestThinking` case is first in its
decision switch, ahead of explicit config. So when Claude Code sends
`thinking: {"type":"enabled", ...}` — which it does on reasoning turns — an
operator cannot turn thinking off for that model, even by setting `thinking` or
`reasoning_effort` in config.

This bites the DeepSeek V4 Flash 0731 case: thinking is ON by default, reasoning
tokens are billed at the output rate, and thinking mode is the source of
cost/latency/timeouts. Today there is **no** way to force-disable or strip
thinking per model or per Claude family (e.g. disable on the `haiku` mapping
while leaving `sonnet` enabled, when both map to `deepseek-v4-flash`).

## Solution

Add a per-model `thinking_mode` field to `ModelConfig`. It takes precedence over
the client request and conversation history, and lets the operator choose the
exact thinking state sent upstream. Because it lives on `ModelConfig`, it is
available everywhere model configs are accepted — `models`, `model_overrides`,
and `model_family_overrides` — giving per-model and per-family granularity for
free (the sonnet/haiku case).

```json
"model_family_overrides": {
  "sonnet": { "provider": "opencode-go", "model_id": "deepseek-v4-flash", "thinking_mode": "enabled" },
  "haiku":  { "provider": "opencode-go", "model_id": "deepseek-v4-flash", "thinking_mode": "disabled" }
}
```

### Values and wire behavior

`thinking_mode` controls the `thinking` param (DeepSeek/Anthropic style) and,
where applicable, `reasoning_effort`. Output is gated by the existing
`allowThinkingParam` / `allowEffortParam` checks, so models that reject these
params (plain GLM/Kimi/Qwen) are never sent them — the field is a harmless no-op
there.

| `thinking_mode`        | `thinking` sent                  | `reasoning_effort` sent        | precedence vs client/history |
|------------------------|----------------------------------|--------------------------------|------------------------------|
| unset / `"auto"`       | today's logic                    | today's logic                  | unchanged (backwards-compatible) |
| `"strip"`              | none                             | none                           | wins                         |
| `"disabled"`           | `{"type":"disabled"}`            | none                           | wins                         |
| `"enabled"`            | `{"type":"enabled"}`             | from `reasoning_effort` (default `"high"`) | wins            |

Notes / footguns (to be documented for users):

- Primary target is DeepSeek. On DeepSeek, `"disabled"` is what actually turns
  the thinking-ON default off. `"strip"` sends nothing, so DeepSeek falls back
  to its **thinking-ON default** — `"strip"` is for models whose off-state is
  "no param" or when the operator wants the upstream default. The two are
  different and both are intentional choices.
- A non-auto `thinking_mode` also wins over the existing raw `thinking` field,
  so the two cannot contradict each other.
- On OpenAI reasoning models (o1/o3) there is no `thinking` param; there the
  field only affects the `reasoning_effort` knob (`"disabled"`/`"strip"`
  suppress it, `"enabled"` keeps it).

## Precedence

Inside `resolveThinkingAndEffort`, the order becomes:

1. **Safety guard** — DeepSeek + assistant history lacking thinking blocks →
   `disabled` (prevents a guaranteed 400). Stays highest.
2. **`thinking_mode`** (non-auto) — exact control, wins over client and history.
3. Client request → history continuity → explicit config → default (today's order).

The only conflict between (1) and (2) is an explicit `"enabled"` or `"strip"`
on a DeepSeek model whose assistant history lacks thinking blocks; there the
guard forces `disabled` to avoid a guaranteed 400. The common case
(`"disabled"`) never conflicts.

## Changes

### `internal/config/config.go`
- Add `ThinkingMode string \`json:"thinking_mode,omitempty"\`` to `ModelConfig`.
- Add constants for the valid values (`auto`, `strip`, `disabled`, `enabled`).

### `internal/config/loader.go`
- Add a `validateThinkingMode(string) error` helper (accepts `` / `auto` /
  `strip` / `disabled` / `enabled`).
- Call it from the `models` validation loop (around the
  `anthropic_tools_disabled` warning at line ~400) and from
  `validateOverrideMap` so all three maps (`models`, `model_overrides`,
  `model_family_overrides`) reject invalid values at load time.

### `internal/transformer/request.go`
- Restructure `resolveThinkingAndEffort` so the evaluation order matches the
  precedence above. Today the client-disabled block (`requestThinkingDisabled`)
  runs *before* the DeepSeek safety guard; reorder so the **safety guard runs
  first**, then the new `thinking_mode` branch, then the existing
  client/history/config logic. (Moving the guard ahead of the client-disabled
  block is safe: when the guard fires the outcome is `disabled` regardless, and
  when it does not fire the client-disabled block behaves exactly as before.)
- Add the `thinking_mode` branch: when `model.ThinkingMode` is non-auto, set
  `openaiReq.Thinking` (and `reasoning_effort` per the table above) and `return`,
  subject to `allowThinkingParam` / `allowEffortParam`. Reuse
  `setReasoningEffort` for the `enabled` case; `"strip"` emits neither param.
- Update the doc comment that lists the decision priority.

### `cmd/routatic-proxy/templates/default_config.json`
- Add `"thinking_mode"` to one documented DeepSeek example entry (alongside the
  existing `reasoning_effort` / `thinking` example) with a comment-style value
  showing `disabled`.

### `configs/config.example.json`
- Add a documented `thinking_mode` example in a model/family-override entry.

### Documentation
- `CLAUDE.md` — mention `thinking_mode` in the model-config / overrides section.
- `docs/reference-api.md` — add `thinking_mode` to the config schema reference.
- `docs/howto-custom-routing.md` — short note on forcing thinking off per family.

## Out of scope
- Global `thinking_mode` toggle (per-model covers the need; global can follow).
- Custom-JSON override values (enum covers the realistic targets).
- GUI editing of model overrides (the Settings tab only reads override IDs today
  and does not render per-field override editors, so this stays config-only).

## Testing

`internal/transformer/request_test.go`:
- `"auto"` / unset reproduces today's behavior (client `thinking` wins).
- `"disabled"` emits `{"type":"disabled"}` and no `reasoning_effort`, even when
  the client sends `thinking: enabled`.
- `"enabled"` emits `{"type":"enabled"}` and `reasoning_effort` (default `high`
  when unset), even when the client sends `thinking: disabled`.
- `"strip"` emits neither param, regardless of client/history.
- Non-auto mode wins over conversation history with thinking blocks.
- Safety guard still wins: explicit `"enabled"` on DeepSeek with assistant
  history lacking thinking blocks → `disabled` (no 400).
- `"disabled"` on a non-DeepSeek, non-reasoning model is a no-op (no params
  emitted beyond today's behavior).

`internal/config` loader test:
- Invalid `thinking_mode` value is rejected for `models`,
  `model_overrides`, and `model_family_overrides`.
- Valid values (including empty) load cleanly.
