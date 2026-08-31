# Chapter 6: Metrics in Practice

## What's worth measuring

Chapter 5 ended with the question this chapter answers. You saw that
the manager already measures itself — reconcile counts by result,
queue depth, duration histograms, all sliced by the controller names
Chapter 4 established. So the design question for FleetDB's own
metrics is not "what can we measure?" but "**what do we know that the
framework can't?**"

The filter is simple to state. Controller-runtime measures its own
mechanics: how many reconciles ran, how they ended, how long they
took, how deep the queue got. It will never know what those
reconciles *meant*. FleetDB's meaning lives in its domain events:

- a database **credential Secret was generated** — this happened,
  forever, for a tenant that just came into existence;
- a **backup schedule was installed** — a tenant asked for backups
  and now has a CronJob to show for it.

These are the facts you'd put on a dashboard: how many tenants exist
right now? How many have backups? The framework's counters can't
answer that. `reconcile_total` counts how many times the reconciler
*ran* — like counting how many times a cook stirred a pot. It never
tells you how many meals were served.

A fair question: why not keep one number per tenant, like
`tenants_with_backups{tenant="acme"} 1`? Two reasons, and both are
limits, not choices:

1. **That number changes constantly, and we'd have to compute it
   ourselves.** A gauge like that needs the answer to "list every
   tenant and check each one" — recomputed every time someone looks.
   Counters just add 1 when something happens. Adding 1 is cheaper
   than recounting the world.
2. **One label per tenant grows without limit.** Ten tenants means
   ten copies of every number. Ten thousand tenants means ten
   thousand copies — of *every* metric that carries the label. That
   is the cardinality rule from Chapter 5: a metric may split by
   *kind* (which controller — a short list), never by *identity*
   (which tenant — an ever-growing list).

So we count *events*: "a password was generated", "a backup schedule
was created". Counting events stays cheap no matter how many tenants
you have, and watching those counts climb is exactly the
operations-level signal: new credentials appearing means new tenants
are arriving; new backup schedules means tenants are adopting
backups.

Two counter metrics, then:

- `fleetdb_credentials_generated_total` — a single number that only
  ever goes up. The name is built from conventions: `fleetdb_` says
  "this metric belongs to FleetDB", so it can never be confused with
  another program's metrics on the same endpoint. And `_total` is
  the standard ending for counters — Prometheus tooling reads it as
  "this counts events".
- `fleetdb_backups_scheduled_total` — same shape, one idea over:
  counts backup schedules created.

**"Why not just count tenants?"** Because the cluster already does.
`kubectl get postgrestenant` *is* the number of tenants — the
cluster is the source of truth, and it is always right. If the
operator also kept its own tenant count, there would be two answers,
and they would disagree: right after a restart, before the operator
has looked at the cluster, its copy says zero while the truth is
nine. That's the Chapter 3 lesson again — never *remember* state,
always re-read it — applied to metrics: don't keep a copy of a
number someone else already owns.

What the cluster *can't* tell you is the history: how many tenants
arrived tonight, whether onboarding is speeding up or stopped. The
credential counter is that history — each new tenant generates one
credential, so the line's slope *is* the onboarding rate. And it
stays meaningful later: when FleetDB learns to rotate passwords,
the same counter counts rotations too. A tenant count would not.

TDD, as always — and the red arrives at compile time, because the
test can't reference a metric that doesn't exist.

## Red: counting generated credentials

New file, `internal/controller/metrics_test.go` — the metrics
concern gets its own home, same package, so `k8sClient` and
`mustQuantity` are available as always:

```go
package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	postgresv1alpha1 "github.com/yourusername/fleetdb/api/v1alpha1"
)

var _ = Describe("FleetDB metrics", func() {
	const timeout = 5 * time.Second
	const interval = 100 * time.Millisecond

	It("counts generated credential Secrets", func() {
		before := testutil.ToFloat64(fleetdbCredentialsGenerated)

		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{Name: "clamps", Namespace: "default"},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				DatabaseName: "clamps",
				StorageSize:  mustQuantity("1Gi"),
			},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		Eventually(func() float64 {
			return testutil.ToFloat64(fleetdbCredentialsGenerated)
		}, timeout, interval).Should(BeNumerically(">", before))
	})
})
```

Two pieces are new, and both matter beyond this test.

`testutil.ToFloat64` is from
`github.com/prometheus/client_golang/prometheus/testutil` — a test
helper that reads a collector's current value directly, in process.
No HTTP, no parsing the text format: the test asserts on the same
in-memory counter the endpoint will eventually serve. (client_golang
is already in your dependency tree — controller-runtime uses it for
exactly the metrics you scraped in Chapter 5 — but it's an *indirect*
dependency so far. The first build after adding these imports will
say so; run `go mod tidy` when it does, and it moves to your
`require` block as a direct dependency.)

The assertion is a **delta**, not a fixed value:
capture `before`, then require the counter to exceed it. Why not
`Should(Equal(1))`? Because this counter is *global state in the
test process* — every spec that ran before this one and created a
tenant already bumped it, and Ginkgo runs specs in randomized order
by default. A fixed value would pass in one run order and fail in
the next; the delta pattern is order-proof by construction. This is
also the honest way to test counters anywhere: what you always mean
is "it went up because of this," never "it equals this."

```text
vet: internal/controller/metrics_test.go:21:32: undefined:
fleetdbCredentialsGenerated
```

Real red. Green time.

## Green: the metrics file

New file, `internal/controller/metrics.go` — and the Single
Responsibility Principle puts it there deliberately: every custom
FleetDB metric will live in this one file, so "what does FleetDB
measure about its domain?" has a one-file answer, separate from the
controllers that *cause* the measurements:

```go
package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// fleetdbCredentialsGenerated counts every database credential
// Secret the tenant controller generates. Framework metrics
// (reconcile counts, queue depth) come from controller-runtime;
// this one is FleetDB's own domain.
var fleetdbCredentialsGenerated = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "fleetdb_credentials_generated_total",
		Help: "Number of database credential Secrets generated.",
	},
)

func init() {
	metrics.Registry.MustRegister(fleetdbCredentialsGenerated)
}
```

Four decisions in twenty lines:

**`prometheus.NewCounter` and its options.** `CounterOpts` carries
the name (the `_total` suffix is the convention — Prometheus tooling
treats it as the counter marker), and `Help`, the one-line string
that appears next to the metric in every scrape. Write Helps for the
you-of-six-months-ago; they're the only documentation a metric gets.

**`metrics.Registry` is the manager's registry.** This is
controller-runtime's `prometheus.Registerer` — the same registry the
`controller_runtime_*` metrics from Chapter 5 are registered in.
Registering FleetDB's counters there means one `/metrics` endpoint
serves framework and domain metrics side by side, which is exactly
what a scrape target should be. No second port, no second format.

**`MustRegister` panics, on purpose.** Registering a metric whose
name is malformed or already taken is a programming error that will
never heal by retrying — every scrape would be wrong until someone
fixes the code. Fail-fast at startup (the same instinct as
`Expect(err).NotTo(HaveOccurred())` in `BeforeSuite`) beats serving
silently-broken telemetry.

**Package-level variable — a deliberate trade.** The counter is a
package-level variable — declared outside any struct, so every file
in the package shares the same one copy:

```go
// metrics.go — one shared copy for the whole program
var fleetdbCredentialsGenerated = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "fleetdb_credentials_generated_total",
		Help: "Number of database credential Secrets generated.",
	},
)

// any file in the package can reach it directly:
fleetdbCredentialsGenerated.Inc()
```

Go style guides usually warn against this: when a variable is shared
by everything, any function can change it, and that gets hard to
track. The alternative is a field on the reconciler, handed in at
construction like `Client` and `Recorder` are:

```go
type PostgresTenantReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Recorder             record.EventRecorder
	CredentialsGenerated prometheus.Counter
}
```

We chose the shared variable anyway, for two plain reasons. First,
the metric really is one number: "credentials generated" has exactly
one value on the endpoint, and a shared variable is the simplest way
to write "exactly one of these exists". Second, the library you're
using does the same — `metrics.Registry` itself is a shared
package-level variable, which is why the code above could call
`MustRegister` on it. Following the library keeps your code familiar
to anyone who has read a controller before.

The struct-field version isn't wrong — it just costs extra wiring in
every place a reconciler is built (`cmd/main.go`, `suite_test.go`,
and more as chapters go by) to buy something our tests don't need:
each reconciler could get a private counter. The tests handle the
one real cost of sharing — specs must not assume fixed values — with
the delta pattern from the test you just read. Break the rule on
purpose, know the price, write down the reason.

**"But several goroutines share it — isn't that dangerous?"** Good
question to ask, and the answer is no, for a reason you never have
to build yourself. Your controllers run reconciles on several worker
goroutines at once, so two of them genuinely can call `Inc()` at the
same moment. For an ordinary variable that would be a bug: both
goroutines could read 7, both write 8, and one login would silently
vanish. The Prometheus client protects against exactly this —
`Inc()` is an *atomic* add, one CPU instruction that reads and
updates the number as a single unbreakable step, so there is no gap
for the second goroutine to sneak into. No mutex is needed, and this
is a promise on the interface itself: every metric type in the
Prometheus client (counters, gauges, histograms) is documented safe
for concurrent use.

The line to remember: *library-provided metrics promise concurrent
safety; your own hand-rolled state does not.* The day FleetDB keeps
its own shared map or counter in Go — `tenantCounts[name]++` on a
variable several goroutines touch — that is the day you need
`sync.Mutex` or `sync/atomic` yourself, because a plain `++` has
exactly the lost-update bug described above. Nothing in FleetDB
holds shared mutable state today; all real state lives in the API
server, which is the Chapter 3 design working in your favor.

Now wire the increment to the moment the domain event happens — in
`Reconcile`, exactly where the `SecretCreated` event is emitted
(both are "this happened" announcements; one for humans, one for
machines):

```go
	if created {
		r.Recorder.Event(&tenant, corev1.EventTypeNormal,
			"SecretCreated", "Generated database credentials")
		fleetdbCredentialsGenerated.Inc()
	}
```

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	12.631s
```

Green — and the suite count is up by one.

## Red, green: counting scheduled backups

Same shape, second metric — and per the chapter's own rule, the test
comes first and the counter doesn't exist yet:

```go
	It("counts scheduled backups", func() {
		before := testutil.ToFloat64(fleetdbBackupsScheduled)

		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{Name: "vices", Namespace: "default"},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				DatabaseName:   "vices",
				StorageSize:    mustQuantity("1Gi"),
				BackupSchedule: "0 3 * * *",
			},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		Eventually(func() float64 {
			return testutil.ToFloat64(fleetdbBackupsScheduled)
		}, timeout, interval).Should(BeNumerically(">", before))
	})
```

(Added inside the same `Describe`; the imports don't change —
`testutil` and the rest are already there.)

```text
vet: internal/controller/metrics_test.go:37:32: undefined:
fleetdbBackupsScheduled
```

The metric joins `metrics.go` — same file, same registration:

```go
// fleetdbBackupsScheduled counts tenants that gained a backup
// schedule — each increment is one new backup CronJob.
var fleetdbBackupsScheduled = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "fleetdb_backups_scheduled_total",
		Help: "Number of backup CronJobs created for tenants.",
	},
)

func init() {
	metrics.Registry.MustRegister(fleetdbCredentialsGenerated)
	metrics.Registry.MustRegister(fleetdbBackupsScheduled)
}
```

and the increment lands in `BackupReconciler` where the
`BackupScheduled` event fires — inside the `OperationResultCreated`
branch, so reschedules and schedule *changes* don't count, only new
ones:

```go
	if operation == controllerutil.OperationResultCreated {
		r.Recorder.Event(tenant, corev1.EventTypeNormal,
			"BackupScheduled", "Scheduled database backups at "+
				tenant.Spec.BackupSchedule)
		fleetdbBackupsScheduled.Inc()
	}
```

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	12.454s
```

## End to end: the endpoint, for real

Chapter 5 scraped `controller_runtime_*` lines. One more experiment
closes the loop — same temporary `BindAddress: ":8081"` in
`suite_test.go`, same mid-run scrape, and now the domain metrics
appear alongside the framework's, off the shared registry:

```text
controller_runtime_reconcile_total{controller="backup",result="success"} 23
fleetdb_backups_scheduled_total 1
fleetdb_credentials_generated_total 9
```

Read it the way an on-call engineer would: the framework line says
the backup controller is *working* (23 successful reconciles, no
errors); the domain lines say what all that work *amounted to* — one
tenant has asked for backups, nine credentials have been generated
by the specs that ran before the scrape. Mechanism and meaning, one
endpoint, one format. Revert to `BindAddress: "0"` — the permanent
enablement (and the Prometheus that scrapes it) is Chapter 9's job.

## What we deliberately didn't do

Each omission is a rule from Chapter 5 applied, not laziness:

- **No `tenant` label.** Identity in logs, category in metrics.
  Per-tenant counts come from the API server itself (`kubectl get
  postgrestenant --no-headers | wc -l`-shaped queries, or Chapter
  9's dashboards reading status conditions) — not from metric
  cardinality.
- **No tenant-ready gauge.** Ready-or-not already has a home: the
  status condition from Chapter 3. Anyone who wants to know if
  tenant acme is ready can read that condition directly —
  duplicating it as a per-tenant metric runs straight into the
  one-label-per-tenant limit from the last section.
- **No reconcile-duration histogram.** controller-runtime already
  ships `controller_runtime_reconcile_time_seconds` per controller.
  Measuring your framework inside your own metrics duplicates it
  and risks disagreeing with it.

## How this differs from Kubebuilder

Nothing does. `prometheus.NewCounter`, the `metrics.Registry`, the
endpoint binding, and `testutil` are the prometheus client and
controller-runtime, used exactly as a Kubebuilder project uses them.
This is the chapter where the "Operator SDK's extras" from the book's
premise stay entirely out of the way — even `operator-sdk scorecard`
(Chapter 17) checks metrics by scraping the same endpoint you just
curled.

## Commit checkpoint

```
fleetdb/
├── go.mod                                # client_golang now direct (go mod tidy)
└── internal/controller/
    ├── metrics.go                        # NEW: both counters + MustRegister
    ├── metrics_test.go                   # NEW: 2 specs, testutil delta assertions
    ├── postgrestenant_controller.go      # + fleetdbCredentialsGenerated.Inc()
    ├── backup_controller.go              # + fleetdbBackupsScheduled.Inc()
    └── suite_test.go                     # BindAddress "0" (experiment reverted)
```

`make test` passes: 18 specs (2 new), 81.4% coverage, about 12
seconds. FleetDB now measures its own domain — credentials generated,
backups scheduled — on the same registry, endpoint, and format as
the framework metrics, with the increments wired to the exact
reconcile branches where the domain facts become true.

Honest gaps, as always:

- **Counters are process-local memory.** Restart the operator and
  every `fleetdb_*` starts from zero — the Prometheus model is
  *rates since last scrape*, and dashboards must be built on
  `rate()`/`increase()`, never raw values. Chapter 9's queries use
  exactly those functions.
- **The suite's counts are the suite's.** Nine credentials in one
  scrape was nine test specs' worth — numbers only mean something
  against the system that produced them.
- **The endpoint is off in tests** (port fights, same reason as
  Chapter 2) — the tests assert on the registry in-process, and the
  HTTP layer gets exercised for real in Chapter 9's kind cluster.
- **`MustRegister` panic is startup-time only.** A name collision
  introduced by a future refactor surfaces as a crash loop on
  deploy — which is loud, but is a *deployment* failure, not a
  graceful one. (A webhook could catch it earlier; that tool
  arrives in Chapter 15.)

Chapter 7 stays with observability and takes up the second pillar in
practice: what FleetDB's logs should say, what they should never
say, and where they go once the operator runs somewhere real.
