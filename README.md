# Reasonix

English | [简体中文](README.zh-CN.md)

A **config- and plugin-driven** coding agent: a thin harness driving multiple
models. The core knows only interfaces — concrete models and tools are resolved
from configuration or injected by plugins. Ships as a single static binary.

## Features

- **Config-driven.** Providers, the agent, enabled tools, and plugins are all
  declared in `reasonix.toml`. No hardcoded models.
- **Multi-model & composable.** DeepSeek (flash/pro) and MiMo ship as presets;
  any OpenAI-compatible endpoint is a config entry, not new code. Optionally run
  two models together (executor + planner) in separate, cache-stable sessions.
- **Plugin-driven.** External tools run as subprocesses over stdio JSON-RPC
  (MCP-compatible). Built-in tools self-register at compile time.
- **Zero-friction distribution.** `CGO_ENABLED=0` single binary; cross-compile
  to six targets with one command. The only dependency is a TOML parser.

## Install / Build

```sh
make build      # -> bin/reasonix
make cross      # -> dist/ (darwin|linux|windows × amd64|arm64)
```

## Quick start

```sh
reasonix init                       # scaffold ./reasonix.toml
export DEEPSEEK_API_KEY=sk-...  # or put it in .env (see .env.example)
reasonix run "implement the TODOs in main.go"
reasonix run --model mimo-pro "add unit tests for this function"
echo "explain this code" | reasonix run
```

## Configuration

Resolution order: **flag > `./reasonix.toml` > `~/.config/reasonix/config.toml` >
built-in defaults**. Secrets come from the environment via `api_key_env` and are
never stored in config files.

```toml
default_model = "deepseek-flash"   # executor; set [agent].planner_model to add a planner
# language    = "zh"               # ui language; empty = auto-detect from $LANG / $REASONIX_LANG

[[providers]]
name        = "deepseek-flash"
kind        = "openai"
base_url    = "https://api.deepseek.com"
model       = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
# also preset: deepseek-pro, mimo-pro (mimo-v2.5-pro), mimo-flash (mimo-v2-flash) @ api.xiaomimimo.com/v1

[tools]
enabled = []   # omit/empty = all built-ins

[permissions]
mode  = "ask"                                # writer fallback when no rule matches: ask|allow|deny
deny  = ["bash(rm -rf*)", "bash(git push*)"] # hard-blocked in every mode
allow = ["bash(go test*)"]                   # never prompted

[sandbox]
# workspace_root = ""          # file-writers confined here; empty = current dir
# allow_write    = ["/tmp"]    # extra dirs write_file/edit_file/multi_edit may touch

[[plugins]]
name    = "example"
command = "reasonix-plugin-example"
```

Permissions gate each tool call: `deny` > `ask` > `allow` > fallback (readers
always allow; writers fall back to `mode`). `reasonix chat` prompts before writers
(`y` once · `a` this session · `n` no); `reasonix run` stays autonomous but still
honours `deny`. See [`docs/SPEC.md`](docs/SPEC.md) for the full schema and contract.

Permissions are *policy* (which calls to allow / prompt). The **sandbox** is
*enforcement*: the file-writers (`write_file` / `edit_file` / `multi_edit`)
refuse any path outside `[sandbox] workspace_root` (default: the current dir, so
edits stay in the project), resolving symlinks and `..` so a link can't tunnel
out. Reads are unrestricted. `bash` is itself jailed on macOS by default
(`[sandbox] bash`, Seatbelt): commands may write only those same roots (plus
temp and toolchain caches) and reach the network only when `[sandbox] network`
is set. Other platforms fall back to running unconfined for now (see
`docs/SPEC.md` §9 for the escape-prompt and Linux support still to come).

### Plugins (MCP)

Reasonix is an MCP client. A `[[plugins]]` entry's `type` selects the transport:
`stdio` (default) launches a local subprocess (`command`/`args`/`env`); `http`
(Streamable HTTP) connects to a remote `url` with optional static `headers`
(`${VAR}` / `${VAR:-default}` expanded from the environment, so tokens stay out
of the file). Tools surface to the model as `mcp__<server>__<tool>`, matching
Claude Code; a tool declaring MCP's `readOnlyHint: true` joins parallel dispatch
and the permission reader-default.

A server's **prompts** surface as `/mcp__<server>__<prompt>` slash commands
(positional args after the command); its **resources** are pulled in by writing
`@<server>:<uri>` in a message; `/mcp` lists connected servers and what each
exposes. `make build` also produces `bin/reasonix-plugin-example` — a runnable
reference stdio server (`echo`, `wordcount`, a `review` prompt, a style-guide
resource) you can copy.

```toml
[[plugins]]                       # local stdio server
name    = "example"
command = "reasonix-plugin-example"

[[plugins]]                       # remote server over Streamable HTTP
name    = "stripe"
type    = "http"
url     = "https://mcp.stripe.com"
headers = { Authorization = "Bearer ${STRIPE_KEY}" }
```

**Already have a Claude Code `.mcp.json`?** Drop it in the project root and Reasonix
reads it as-is — the `mcpServers` spec (`command`/`args`/`env`, `type`/`url`/
`headers`, `${VAR}` expansion) maps field-for-field onto `[[plugins]]`. Both
sources are merged; on a name collision `reasonix.toml` wins.

```json
{
  "mcpServers": {
    "filesystem": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path"] },
    "stripe": { "type": "http", "url": "https://mcp.stripe.com", "headers": { "Authorization": "Bearer ${STRIPE_KEY}" } }
  }
}
```

### Slash commands

In `reasonix chat`, built-in commands (`/compact`, `/new`, `/mcp`, `/help`) run
locally. **Custom commands** are Markdown files under `.reasonix/commands/` (project)
or `~/.config/reasonix/commands/` (user) — `review.md` becomes `/review`, a
subdirectory namespaces it (`git/commit.md` → `/git:commit`). The body is a
prompt template; invoking the command sends it as a turn.

```markdown
---
description: Review the staged diff
argument-hint: [focus-area]
---
Review the staged diff. Focus on $ARGUMENTS, list bugs with file:line.
```

`$ARGUMENTS` expands to all space-separated args, `$1`…`$N` to positional ones.
MCP prompts also appear here as `/mcp__<server>__<prompt>`.

### @ references

Embed `@` references in a message and Reasonix resolves them before sending, as
tagged context blocks: `@path/to/file` (or `@dir`) injects a local file's
contents (or a directory listing), and `@<server>:<uri>` injects an MCP
resource. A local path is only treated as a reference when it actually exists,
so ordinary `@mentions` stay literal. Typing `/` or `@` opens an autocomplete
menu — slash commands, or hierarchical file navigation (one directory level at a
time, descend into folders) plus MCP resources.

### Two-model collaboration (optional)

`reasonix init` keeps first-run minimal: pick provider → keys (every SKU of a
chosen provider is enabled). Running two models together (executor + planner,
separate cache-stable sessions) is a one-line edit afterwards — set
`planner_model` to any other enabled provider:

```toml
[agent]
planner_model = "deepseek-pro"   # used as the low-frequency planner
```

## Architecture

Three tiers of extensibility, all behind registries the core resolves by name:

1. **Registry** — `Provider` and `Tool` are interfaces; the core has no
   `switch model`.
2. **Compile-time built-ins** — providers (`provider/openai`) and tools
   (`tool/builtin`) self-register via `init()`; `main` blank-imports them.
   Adding a built-in is one file plus one import.
3. **Runtime plugins** — executables declared in config, spoken to over
   newline-delimited JSON-RPC 2.0 on stdin/stdout (the MCP stdio convention).
   Each remote tool is adapted to the `Tool` interface.

## Status

Done: registry-based providers/tools, OpenAI-compatible streaming with tool
calls (bounded retry on 429/5xx), nine built-in tools (read_file, write_file,
edit_file, multi_edit, bash, ls, glob, grep, web_fetch), TOML config, an
interactive `reasonix init` wizard, two-model collaboration (executor + planner in
separate, cache-stable sessions), low-frequency context compaction, sub-agents
(`task`), a bubbletea chat TUI (markdown, plan mode, context gauge, `/compact`
`/new`), session persistence + resume, per-call **permissions** (allow/ask/deny
rules; chat prompts before writers, deny rules hard-block everywhere), a
**workspace sandbox** confining file-writers to the project (symlink/`..`-safe),
stdio (MCP) client — **stdio + Streamable HTTP** transports, tools (`mcp__server__tool`,
`readOnlyHint`-aware), prompts (slash commands), resources (`@`-references), and
`/mcp`, configured via `[[plugins]]` or a Claude-style project `.mcp.json` —
custom slash commands (`.reasonix/commands/*.md`), `@file` / `@resource`
references, plus a runnable reference plugin (`cmd/reasonix-plugin-example`), the
harness loop, and CLI. The chat runs in the terminal's normal buffer (native
scrollback) with `/` and `@` autocomplete. Next: an OS-level sandbox for `bash`
(macOS Seatbelt / Linux bubblewrap, "allow inside the box, prompt at its edge"),
an Anthropic-native provider, MCP OAuth + legacy SSE. See `docs/SPEC.md` §9.
