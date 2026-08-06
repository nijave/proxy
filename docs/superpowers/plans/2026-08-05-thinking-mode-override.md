# thinking_mode Override Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-model `thinking_mode` config field (`auto`/`strip`/`disabled`/`enabled`) that overrides the client's `thinking` parameter so operators can force thinking off (or on) per model/family.

**Architecture:** A new string field on `config.ModelConfig` flows through the existing model-override chain (so it works in `models`, `model_overrides`, and `model_family_overrides`). In `resolveThinkingAndEffort`, an explicit non-auto `thinking_mode` is applied with precedence below only the DeepSeek safety guard and above the client request and conversation history. Enum values are validated at config load.

**Tech Stack:** Go, standard `testing` package, table-driven tests. Build/test via `make test` (runs `go test -race`) and `make lint` (`go vet` + tests).

## Global Constraints

- New field: `ThinkingMode string` with JSON tag `json:"thinking_mode,omitempty"`, on `config.ModelConfig` (`internal/config/config.go`).
- Valid values: `""` (auto), `"auto"`, `"strip"`, `"disabled"`, `"enabled"`. Any other value is a **hard error** at config load.
- Decision precedence inside `resolveThinkingAndEffort` (`internal/transformer/request.go`): **safety guard → `thinking_mode` (non-auto) → client-disabled → client/history/config/default**.
- `thinking_mode` output is gated by the existing `allowThinkingParam`/`allowEffortParam` checks, so models that reject these params are untouched (no-op).
- Fully backwards-compatible: unset / `"auto"` reproduces today's behavior exactly.
- Match the surrounding code's comment style and naming. Do not reformat unrelated code.

---

## File Structure

- **Modify** `internal/config/config.go` — add `ThinkingMode` field + exported constants.
- **Modify** `internal/config/loader.go` — add `validateThinkingMode` + `validateThinkingModes`, wire into `validate`.
- **Modify** `internal/transformer/request.go` — reorder safety guard first; add `thinking_mode` branch in `resolveThinkingAndEffort`.
- **Modify** `internal/config/loader_test.go` — invalid/valid `thinking_mode` load tests.
- **Modify** `internal/transformer/request_test.go` — extend the decision-matrix table test with `thinking_mode` cases.
- **Modify** `configs/config.example.json` — documented `thinking_mode` example.
- **Modify** `cmd/routatic-proxy/templates/default_config.json` — documented `thinking_mode` example.
- **Modify** `CLAUDE.md`, `docs/howto-custom-routing.md`, `docs/reference-api.md` — document the field.

---

### Task 1: Config field, constants, and validation

**Files:**
- Modify: `internal/config/config.go:81-97` (the `ModelConfig` struct)
- Modify: `internal/config/loader.go` (add validators near the other `validate*` funcs; wire near line 369)
- Test: `internal/config/loader_test.go`

**Interfaces:**
- Produces: `config.ThinkingModeAuto`, `config.ThinkingModeStrip`, `config.ThinkingModeDisabled`, `config.ThinkingModeEnabled` (exported string constants), the `ModelConfig.ThinkingMode` field, and a load-time validation error for invalid values. Task 2 consumes `model.ThinkingMode` and the constants.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/loader_test.go` (imports already include `os`, `filepath`, `testing` — confirm and reuse the same env-var setup pattern as `TestLoadJSON_ModelOverrides_InvalidEntryRejected` at line 138):

```go
func TestLoadJSON_InvalidThinkingModeRejected(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfgJSON := `{
		"api_key": "test-key",
		"model_overrides": {
			"deepseek-v4-flash": {
				"provider": "opencode-go",
				"model_id": "deepseek-v4-flash",
				"thinking_mode": "bogus"
			}
		}
	}`

	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_ = os.Setenv("OC_GO_CC_CONFIG", cfgPath)
	defer func() { _ = os.Unsetenv("OC_GO_CC_CONFIG") }()
	oldAPIKey := os.Getenv("OC_GO_CC_API_KEY")
	_ = os.Unsetenv("OC_GO_CC_API_KEY")
	defer func() { _ = os.Setenv("OC_GO_CC_API_KEY", oldAPIKey) }()

	if _, err := Load(); err == nil {
		t.Fatal("expected Load() to fail validation for invalid thinking_mode, got nil")
	}
}

func TestLoadJSON_ValidThinkingModesAccepted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfgJSON := `{
		"api_key": "test-key",
		"model_family_overrides": {
			"haiku": {
				"provider": "opencode-go",
				"model_id": "deepseek-v4-flash",
				"thinking_mode": "disabled"
			},
			"sonnet": {
				"provider": "opencode-go",
				"model_id": "deepseek-v4-flash",
				"thinking_mode": "enabled"
			}
		}
	}`

	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_ = os.Setenv("OC_GO_CC_CONFIG", cfgPath)
	defer func() { _ = os.Unsetenv("OC_GO_CC_CONFIG") }()
	oldAPIKey := os.Getenv("OC_GO_CC_API_KEY")
	_ = os.Unsetenv("OC_GO_CC_API_KEY")
	defer func() { _ = os.Setenv("OC_GO_CC_API_KEY", oldAPIKey) }()

	if _, err := Load(); err != nil {
		t.Fatalf("expected Load() to accept valid thinking_mode values, got: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadJSON_InvalidThinkingModeRejected -race -v`
Expected: the invalid test FAILS (Load returns nil — no validation yet), and the valid test PASSES (field is already unmarshalled, it's just not validated).

- [ ] **Step 3: Add the field, constants, and validators**

In `internal/config/config.go`, add the field to `ModelConfig` immediately after the `Thinking` field (line 94):

```go
	ReasoningEffort        string          `json:"reasoning_effort"`
	Thinking               json.RawMessage `json:"thinking,omitempty"`
	ThinkingMode           string          `json:"thinking_mode,omitempty"`
```

Add the constants immediately after the `ModelConfig` struct closing brace (after line 97):

```go
// ThinkingMode values for ModelConfig.ThinkingMode. An empty string behaves
// identically to ThinkingModeAuto (today's client-wins behavior).
const (
	ThinkingModeAuto     = "auto"
	ThinkingModeStrip    = "strip"
	ThinkingModeDisabled = "disabled"
	ThinkingModeEnabled  = "enabled"
)
```

In `internal/config/loader.go`, add these two functions near the other `validate*` functions (e.g. just before `validateAnthropicToolsDisabled` at line 395):

```go
// validateThinkingMode checks that a thinking_mode value is one of the allowed
// enum values. An empty string is valid and means "auto".
func validateThinkingMode(mode string) error {
	switch mode {
	case "", ThinkingModeAuto, ThinkingModeStrip, ThinkingModeDisabled, ThinkingModeEnabled:
		return nil
	default:
		return fmt.Errorf("invalid thinking_mode %q (must be %q, %q, %q, or %q)",
			mode, ThinkingModeAuto, ThinkingModeStrip, ThinkingModeDisabled, ThinkingModeEnabled)
	}
}

// validateThinkingModes checks thinking_mode on every model config across the
// models, model_overrides, and model_family_overrides maps.
func validateThinkingModes(cfg *Config) error {
	for key, mc := range cfg.Models {
		if err := validateThinkingMode(mc.ThinkingMode); err != nil {
			return fmt.Errorf("models[%q]: %w", key, err)
		}
	}
	for key, mc := range cfg.ModelOverrides {
		if err := validateThinkingMode(mc.ThinkingMode); err != nil {
			return fmt.Errorf("model_overrides[%q]: %w", key, err)
		}
	}
	for key, mc := range cfg.ModelFamilyOverrides {
		if err := validateThinkingMode(mc.ThinkingMode); err != nil {
			return fmt.Errorf("model_family_overrides[%q]: %w", key, err)
		}
	}
	return nil
}
```

Wire it into the aggregate validator. In the `validate` function (the one containing the `if err := validateModelOverrides(...)` block around line 361), add immediately after the `validateAnthropicToolsDisabled` call (line 369-371):

```go
	if err := validateAnthropicToolsDisabled(cfg); err != nil {
		return err
	}

	if err := validateThinkingModes(cfg); err != nil {
		return err
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -race -v`
Expected: both new tests PASS and no existing config tests regress.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/loader.go internal/config/loader_test.go
git commit -m "feat(config): add thinking_mode field with enum validation"
```

---

### Task 2: Transformer `thinking_mode` override

**Files:**
- Modify: `internal/transformer/request.go:175-296` (`resolveThinkingAndEffort` and its doc comment)
- Test: `internal/transformer/request_test.go` (`TestTransformRequestThinkingDecisionMatrix` at line 414)

**Interfaces:**
- Consumes: `config.ModelConfig.ThinkingMode` and the `config.ThinkingMode*` constants from Task 1.
- Produces: no new exported symbols. Behavior change only — `resolveThinkingAndEffort` now honors `thinking_mode`.

- [ ] **Step 1: Write the failing tests**

In `internal/transformer/request_test.go`, add these cases to the `tests` slice inside `TestTransformRequestThinkingDecisionMatrix` (insert them immediately before the closing `}` of the slice, right before the `for _, tt := range tests {` loop at line 513). The struct fields (`name`, `messages`, `thinking`, `model`, `wantThink`, `wantEffort`) and the helper slices (`userOnly`, `plainAssistantHistory`, `thinkingHistory`) already exist at the top of that test:

```go
		{
			name:      "thinking_mode disabled wins over client enabled request",
			messages:  userOnly,
			thinking:  json.RawMessage(`{"type":"enabled","budget_tokens":4096}`),
			model:     config.ModelConfig{ModelID: "deepseek-v4-pro", ThinkingMode: config.ThinkingModeDisabled},
			wantThink: `{"type":"disabled"}`,
		},
		{
			name:      "thinking_mode enabled wins over client disabled and defaults effort to high",
			messages:  userOnly,
			thinking:  json.RawMessage(`{"type":"disabled"}`),
			model:     config.ModelConfig{ModelID: "deepseek-v4-pro", ThinkingMode: config.ThinkingModeEnabled},
			wantThink: `{"type":"enabled"}`,
			wantEffort: func() *string {
				s := "high"
				return &s
			}(),
		},
		{
			name:      "thinking_mode strip emits nothing despite client enabled",
			messages:  userOnly,
			thinking:  json.RawMessage(`{"type":"enabled","budget_tokens":4096}`),
			model:     config.ModelConfig{ModelID: "deepseek-v4-pro", ThinkingMode: config.ThinkingModeStrip},
			wantThink: "",
		},
		{
			name:      "thinking_mode disabled wins over thinking history",
			messages:  thinkingHistory,
			model:     config.ModelConfig{ModelID: "deepseek-v4-pro", ThinkingMode: config.ThinkingModeDisabled},
			wantThink: `{"type":"disabled"}`,
		},
		{
			name:      "thinking_mode enabled yields to deepseek history guard",
			messages:  plainAssistantHistory,
			model:     config.ModelConfig{ModelID: "deepseek-v4-pro", ThinkingMode: config.ThinkingModeEnabled, ReasoningEffort: "max"},
			wantThink: `{"type":"disabled"}`,
		},
		{
			name:      "thinking_mode disabled is no-op on non-reasoning model",
			messages:  userOnly,
			thinking:  json.RawMessage(`{"type":"enabled","budget_tokens":2048}`),
			model:     config.ModelConfig{ModelID: "qwen3.6-plus", ThinkingMode: config.ThinkingModeDisabled},
			wantThink: "",
		},
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/transformer/ -run TestTransformRequestThinkingDecisionMatrix -race -v`
Expected: the `disabled`, `enabled`, and `enabled yields to guard` cases FAIL (today the client/config wins, so `thinking_mode` is ignored); `strip` and `disabled wins over thinking history` may also fail. The `no-op on non-reasoning model` case likely already passes.

- [ ] **Step 3: Restructure `resolveThinkingAndEffort`**

In `internal/transformer/request.go`, update the doc comment for `resolveThinkingAndEffort` (lines 175-186) to reflect the new priority. Replace the decision-priority portion of the comment with:

```go
// resolveThinkingAndEffort applies thinking/reasoning_effort to the OpenAI
// request. Decision priority:
//
//  1. Safety guard — DeepSeek + assistant history lacking thinking blocks
//     → disabled (avoids a guaranteed 400). Wins over everything.
//  2. Explicit thinking_mode (non-auto) → exact control, wins over client
//     and history.
//  3. Client request — anthropicReq.Thinking set and not disabled
//     → forward thinking config; map budget_tokens to reasoning_effort.
//  4. History continuity — a prior turn used thinking → keep it enabled.
//  5. Explicit config — model.Thinking set → use it verbatim.
//  6. Config intent — model.ReasoningEffort set without model.Thinking
//     → enable on first turn, disable only when the safety guard fires.
//  7. No config, no history → leave both unset (safety guard for DeepSeek).
```

Then restructure the function body. The current body (lines 212-296) computes the locals, then has a `requestThinkingDisabled` return, then the DeepSeek safety-guard return, then the `switch`. Reorder so the **safety guard runs first**, then a **`thinking_mode` branch**, then the existing `requestThinkingDisabled` return and `switch`. Replace the two early-return blocks (lines 229-241, the `requestThinkingDisabled` block followed by the safety-guard block) with this ordering:

```go
	// 1. Safety guard: DeepSeek rejects thinking mode when assistant history
	//    lacks reasoning_content, so this wins over everything — including an
	//    explicit thinking_mode — to avoid a guaranteed 400.
	if isDeepSeek && hasAssistant && !hasThinking {
		if allowThinkingParam {
			openaiReq.Thinking = json.RawMessage(`{"type":"disabled"}`)
		}
		return
	}

	// 2. Explicit thinking_mode overrides the client request and conversation
	//    history. Gated by allowThinkingParam/allowEffortParam so models that
	//    reject these fields are left untouched (no-op).
	if model.ThinkingMode != "" && model.ThinkingMode != config.ThinkingModeAuto {
		switch model.ThinkingMode {
		case config.ThinkingModeStrip:
			// Emit neither param; accept the upstream default.
		case config.ThinkingModeDisabled:
			if allowThinkingParam {
				openaiReq.Thinking = json.RawMessage(`{"type":"disabled"}`)
			}
		case config.ThinkingModeEnabled:
			if allowThinkingParam {
				openaiReq.Thinking = json.RawMessage(`{"type":"enabled"}`)
			}
			if allowEffortParam {
				setReasoningEffort(openaiReq, model.ReasoningEffort)
			}
		}
		return
	}

	// 3. Client explicitly disabled thinking → forward it.
	if requestThinkingDisabled {
		if allowThinkingParam {
			openaiReq.Thinking = anthropicReq.Thinking
		}
		return
	}
```

Leave the trailing `switch { ... }` (the `requestThinking` / `hasThinking` / `explicitThinking` / `explicitEffort` / `default` cases) exactly as it is today. The locals (`hasThinking`, `hasAssistant`, `explicitThinking`, `explicitEffort`, `isDeepSeek`, `isOpenAIReasoning`, `requestThinkingDisabled`, `requestThinking`, `allowThinkingParam`, `allowEffortParam`) are already computed above and remain unchanged.

- [ ] **Step 4: Run the full transformer test suite**

Run: `go test ./internal/transformer/ -race -v`
Expected: all new matrix cases PASS and every existing test (round-trip reasoning, history guard, first-turn effort, request-disabled, first-turn defaults) still PASSES — the reorder is behavior-preserving for `thinking_mode` unset.

- [ ] **Step 5: Commit**

```bash
git add internal/transformer/request.go internal/transformer/request_test.go
git commit -m "feat(transformer): honor thinking_mode override in resolveThinkingAndEffort"
```

---

### Task 3: Example config

**Files:**
- Modify: `configs/config.example.json:46-60` (the `model_overrides` DeepSeek entries)
- Modify: `cmd/routatic-proxy/templates/default_config.json:289-294` (the `deepseek-v4-flash-free` override entry)

**Note:** The embedded runtime default (`cmd/routatic-proxy/main.go` `//go:embed n/n.json`) is intentionally left untouched — `thinking_mode` is opt-in (empty default), so the runtime default needs no change. The two human-readable reference files below get the example.

- [ ] **Step 1: Add a documented example to `configs/config.example.json`**

In `configs/config.example.json`, the `model_overrides` block contains a `deepseek-v4-flash` entry (around line 56). Read the file, then add `"thinking_mode": "disabled"` as the last field of that entry. Keep the existing `deepseek-v4-pro` entry (which demonstrates `reasoning_effort` + `thinking: enabled`) unchanged as the force-on contrast. The flash entry should read (preserve its existing field order and values, adding only the one line):

```json
    "deepseek-v4-flash": {
      "provider": "opencode-go",
      "model_id": "deepseek-v4-flash",
      "temperature": 0.7,
      "max_tokens": 4096,
      "thinking_mode": "disabled"
    },
```

- [ ] **Step 2: Add the same example to the readable template**

In `cmd/routatic-proxy/templates/default_config.json`, the `model_overrides` block contains a `deepseek-v4-flash-free` entry (around line 289). Read the file, then add `"thinking_mode": "disabled"` as the last field of that entry, matching the surrounding two-space indentation:

```json
    "deepseek-v4-flash-free": {
      "provider": "opencode-zen",
      "model_id": "deepseek-v4-flash-free",
      "temperature": 0.7,
      "max_tokens": 4096,
      "thinking_mode": "disabled"
    },
```

- [ ] **Step 3: Verify build and config tests still pass**

Run: `make build && go test ./internal/config/ -race`
Expected: build succeeds; config tests pass (the example/template files are not loaded by tests, but this confirms no incidental breakage).

- [ ] **Step 4: Commit**

```bash
git add configs/config.example.json cmd/routatic-proxy/templates/default_config.json
git commit -m "docs(config): add thinking_mode example to reference configs"
```

---

### Task 4: Documentation

**Files:**
- Modify: `CLAUDE.md` (near the `anthropic_tools_disabled` note, line 39)
- Modify: `docs/howto-custom-routing.md` (near the `model_family_overrides` section, line 67)
- Modify: `docs/reference-api.md` (near the override-routing description, line 33-34)

- [ ] **Step 1: CLAUDE.md**

After the `anthropic_tools_disabled` paragraph (line 39), add a parallel paragraph:

```markdown
If you need to force a specific thinking state regardless of what the client requests (e.g. disable DeepSeek's thinking-on default on the `haiku` family while leaving `sonnet` enabled), set `"thinking_mode"` on the model config — one of `"auto"` (default, client wins), `"strip"` (send no thinking param), `"disabled"` (send `{"type":"disabled"}`), or `"enabled"` (send `{"type":"enabled"}`). It works in `models`, `model_overrides`, and `model_family_overrides`, and wins over the client's `thinking` field.
```

- [ ] **Step 2: docs/howto-custom-routing.md**

In the `model_family_overrides` section (after the precedence list ending around line 84), add a short subsection:

```markdown
### Forcing thinking off per family

Some upstreams default to thinking mode (notably DeepSeek V4 Flash). To turn it off for one Claude family while leaving another on, set `thinking_mode` on the family entry:

```json
"model_family_overrides": {
  "sonnet": { "provider": "opencode-go", "model_id": "deepseek-v4-flash", "thinking_mode": "enabled" },
  "haiku":  { "provider": "opencode-go", "model_id": "deepseek-v4-flash", "thinking_mode": "disabled" }
}
```

`thinking_mode` accepts `"auto"` (default — the client's `thinking` field wins), `"strip"` (send no thinking param), `"disabled"` (send `{"type":"disabled"}`), or `"enabled"` (send `{"type":"enabled"}`). On DeepSeek, prefer `"disabled"` over `"strip"`: `"strip"` sends nothing, so DeepSeek falls back to its thinking-on default. The field also works in `models` and `model_overrides`.
```

- [ ] **Step 3: docs/reference-api.md**

Near the override-routing description (around line 33-34), add one line documenting the field:

```markdown
- `thinking_mode` (`auto` | `strip` | `disabled` | `enabled`, optional) — forces the thinking state sent upstream, overriding the client's `thinking` field. Available on every model config (`models`, `model_overrides`, `model_family_overrides`).
```

- [ ] **Step 4: Verify lint and full test suite**

Run: `make lint`
Expected: `go vet` clean and all tests pass.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md docs/howto-custom-routing.md docs/reference-api.md
git commit -m "docs: document thinking_mode override"
```

---

## Verification (final)

- [ ] `make test` — entire suite green with the race detector.
- [ ] `make lint` — `go vet` clean.
- [ ] `make build` — binary builds.
- [ ] Manual sanity (optional): in a throwaway config, set `thinking_mode: "disabled"` on a DeepSeek family override and confirm the normalized upstream request in `~/.config/routatic-proxy/debug/` carries `"thinking":{"type":"disabled"}` even when Claude Code sends `thinking: {type:"enabled"}`.
