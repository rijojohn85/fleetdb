# AGENTS.md — FleetDB book project

Standing instructions for any AI agent (or collaborator) working in this
repo. Read MEMORY.md and the `feedback_*.md` files it indexes before
starting work — they carry user corrections that apply to every session.

## What this project is

A technical book — **"Building a Kubernetes Operator with Operator SDK"**
— about **FleetDB**, a multi-tenant Postgres provisioning operator. The
book (`book/`, mdBook, published to GitHub Pages) is the deliverable.
The `fleetdb/` directory is the operator project the book describes and
the reader builds. Full context and chapter plan:
`fleetdb-operator-sdk-book-prompt.md`.

- Book source: `book/src/` — `SUMMARY.md` is the authoritative outline
  (20 chapters, ch00–ch19, 5 phases). Never create chapter files that
  aren't in SUMMARY.md; keep in-chapter cross-references pointing at the
  current numbering.
- FleetDB operator: `PostgresTenant` CR (`postgres.fleetdb.io/v1alpha1`)
  → StatefulSet + headless Service + Secret + PVC, a backup CronJob via
  a second controller, per-tenant observability (metrics/logs/traces +
  auto-provisioned Grafana dashboard), pgAdmin. Later: conversion +
  admission webhooks, bundle/CSV, scorecard, OLM install/upgrade.

## Hard rules

1. **Write the book, not the operator.** Never run scaffolding commands
   (`operator-sdk init`, `create api`, `make`, etc.) against, or write
   source files into, `fleetdb/` — that is the user's hands-on workspace.
   Verify chapter code by building/running it in scratch space (e.g.
   `/tmp/...` or a throwaway kind cluster), then delete the scratch
   artifacts. Keep only logs if needed.
2. **TDD every chapter, for real.** Write the failing test first in the
   chapter narrative, run it, show actual failure output (real red), then
   the implementation, then the green run. Never narrate output you
   didn't produce in scratch space.
3. **Surface SOLID inline.** When a design decision exercises a
   principle (e.g. `reconcileCreateOnce` = Dependency Inversion), call
   it out in plain language where it arises — not as a bolted-on section.
4. **Self-check before finalizing each chapter:** tests before
   implementation? ≥1 SOLID principle named where it actually applies?
   Reader-level checklist (below) passed?

## Reader profile

New to Ginkgo, new to controller development, beginner at Go, beginner
at Kubernetes. (The book's premise mentions prior Kubebuilder exposure,
but write to the profile above.) Before finalizing any chapter, verify:

- The complete final version of every function the chapter builds is
  shown at least once — not only fragments. Fragments state where they
  live and where their variables come from.
- Every snippet compiles as printed; all variables are declared; the
  first occurrence of any repeated pattern (e.g. a test spec shape) is
  shown in full before later copies elide it.
- Write in very simple English. Short sentences, everyday words, one
  idea per sentence. After writing any explanation, re-read it as a
  beginner who has never used the concept being taught (metrics,
  conditions, requeues, whatever it is): would they follow it on one
  read? Concrete analogies and worked examples beat compressed
  abstractions. If a sentence needs re-reading, rewrite it. (Found in
  a Chapter 6 review, 2026-08-31: the "counters count attempts, not
  outcomes" paragraph was too dense for the reader profile.)
- Keep every line inside a code fence ≤ 80 columns so nothing
  overflows the rendered mdBook code box: wrap function signatures,
  chained calls (`Expect(x).` / `To(...)`), struct literals, and long
  `Get`/`Create` calls across lines. Exceptions that must stay long
  because wrapping changes meaning: verbatim command output (test
  runner logs, FAIL summaries), `+kubebuilder:rbac` marker comments,
  and struct tags. Scan before finalizing a chapter, e.g.
  `awk '/^```/{i=!i;next} i&&length>80{print FILENAME":"FNR}' chapter.md`.
- Import blocks are given for implementation files, not just test files.
- No latent bugs in illustrative code — check fragment logic against the
  whole function, not just whether tests would pass.
- Terms explained inline at first use, however small (Secret `Data` vs
  `StringData`, `POSTGRES_*` image conventions, `envFrom`, StatefulSet
  vs Deployment, governing Service, PVC claims, `ctrl.Result`,
  `NamespacedName`, `intstr`, `apierrors.IsNotFound`, `var _ = Describe`,
  etc.).
- Related artifacts are explicitly connected (ordering in `Reconcile`,
  what consumes what) — not presented as independent pieces.
- Honest gaps are named in prose (envtest doesn't enforce RBAC, missing
  `.Owns(...)` watches) rather than implied away by passing tests.

## Book style

- Modeled on *Distributed Services with Go*: plain language, no
  unexplained jargon, incremental builds, first-principles explanations.
- Each chapter ends in a working, commit-able state with a "commit
  checkpoint" section (repo tree + test evidence).
- Every chapter has a "How this differs from Kubebuilder" callout — even
  when the answer is "nothing does" (say so explicitly).
- Chapter structure: conceptual intro → failing tests (with real output)
  → implementation → green run → checkpoint.
- Don't assume controller-runtime internals (cache, informers, leader
  election) or observability concepts — both are taught from zero, with
  hands-on experiments (e.g. kill a leader pod, watch the lease).
- Module path `github.com/yourusername/fleetdb` is an intentional
  placeholder — every reader substitutes their own. Do not "fix" it.

## Repo conventions

- `MEMORY.md` is the index of standing user feedback; each entry lives in
  its own `feedback_*.md` file. Add new user corrections there and index
  them.
- Chapter stubs are one-line `# Coming soon` files until written.
- Environment validated in Phase 0: operator-sdk v1.42.3, kind cluster
  named `fleetdb`, OLM installed. Smoke-test log:
  `book/phase0-smoke-test.log`.

## Current state

- Written: introduction, ch00–ch07 (Phase 2 in progress).
- Next chapter: **ch08 — Traces in Practice**. Picks up ch07's
  closing thread: `reconcileID` correlates within one reconcile;
  traces correlate across components (controller → API server →
  CronJob pod).
- ch04 delivered a second controller (BackupReconciler + CronJob);
  ch05 is the concepts chapter with real scrape/log experiments; ch06
  added `fleetdb_credentials_generated_total` and
  `fleetdb_backups_scheduled_total` on `metrics.Registry`.
- Standing threads: envtest RBAC not enforced (verify in ch18), real
  backup durability deferred, `.Owns` recreate = new Secret password
  (rotation is a later chapter), typo'd schedule rejection → ch15.
