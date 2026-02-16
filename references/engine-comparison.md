# Engine Comparison Reference

Detailed engine capabilities, configuration modes, and operational parameters.

---

## Engine Table

| Aspect | Codex | Claude | OpenCode |
| --- | --- | --- | --- |
| SDK | `@openai/codex` | `@anthropic-ai/claude-code` | `opencode` CLI |
| Default model | `gpt-5.3-codex` | `claude-opus-4-6` | Varies by preset |
| Model variants | `gpt-5.3-codex`, `gpt-5.3-codex-spark` | `claude-opus-4-6` | `kimi`, `glm-5`, `opencode-minimax`, `deepseek-r1`, `free` |
| Speed | ~65-70 tok/s (Spark: 1000+ tok/s) | ~65-70 tok/s | Varies |
| Context window | Standard (Spark: 128K) | 1M (beta) | 200-262K |
| Best for | Implementation, debugging, code changes | Architecture, reasoning, synthesis, writing | Third opinion, model diversity, cost control |
| SWE-Bench Pro | 56.8% (Spark: 56%) | N/A | 80% (minimax) |
| Terminal-Bench | 77.3% (Spark: 58.4%) | N/A | N/A |

---

## Timeout / Effort Mapping

| Effort | Timeout (ms) | Timeout (human) | Guidance |
| --- | ---: | --- | --- |
| `low` | `120000` | 2 min | Quick checks, trivial fixes |
| `medium` | `600000` | 10 min | Routine tasks (default) |
| `high` | `1200000` | 20 min | Workhorse for implementation |
| `xhigh` | `2400000` | 40 min | Deep analysis only |

---

## Codex Sandbox Modes

| Mode | Access | When to use |
| --- | --- | --- |
| `read-only` | Read filesystem, no writes | Default. Safe for analysis, review |
| `workspace-write` | Read + write within `--cwd` | Implementation tasks, file generation |
| `danger-full-access` | Full system access | Only when explicitly authorized. `--full` forces this |

Additional Codex flags:
- `--network` / `-n`: Enable network access (forced by `--full`)
- `--add-dir` / `-d`: Additional writable directories (repeatable)
- `--reasoning` / `-r`: Reasoning effort (`minimal`, `low`, `medium`, `high`, `xhigh`)

---

## Claude Permission Modes

| Mode | Behavior | When to use |
| --- | --- | --- |
| `default` | Prompts for permissions on each action | Interactive sessions |
| `acceptEdits` | Allows file edits without prompts | Semi-autonomous work |
| `bypassPermissions` | Full autonomy, no prompts | Default for agent-mux dispatches |
| `plan` | Read-only analysis, no file mutations | Architecture review, planning |

Additional Claude flags:
- `--max-turns`: Limit agent conversation turns
- `--max-budget`: USD budget cap (`maxBudgetUsd`)
- `--allowed-tools`: Comma-separated tool whitelist

---

## OpenCode Configuration

**Model selection:** Use `--model <preset>` or `--variant <preset>`.

| Preset | Context | Cost | Strength |
| --- | --- | --- | --- |
| `kimi` | 262K | Paid | Multimodal, largest context window |
| `glm-5` | Standard | Paid | Agentic engineering, tool-heavy tasks |
| `opencode-minimax` | Standard | Free | Strong SWE-bench (80%) |
| `deepseek-r1` | Standard | Free | Code reasoning |
| `free` | Standard | Free | Zero-cost smoke tests |

Additional OpenCode flags:
- `--agent`: OpenCode agent selection

---

## Session Management

### Codex
- Sessions are single-shot by default
- `session_id` returned in metadata for tracking
- Each invocation is independent

### Claude
- Sessions are single-shot per agent-mux invocation
- Turn count controlled via `--max-turns` or effort-derived defaults
- Budget caps via `--max-budget`

### OpenCode
- Sessions managed by the OpenCode CLI
- Model routing determined by preset selection
- Agent selection via `--agent` flag
