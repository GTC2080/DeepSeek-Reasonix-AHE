# Reasonix-AHE Design

## Goal

Build a cache-preserving Agentic Harness Engineering substrate for Reasonix.

DeepSeek-Reasonix-AHE is an experimental derivative and secondary-development
project based on [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix).

Reasonix-AHE v0.1 is a local experiment layer for observability, cache
contracts, harness snapshots, smoke evaluation, and evidence reports. It is not
an automatic self-modification system.

## Non-goals for v0.1

- No automatic self-modification of Reasonix core.
- No automatic merge of evolution proposals.
- No live-session harness mutation.
- No dynamic injection of eval evidence into the live model prefix.
- No module path rename.
- No push to the upstream repository.

## Invariants

- Cache-first.
- Snapshot-based harness evolution.
- Append-only live sessions.
- Stable model-visible tool schema.
- Trace and evidence stay out of the live agent prefix.
- Eval gates include cache hit ratio and cache contract violations.
- Raw trace and eval artifacts must be garbage-collectable.

## Editable Harness Components

- Prompts.
- Tool descriptions.
- Skills.
- Middleware config.
- Model routing.
- Long-term memory.
