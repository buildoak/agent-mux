# Prompting Guide by Engine

Engine-specific prompting tips, model variants, and the golden rules for each engine.

---

## Codex (GPT-5.3)

**The golden rule:** Tell Codex WHAT to read, WHAT to check, and WHERE to write. Never say "explore" or "audit everything."

**What works:**
- One goal per invocation
- Explicit file targets
- Concrete deliverables (patches, tests)
- LOC limits and style constraints
- Bias toward action

**What fails:**
- "Audit the entire codebase."
- Multi-goal prompts
- Upfront planning announcements (causes premature stopping)
- Open-ended exploration

**Model variants:**

| Model | Speed | Context | SWE-Bench Pro | Terminal-Bench | Best for |
| --- | --- | --- | --- | --- | --- |
| `gpt-5.3-codex` (default) | ~65-70 tok/s | Standard | 56.8% | 77.3% | Thorough, pedantic, complex multi-step tasks |
| `gpt-5.3-codex-spark` | 1000+ tok/s | 128K | 56% | 58.4% | Fast grunt work, parallel workers |

**Reasoning levels:**

| Level | Use case | Notes |
| --- | --- | --- |
| `minimal` | Not recommended | Incompatible with MCP tools |
| `low` | Trivial fixes | Minimal reasoning overhead |
| `medium` | Routine tasks | Default level |
| `high` | Implementation | Sweet spot for most work |
| `xhigh` | Deep audits only | Overthinks routine work |

---

## Codex Spark

Same prompting discipline as Codex, tighter scope.

**Use for:**
- Parallel workers
- Filesystem scanning
- Docstring generation
- Fast iteration cycles

**Avoid for:**
- Complex multi-file refactors
- Deep reasoning
- Context beyond 128K

Invoke with: `--engine codex --model gpt-5.3-codex-spark`

---

## Claude (Opus 4.6)

**What works:**
- Open-ended exploration
- Multi-goal when needed
- Writing and documentation
- Prompt crafting for other engines
- Architecture with tradeoff reasoning

**Permission modes:**

| Mode | Use case |
| --- | --- |
| `default` | Interactive use, prompts for permissions |
| `acceptEdits` | Allows file edits without prompts |
| `bypassPermissions` | Full autonomy, default for agent-mux |
| `plan` | Read-only analysis, no mutations |

**Turn scaling:** Use `--max-turns` for effort control. Higher turns = more thorough exploration. Effort level auto-derives turns when unset.

---

## OpenCode

**What works:**
- End-to-end deliverable framing
- Structured output requests
- Cross-checking other engines

**Key presets:**

| Preset | Context | Cost | Strength |
| --- | --- | --- | --- |
| `kimi` | 262K | Paid | Multimodal, largest context |
| `glm-5` | Standard | Paid | Agentic engineering, tool-heavy |
| `opencode-minimax` | Standard | Free | 80% SWE-bench |
| `deepseek-r1` | Standard | Free | Code reasoning |
| `free` | Standard | Free | Zero-cost smoke tests |

---

## Engine Comparison Table

| Aspect | Codex (5.3) | Codex Spark | Claude (Opus 4.6) | OpenCode (varies) |
| --- | --- | --- | --- | --- |
| Speed | ~65-70 tok/s | 1000+ tok/s | ~65-70 tok/s | Varies |
| Context | Standard | 128K | 1M (beta) | 200-262K |
| Prompting | One goal, explicit files | Same, simpler tasks | Open-ended, multi-goal OK | End-to-end deliverables |
| Best for | Implementation, review | Fast grunt work | Architecture, writing | Third opinion, diversity |
| Fails on | Open-ended exploration | Complex multi-step | Drift without constraints | Vague prompts |

---

## Spark vs Regular Codex Decision

| Signal | Use Spark | Use Regular Codex |
| --- | --- | --- |
| Task complexity | Simple, well-scoped | Multi-step, complex logic |
| Parallelism needed | Yes -- many small workers | No -- one thorough pass |
| Speed priority | Latency-sensitive | Quality over speed |
| Context size | Under 128K | Larger context needed |
| Examples | Docstrings, rename, scan | Refactor, debug, implement |
