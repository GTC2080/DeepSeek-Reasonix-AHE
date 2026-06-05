# Reasonix Lab

Reasonix Lab is a local/offline subsystem for Reasonix-AHE experiments.

## Responsibilities

- Trace collection.
- Cache contract verification.
- Harness snapshot management.
- Smoke and canary evaluation.
- Evidence distillation.
- Proposal manifest checking.
- Artifact garbage collection.

## Local Artifacts

Generated local artifacts live under `.reasonix-ahe/` and
`.reasonix-harness/`. These directories are for local experiment state and
should not be pushed to the upstream repository.

## Cache Reports

`reasonix lab cache-report <trace.jsonl>` reads one trace JSONL file and
summarizes model calls, prompt cache hit/miss tokens, hit ratio, stable-prefix
drift, contract violations, and the active harness snapshot when present.

Use `--json` for machine-readable output. Use `--gate` with
`--min-hit-ratio` or `--max-contract-violations` when the report should affect
the process exit code.

## Evidence Distillation

`reasonix lab distill <eval-run-dir>` reads an eval run directory and writes
deterministic markdown evidence under `<eval-run-dir>/evidence/`. The reports
summarize task results, verifier output, tool-call counts, cache data, failure
taxonomy, and suggested harness components without calling a model.

## Proposal Manifests

`reasonix lab proposal create --base <snapshot-id> --name <name>` creates a
draft proposal under `.reasonix-ahe/proposals/` with `manifest.json`,
`evidence.md`, and `diff.patch`.

`reasonix lab proposal check <proposal-dir>` validates the manifest fields that
future harness evolution must declare. It does not apply diffs, create harness
snapshots, or modify live sessions.

`reasonix lab proposal status <proposal-dir>` reports the local review state.
Without `status.json`, invalid manifests are `draft` and valid manifests are
`ready`.

`reasonix lab proposal apply <proposal-dir> [--eval <task-or-suite>] [--bin path]
[--model name] [--trace-mode metadata|preview|full]` applies `diff.patch` in a
temporary copy of the base harness source, creates a target snapshot with its
own `source/` copy, and writes an apply result under the proposal directory. It
does not overwrite `.reasonix-harness/source`, accept the proposal, or activate
the target snapshot. When `--eval` is provided, the target snapshot is activated
only for the eval run and the previous active snapshot is restored afterward.

`reasonix lab proposal accept <proposal-dir> [--activate] [--pin-target]`
records an accepted review for a valid manifest whose target snapshot already
exists. The optional flags activate and pin that target snapshot.

`reasonix lab proposal reject <proposal-dir> --reason <text>` records a rejected
review with a human-readable reason.

## Snapshot Pins

`reasonix lab harness snapshot pin <snapshot-id>` and `unpin <snapshot-id>`
manage `.reasonix-harness/pinned`. Pinned snapshots are protected by
`reasonix lab gc --dry-run`.
