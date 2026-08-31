# Chapter 5: What Are Metrics, Logs, and Traces?

## Why Phase 2 starts here

Phase 1 ended with a genuinely working operator: two controllers, six
resources, self-healing children, backups on a schedule. So the
natural question is — why interrupt the feature work for three
chapters of "observability"?

Because of what *you* were doing two chapters ago. When the gizmos
Service refused to appear, you found it by reading test failure
output and controller logs. When the backup controller error-spammed
the API server with `spec.schedule: Required value`, you found the
cause the same way. That works when you're the one running `make
test` at a terminal. It stops working the moment FleetDB runs on a
cluster somewhere, at 3 a.m., managing databases that real people
use, when the only thing you receive is a Slack ping that says
"backups are failing."

Observability is the discipline of answering questions about a system
from its outputs instead of by poking at its insides. And the entire
field, despite the textbook-sized literature, rests on three artifact
types, each tuned to one question:

- **Metrics** — numbers that answer *"is something wrong?"* cheaply,
  across everything, right now.
- **Logs** — event records that answer *"what exactly happened?"*
  richly, for the thing that's wrong.
- **Traces** — causal chains that answer *"where in the path did it
  go wrong?"* when one logical operation crosses many components.

None of the three is optional, and none substitutes for another —
that's the actual thesis of this chapter, and everything from here to
Chapter 9 is machinery for producing and consuming these three things
in FleetDB.

This chapter is conceptual: no new controllers, no new specs. But it
is *not* hand-wavy — every number and every log line printed below
was scraped from a real run of the suite you already have, using
nothing but the machinery Chapters 1–4 already built. That's
deliberate: FleetDB has been observable since Chapter 2. You just
haven't been looking.

## Metrics: cheap answers about everything

A metric is a number, some labels, and a timestamp — nothing more.
`reconcile_total = 47`. `queue_depth = 0`. That poverty is the point:
a metric says almost nothing about *why*, which is exactly what makes
it cheap enough to collect about *everything*, continuously, forever.
Metrics are the only member of the trio you can afford to record
every second for every tenant and keep for months.

In the Prometheus world (where controller-runtime lives — more on
that in Chapter 6), every metric line looks like this:

```text
metric_name{label="value",other_label="value"} 12345
```

The name says *what* is being measured, the labels say *which
slice* of it, and the number is the measurement. Three kinds of
measurement cover nearly everything you'll ever emit or consume:

- **Counter** — only ever goes up. Requests handled, errors seen,
  reconciles run. The interesting signal is never the value itself
  but its *rate of change* ("errors jumped from ~0/min to 40/min").
- **Gauge** — a current reading that goes up *and* down. Queue depth,
  active workers, memory used. The value is the signal.
- **Histogram** — many measurements dropped into buckets ("how many
  reconciles took under 5ms? under 25ms?"). This is how you answer
  "how slow is slow?" — you can't keep every duration as a metric,
  but buckets give you percentiles.

### The experiment: FleetDB is already emitting these

Chapter 2 disabled the manager's metrics endpoint with
`Metrics: metricsserver.Options{BindAddress: "0"}` — the sentinel
for "don't bind a port," chosen because test runs would fight over a
port. Turn it back on for one experiment. In `suite_test.go`:

```go
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: ":8081"},
	})
```

and run the suite:

```console
$ go test ./internal/controller/ -run TestControllers -count=1
```

while it runs (the manager is alive from `BeforeSuite` until
teardown — scrape about ten seconds in, after specs have been
creating tenants for a while), from another terminal:

```console
$ curl -s localhost:8081/metrics | grep '^controller_runtime_reconcile_total'
controller_runtime_reconcile_total{controller="backup",result="error"} 0
controller_runtime_reconcile_total{controller="backup",result="requeue"} 0
controller_runtime_reconcile_total{controller="backup",result="requeue_after"} 0
controller_runtime_reconcile_total{controller="backup",result="success"} 10
controller_runtime_reconcile_total{controller="postgrestenant",result="error"} 0
controller_runtime_reconcile_total{controller="postgrestenant",result="requeue"} 0
controller_runtime_reconcile_total{controller="postgrestenant",result="requeue_after"} 11
controller_runtime_reconcile_total{controller="postgrestenant",result="success"} 0
```

(Your exact numbers will differ — scrape timing and machine speed
change them. The *shape* won't.)

Sit with that output for a minute, because it's doing several jobs at
once.

**The counter and its labels.** `controller_runtime_reconcile_total`
is a counter — you can see the design: it only counts up, and the
`result` label slices it by outcome. `error + requeue + requeue_after
+ success` is the complete budget of Chapter 3's return-value table,
turned into telemetry. The manager emits these without you writing a
line of metric code.

**Chapter 4's `Named("backup")` failure, vindicated.** The
`controller` label is the controller *name* — the thing
controller-runtime refused to let two controllers share. Had the
duplicate-name validation not fired in Chapter 4, both controllers
would be writing to the same label value here, and
`reconcile_total{controller="postgrestenant"}` would silently count
two controllers' work as one. The startup error you hit was
protecting this graph.

**The two controllers' personalities, in numbers.** The tenant
controller shows eleven `requeue_after` and zero `success` — because
in envtest no pod ever becomes ready, so every tenant reconcile ends
at Chapter 3's `RequeueAfter: 10 * time.Second` branch, forever
waiting. The backup controller shows ten `success` and zero
requeues — its `Reconcile` has no requeue logic at all: create or
delete the CronJob, then return "done." Same codebase, same suite,
and the metrics already tell the two controllers apart *behaviorally*.
This is the "is something wrong?" power: if the backup controller
suddenly showed a rising `error` count — say, someone deployed with a
bad RBAC role and every CronJob create got `Forbidden` — this one
metric line would tell you before any human noticed a missing backup.

The endpoint also carries gauges and histograms. Two gauges worth
meeting now:

```text
workqueue_depth{controller="postgrestenant",name="postgrestenant"} 0
controller_runtime_active_workers{controller="backup"} 0
```

`workqueue_depth` is the length of Chapter 2's work queue *right now*
— a gauge, because it rises when events flood in and falls as workers
drain it. Zero here means "fully caught up" (the scrape landed
between reconciles). And the reconcile *duration* histogram, which
shows how the bucket shape works:

```text
controller_runtime_reconcile_time_seconds_bucket{controller="backup",le="0.005"} 8
controller_runtime_reconcile_time_seconds_bucket{controller="backup",le="0.01"} 10
controller_runtime_reconcile_time_seconds_bucket{controller="backup",le="0.025"} 10
...
```

Read it as: of the backup controller's 10 reconciles, 8 finished
within 5 milliseconds, and all 10 within 10ms — the cumulative
`le=` (less-than-or-equal) buckets are the histogram's whole trick.
A fast, boring controller; exactly what a create-once CronJob
reconciler should be.

One honest footnote from capturing this chapter's own output: the
first scrape landed while the suite was still in `BeforeSuite`, and
every counter read zero. Metrics describe the running system *at
scrape time* — a zero is a real reading, not a malfunction, and
knowing when a number was taken matters as much as the number.

Turn the endpoint back off (`BindAddress: "0"`) after the
experiment — Chapter 9 wires it up for real, permanently.

## Logs: the whole story, one event at a time

A log line is a record that *something happened, when it happened,
with what context* — written at the moment it happened, and never
updated. Where a metric answers "how many reconciles failed tonight?"
with one number, logs answer "show me the reconcile that failed, with
everything it was doing" — one line at a time, in full detail. That
richness is why logs are expensive: you can't aggregate them the way
you aggregate counters, so you read them selectively, usually *after*
a metric has pointed at the neighborhood.

You have been reading logs since Chapter 2 — every `log.Info("secret
created")` and every `log.Error(err, "error reconciling backup")`
you've seen scroll past *is* structured logging. Here's a real line
from this chapter's run, dissected:

```text
2026-08-31T11:51:49+05:30	INFO	starting reconciliation
    {"controller": "postgrestenant", "controllerGroup":
    "postgres.rijojohn.xyz", "controllerKind": "PostgresTenant",
    "PostgresTenant": {"name":"drills","namespace":"default"},
    "namespace": "default", "name": "drills", "reconcileID":
    "395f3b2b-068f-495f-b4f3-fc3d1f976b99", ...}
```

Zap — the logger `logf.SetLogger` installed in `suite_test.go` back
in Chapter 1 — prints four zones, and each exists for a reason:

- **The timestamp** — when, precisely, including timezone. Log lines
  are only ever useful in sequence, and sequence needs clocks.
- **The level** (`INFO`) — a coarse severity knob. FleetDB uses
  `Info` for "normal operation worth seeing" (`starting
  reconciliation`), `Debug` for "detail that's noise unless you're
  debugging" (every event write), and `Error` for "something failed"
  (the backup controller's rejected CronJob creates, in your last
  debugging session). Levels are how one log stream serves both the
  casual observer (INFO) and the debugging session (DEBUG).
- **The message** — the human sentence.
- **The structured fields** — the JSON blob. *This* is the part that
  makes it structured logging rather than `fmt.Printf`: the
  attributes are key-value pairs a machine can filter on. `grep
  reconcileID` to pull one reconcile's lines out of millions; `grep
  '"controller": "backup"'` to see only the backup controller. You
  did this instinctively while debugging Chapter 4 — the error
  messages you pasted into this book's sessions carried their
  `reconcileID`s, and that's why they were readable.

Two of those fields deserve special attention. `controller` labels
each line by which of your two controllers emitted it — the log-level
equivalent of the metric label, and another dividend of `Named`.
And `reconcileID` is the work queue's stamp on every log line from
one call of `Reconcile` — one ID, many lines (the fetch, the secret
create, the status write, the requeue decision). It is, quietly, a
*correlation ID*: the raw material of the third pillar, as you'll
see in the next section.

### The experiment that wasn't about logs

Capturing real log lines for this chapter took three attempts, and
the failures are the actual lesson. The suite's logger writes to
`GinkgoWriter` — Ginkgo's buffer — which Ginkgo *suppresses* when a
run passes, dumping it only on failure. Switching the logger to
`zap.WriteTo(os.Stderr)` fixed that... and produced nothing,
because `go test` *also* suppresses a passing test binary's output
unless you pass `-v`. Only `-v` plus stderr showed the lines.

Three layers — your logger's sink, the test framework's buffer, the
test runner's verbosity — each silently deciding whether the logs
you wrote get *seen*. That's the operational moral, and it's not a
testing artifact: in production, a controller's logs go to the
container runtime, and whether they survive depends on the node's
log rotation, the collector's config, and the retention policy —
none of which your code controls. Where logs *go* is as much a part
of logging as what they say. (FleetDB's production answer — running
as a real deployment with container logs — arrives in Chapter 18;
`kubectl logs` is the reader there.)

## Traces: one operation, many hops

The third pillar exists because of a gap the first two leave open.
Metrics tell you the backup controller errored 40 times. Logs tell
you *which* reconcile failed and what it printed. But when one
logical operation — "back up tenant acme's database" — is actually
many hops across many components (work queue → your `Reconcile` →
API server → CronJob controller → Job pod → `pg_dump` → network →
database), a pile of unrelated log lines from unrelated components
doesn't show you the *path*. Traces do: a trace records one
operation's end-to-end journey as a tree of **spans** — each span a
timed, labeled unit of work, children nested inside parents, all
sharing the trace's ID.

Read that description back against `reconcileID`: one ID shared by
every log line from one reconcile call, spanning your controller's
work plus the API-server calls underneath it. FleetDB has been
emitting proto-traces since Chapter 2 without the machinery.
The trace *systems* — OpenTelemetry, span exporters, sampling —
are Chapter 8's subject, and they're the heaviest machinery of the
three pillars; that's why this chapter only plants the flag. What
matters now is the question traces answer, which neither metrics
nor logs can: *where* in a multi-hop path did the time go or the
failure happen — the queue? the API server? the pod?

## Using them together

The three pillars are a workflow, not a menu. It runs, in order:

1. A **metric** trips — `reconcile_errors_total{controller="backup"}`
   is climbing. Cheap, aggregate, first responder. You now know
   *that* something is wrong and roughly *where*.
2. **Logs** at that location take over — filter by
   `controller="backup"` and level `error`, read the messages and
   structured fields. You now know *what* is failing and usually
   *why* — `spec.schedule: Required value`, say.
3. If the "why" is still fuzzy because the operation spans
   components — the CronJob was created but the backup pod never
   ran, and everyone's logs look fine — a **trace** shows the path
   and where it stopped.

Each step narrows the previous one. Metrics without logs leave you
knowing *that* but not *why*; logs without metrics leave you
reading everything forever; both without traces leave you guessing
inside multi-hop failures. It's worth naming the two classic
anti-patterns now, because both are tempting exactly where FleetDB
stands:

- **Grep-counting logs as metrics.** "We can just count ERROR lines
  per minute" — until log volume doubles, retention shrinks, or a
  format changes and your dashboard lies. Counters exist because
  counting must be cheap and permanent; logs are neither.
- **High-cardinality labels.** It's tempting to label a metric
  `tenant="acme"` — and with ten tenants, fine. With ten thousand,
  every new tenant multiplies every time series containing that
  label, and the metrics system drowns. The dividing rule you'll
  use in Chapter 6: *identity belongs in logs, category belongs in
  metrics* — `controller="backup"` is a category (handful of
  values), `tenant="acme"` is an identity (unbounded).

## How this differs from Kubebuilder

Nothing does — and this chapter is the proof. The metrics endpoint
is the manager's, controller-runtime's; the logger is zap through
`logf`, which Kubebuilder scaffolds identically; the workqueue
metrics and `reconcileID` come from the shared controller machinery,
not from either tool. Observability is where the SDK-vs-Kubebuilder
distinction matters least: you're consuming the same bedrock either
way. (Operator SDK's additions here are packaging-level —
Scorecard's telemetry checks in Chapter 17, the bundle's Grafana
dashboard references in Chapter 16 — and they sit *on top of* the
three pillars, not beside them.)

## Commit checkpoint

```
fleetdb/
├── internal/controller/
│   └── suite_test.go    # temporarily BindAddress ":8081" — revert to "0"
└── (everything else unchanged from Chapter 4)
```

No production code changed in this chapter — the deliverable is the
mental model, verified by two experiments against the running suite:
a scrape of real controller-runtime metrics (counters, gauges, and a
histogram, split by the controller names Chapter 4 established) and
real structured log lines (timestamp, level, message, fields,
`reconcileID`). The suite still passes: 16 specs, ~14 seconds.

Chapter 6 turns the metrics experiment into practice: what
controller-runtime already measures, what FleetDB should measure
about itself, and how the metrics endpoint becomes a scrape target
instead of a curl.
