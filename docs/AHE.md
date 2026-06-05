# Reasonix-AHE Design

## Goal

Build a cache-preserving Agentic Harness Engineering substrate for Reasonix.

DeepSeek-Reasonix-AHE is an experimental derivative and secondary-development
project based on [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix).
It is explicitly centered on
[Agentic Harness Engineering: Observability-Driven Automatic Evolution of
Coding-Agent Harnesses](https://arxiv.org/abs/2604.25850): Reasonix remains the
base coding-agent substrate, while AHE is the upgrade framework that makes the
harness observable, evaluable, revertible, and incrementally evolvable.

Reasonix-AHE v0.1 is a local experiment layer for observability, cache
contracts, harness snapshots, smoke evaluation, and evidence reports. It is not
an automatic self-modification system.

## Paper Alignment

Reasonix-AHE maps the paper's AHE loop onto a local Reasonix workflow:

- Component observability: harness source, snapshots, lock hashes, and proposal
  manifests make editable harness components explicit and revertible.
- Experience observability: traces, eval artifacts, cache reports, and evidence
  distillation turn raw agent runs into reviewable local evidence.
- Decision observability: proposal manifests, apply results, acceptance gates,
  lifecycle status, pins, and GC records pair every harness change with stated
  expectations and follow-up verification.

## Non-goals for v0.1

- No automatic self-modification of Reasonix core.
- No automatic merge of evolution proposals.
- No mid-session harness mutation after session-start injection.
- No dynamic injection of eval evidence into the live model prefix.
- No module path rename.
- No push to the upstream repository.

## Invariants

- Cache-first.
- Snapshot-based harness evolution.
- Active harness snapshots are loaded once at session start.
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
