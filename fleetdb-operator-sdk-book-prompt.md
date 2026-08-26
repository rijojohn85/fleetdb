# Claude Code Prompt: "Building a Kubernetes Operator with Operator SDK"

## Context

I've built one Kubebuilder operator before (snapkeep, a snapshot-backup
operator) and wrote "Building a CSI Driver with Go" alongside localdir-csi,
but I have not worked much hands-on with leader election or how
controller-runtime's cache and informers actually work under the hood —
don't assume that ground is covered. I'm learning Operator SDK, and I want
this project to build real, working familiarity with the full
controller-runtime machinery (manager, cache, informers, leader election)
as well as everything Operator SDK adds on top of it: CRD version
conversion, webhooks, OLM packaging, bundle/catalog images, and scorecard
testing.

I want to write a technical book **while** building the project, the same
way I did for localdir-csi and "Building a CSI Driver with Go." Each chapter
should end in a working, commit-able state of the code.

## Project: FleetDB

A multi-tenant Postgres provisioning operator. A `PostgresTenant` CRD
provisions a StatefulSet, Service, Secret, and PVC per tenant, a scheduled
backup via CronJob, a per-tenant observability stack (metrics, logs,
traces, and an auto-provisioned Grafana dashboard), and a pgAdmin instance
wired to the tenant's database. The project exists to force contact with
everything Operator SDK adds on top of controller-runtime: conversion
webhooks, admission webhooks, bundle generation, scorecard, and OLM
lifecycle on a kind cluster — plus real, hands-on grounding in
controller-runtime internals and observability, neither of which I've
worked with much yet.

## Book style (match "Building a CSI Driver with Go")

- Modeled on *Distributed Services with Go* by Travis Jeffery: plain
  language, no unexplained jargon, incremental builds.
- TDD throughout — tests before implementation, every chapter.
- SOLID principles surfaced inline where they naturally arise, not bolted on.
- Concepts explained from first principles; self-contained, not
  back-and-forth Q&A style.
- Each chapter's code is a real commit — reader can `git checkout` chapter
  boundaries and have a working system at every one.
- Publish via mdBook to GitHub Pages, same as localdir-csi.

## Learning preferences to encode

- Hands-on, code-first: scaffold the interface/API layer first, then drop
  into real mechanics incrementally.
- No unexplained jargon — define terms inline the first time they appear.
- Prefer explaining Operator SDK internals in terms of what I already know
  from Kubebuilder/controller-runtime — call out explicitly where Operator
  SDK diverges (e.g. "this is scaffolded differently than kubebuilder
  because...") rather than presenting it as entirely new.
- Don't assume prior mastery of controller-runtime internals (cache,
  informers, leader election) — teach these from first principles with
  working code and small experiments (e.g. deliberately kill a leader pod
  and watch failover happen), not just a diagram and a paragraph.
- Don't assume any prior knowledge of observability concepts either.
  Explain what a metric, a log, and a trace actually are and why they're
  three separate things before writing any instrumentation code — plain
  language, concrete analogies, no assumed familiarity with Prometheus,
  Grafana, or OpenTelemetry terminology.

## Chapter plan (phases → chapters)

**Phase 0 — Validate environment first**
Before any code: confirm `operator-sdk` CLI, kind, and OLM installation
work end-to-end with a trivial "hello world" operator. Don't let later
chapters discover a broken toolchain.

0. Toolchain setup and OLM smoke test on kind

**Phase 1 — Core API and controller**

1. `PostgresTenant` v1alpha1 API + project scaffold — call out
   `operator-sdk init` vs `kubebuilder init` differences as they appear
2. Reconciler: owns StatefulSet + Service + Secret + PVC, TDD with envtest
3. Status conditions and requeue strategy
4. Scheduled backups: second controller owning a CronJob per tenant,
   backup status surfaced on `PostgresTenant.status` — real multi-resource,
   multi-controller ownership, not left as an exercise

**Phase 2 — Observability, from zero**

No assumed background here — each concept gets a plain-language
explanation with a concrete analogy before any code.

5. What are metrics, logs, and traces, and why are they three different
   things? (a log is one event's story; a metric is a number tracked over
   time; a trace follows one request across every hop it takes) — no code
   yet, just building the mental model
6. Metrics in practice: instrumenting the reconciler with Prometheus
   client_go — counters vs gauges vs histograms explained with the
   tenant-provisioning use case (`tenants_total`, `reconcile_duration_seconds`)
7. Logs in practice: structured logging with `logr`, correlating log lines
   to a specific reconcile request so a tenant's story can be followed
   end-to-end
8. Traces in practice: introducing OpenTelemetry, instrumenting the
   reconcile loop, exporting to Jaeger/Tempo, and reading your first trace
   waterfall
9. Deploying Prometheus + Grafana on the cluster, and having FleetDB
   auto-provision a per-tenant Grafana dashboard (dashboard-as-ConfigMap
   pattern) as part of reconciliation — the operator manages its own
   observability, not just the app it's provisioning
10. Deploying pgAdmin per tenant, wired to the tenant's generated
    credentials Secret, as an owned resource alongside the StatefulSet

**Phase 3 — controller-runtime internals, hands-on**

11. How the manager starts a cache: build a tiny standalone program using
    just `client-go`'s informer/Reflector directly (no controller-runtime)
    to watch Pods and print add/update/delete events — see the raw
    mechanism before controller-runtime's abstraction over it
12. Mapping that to controller-runtime: trace a live Reconcile call from
    watch event → work queue → Reconcile(), with logging inserted at each
    hop so the trace is visible, not just described
13. Leader election, hands-on: run FleetDB with 3 replicas, use
    `kubectl get lease` to watch the lease object, kill the leader pod
    mid-reconcile and observe failover and the new leader take over;
    explain what breaks if leader election is disabled with >1 replica

**Phase 4 — The genuinely new material (Operator SDK / OLM)**

14. Introducing `v1` with a breaking field rename; writing the conversion
    webhook; testing conversion round-trips
15. Validating and mutating admission webhooks (reject storage-class
    downgrades, default missing fields)
16. Bundle generation: `operator-sdk generate bundle`, hand-editing the CSV
    (install modes, RBAC, owned CRDs) — explain what a CSV *is* before
    editing one
17. Custom scorecard tests beyond the built-ins (assert a tenant reaches
    `Ready` within N seconds)
18. OLM install on kind: bundle image, index image, `operator-sdk run
    bundle`, and debugging a stuck CSV
19. Upgrade path: v1alpha1 → v1 CSV upgrade with a live tenant's data intact

## Deliverable format

For each chapter, produce:
- A short conceptual intro (first principles, plain language)
- Tests written first
- Implementation
- A "commit checkpoint" — what the repo should look like at chapter's end
- Where relevant, an explicit "how this differs from Kubebuilder" callout

Work through phases in order. Confirm Phase 0's environment actually works
before writing Chapter 1. Before Phase 2 (observability), also confirm
Prometheus and Grafana can actually be installed and reached on the kind
cluster — don't let a broken observability stack surface mid-chapter.
