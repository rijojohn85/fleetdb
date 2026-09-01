# Chapter 7: Logs in Practice

## What we're building

Chapter 5 taught what logs *are*: records of things that happened,
written at the moment they happened, with structured fields a machine
can filter. Chapter 6 gave FleetDB its first deliberate output
channels — metrics counters wired to domain events. But one channel
has been left to accident: the reconciler's own log. The tenant
controller logs nothing at all today; every log line you've seen
this whole book came from the framework (the `controller=`,
`reconcileID=` prefixes) or from one of *your* debugging detours.

This chapter makes logging deliberate. Three questions, one chapter:

- **What should FleetDB log?** (Less than you'd think — and this
  chapter's first test will fail until the logging is disciplined.)
- **What must FleetDB *never* log?** Credentials, for a start —
  FleetDB generates database passwords, and this chapter adds a test
  whose entire job is to fail if one ever reaches the log.
- **Where do the logs go** once the operator runs somewhere real?
  (`kubectl logs`, node rotation, and the capture lesson Chapter 5
  stumbled into.)

And the tool for all three is new, and worth the price of admission
on its own: **a test that reads the operator's log output and makes
assertions about it.**

## The inventory: what a first draft looks like

Before disciplining the log, write the first draft most of us would
write. Two lines in `Reconcile` — a friendly "I'm working" at the
top, and a full report when requeueing:

```go
func (r *PostgresTenantReconciler) Reconcile(ctx context.Context,
	req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("Name", req.Name,
		"Namespace", req.Namespace)
	log.Info("starting reconciliation")
	// ... fetch of the tenant unchanged ...

	if !stsReady(sts) {
		log.Info("statefulset not ready, requeuing...",
			"statefulset", sts)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	// ...
```

The first line needs an import this chapter adds —
`logf "sigs.k8s.io/controller-runtime/pkg/log"` — and uses
`FromContext(ctx)` for the first time, though it's been sitting in
the machinery since Chapter 2: the manager puts its logger *into*
every context it hands `Reconcile`, and `FromContext` retrieves it.
(The `.WithValues(...)` part is Chapter 5's structured fields — the
manager already stamps `controller=`, `reconcileID=` and friends;
`Name` and `Namespace` join them on every line this logger writes.)

That's it — that's the whole draft. It compiles, it's friendly, and
it is exactly how log monsters are born. Run the suite and count:

```text
grep -c "starting reconciliation" → 44
grep -c "statefulset not ready"   → 41
```

Forty-four "I'm starting" lines for one test run — because
`Reconcile` runs *constantly* (Chapter 3's requeue timer, every
watch event, every status write). And the requeue line is worse
than repetitive. Here is one of those 41 lines, in full:

```text
2026-08-31T11:27:58+05:30	INFO	statefulset not ready,
    requeuing...	{"controller": "postgrestenant", ..., "Name":
    "acme", "Namespace": "default", "statefulset":
    "&StatefulSet{ObjectMeta:{acme-postgres  default
    393eb7b1-343f-465b-8e1a-1752e1c8bab5 216 1 2026-08-31 ...
    [the entire StatefulSet object: metadata, spec, template,
    containers, volumes, status — 3,751 characters in total]"}
```

Three thousand seven hundred and fifty-one characters to say "not
ready yet." Three things are wrong with this draft, and the rest of
the chapter fixes all three with tests:

1. **Per-reconcile chatter at `Info` level** — lines that fire on
   every single pass drown the lines that matter.
2. **Whole objects logged** (`"statefulset", sts`) — an object dump
   is unreadable, bloated, and dangerous in a way the password
   section will make visceral.
3. Nothing is *wrong* yet, but the draft is one careless line away
   from logging the credential Secret's contents — and nothing
   would catch it.

## A test harness that reads your logs

Every assertion in this chapter is about *what got logged* — so the
first build is a way to get the logs out of a reconcile and into a
test. The suite's manager can't help: its logger writes into
Ginkgo's buffer, shared with every other test. Instead, call
`Reconcile` **by hand**, with a logger that writes into a `bytes.Buffer`
we own. New file, `internal/controller/logging_test.go`:

```go
package controller

import (
	"bytes"
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	postgresv1alpha1 "github.com/yourusername/fleetdb/api/v1alpha1"
)

// reconcileWithLogBuffer calls Reconcile by hand, with a logger
// that writes into a buffer, so tests can assert on exactly what
// was logged. The suite's manager is untouched.
func reconcileWithLogBuffer(name string) (string, error) {
	var buf bytes.Buffer
	logger := zap.New(zap.WriteTo(&buf))
	ctx := log.IntoContext(context.Background(), logger)

	r := &PostgresTenantReconciler{
		Client:   k8sClient,
		Scheme:   scheme.Scheme,
		Recorder: record.NewFakeRecorder(32),
	}
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: name, Namespace: "default",
		},
	}
	_, err := r.Reconcile(ctx, req)
	return buf.String(), err
}
```

Four new pieces, each small:

- **`zap.New(zap.WriteTo(&buf))`** builds a logger whose output
  lands in `buf`. (Notice what Chapter 5's capture odyssey already
  taught: `zap.New()` with no options defaults to *production* mode
  — JSON lines, info level and up. The suite's logger uses
  `UseDevMode(true)`, which is why its output looked like
  tab-separated console text. Same library, two costumes.)
- **`log.IntoContext(ctx, logger)`** is the inverse of the
  `FromContext(ctx)` call in the first draft: it puts a logger
  *into* a context. When the hand-built `Reconcile` call runs
  `logf.FromContext(ctx)`, it finds this logger and writes to this
  buffer. The manager does precisely the same trick with its own
  logger on every real reconcile.
- **`record.NewFakeRecorder(32)`** satisfies the `Recorder` field
  with a fake that collects events into an in-memory channel
  (events aren't what we're testing, but `Reconcile` would panic on
  a nil recorder the first time it emits one).
- **A hand-built `ctrl.Request`** — the same two-field struct the
  work queue delivers, constructed in the open instead of arriving
  from a queue. Calling `Reconcile` directly like this is a
  genuinely useful tool beyond this chapter: it's the difference
  between "the manager eventually called my code" and "I called my
  code and can inspect everything it did." Chapter 12 leans on the
  same move.

One warning the tests themselves taught me: **the suite's manager is
still running and watching** — it reconciles the same tenants these
tests create, racing the hand-built call. Both reconcilers writing
children at once occasionally produces `AlreadyExists` errors from
whichever loses. That's why every use of the harness below is
wrapped in `Eventually`: retry the hand-built reconcile until it
succeeds cleanly (once the children exist, `CreateOrUpdate` and
`reconcileCreateOnce` are no-ops — the level-triggered design
already guarantees a second pass is safe and quiet).

## Red, green: chatter and object dumps

The first spec pins both draft sins:

```go
var _ = Describe("FleetDB logging", func() {
	const timeout = 5 * time.Second
	const interval = 100 * time.Millisecond

	It("keeps per-reconcile chatter out of the info level", func() {
		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rulers", Namespace: "default",
			},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				DatabaseName: "rulers",
				StorageSize:  mustQuantity("1Gi"),
			},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		var logs string
		Eventually(func() error {
			var err error
			logs, err = reconcileWithLogBuffer("rulers")
			return err
		}, timeout, interval).Should(Succeed())

		Expect(logs).NotTo(ContainSubstring(
			"starting reconciliation"))
		Expect(logs).NotTo(ContainSubstring("ObjectMeta{"))
	})

	// the password spec arrives in the next section
})
```

The assertions read the buffer like an on-call engineer would:
`"starting reconciliation"` must not appear at info level, and
`ObjectMeta{` — the fingerprint of a logged Go object — must not
appear at all. Against the first draft:

```text
[FAILED] Expected
    <string>: {"level":"info","ts":"2026-09-01T08:32:22+05:30",
    "msg":"starting reconciliation","Name":"rulers",
    "Namespace":"default"}
to not contain
    <string>: starting reconciliation
```

Real red, in production-JSON costume — the draft's chatter, caught
by the test. The fix is two small changes, and one new idea.

**The idea: verbosity levels.** zap (behind logr) gives every logger
a ladder of levels. `Info` sits at the default rung — visible unless
someone deliberately turns logging down. Above the default, logr
offers *verbosity levels*: `log.V(1)` returns a logger one rung up,
and `V(1).Info(...)` writes at what every tool treats as *debug*.
The practical effect: **a demoted line is not deleted — it just
stops appearing unless someone asks for detail.** In `cmd/main.go`,
the scaffold already wires a `--zap-log-level` flag (via
`opts.BindFlags`); a production operator runs quiet by default and
can be turned up mid-incident without a redeploy.

**The fix.** The "starting" line and the requeue line are
per-reconcile lifecycle — exactly the demote-to-debug class. The
requeue line also loses the object dump, keeping the two numbers
that matter:

```go
	log.V(1).Info("starting reconciliation")
```

and, in the requeue branch:

```go
	if !stsReady(sts) {
		log.V(1).Info("statefulset not ready, requeuing",
			"ready", sts.Status.ReadyReplicas,
			"wanted", *sts.Spec.Replicas)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
```

The requeue line is worth comparing side by side. Before: one
3,751-character object dump per line. After: the same fact, four
words and two numbers, and the *identifiers* (which the manager
stamps anyway) instead of the *contents*. When you're debugging at
3 a.m., `"ready": 0, "wanted": 1` is the sentence you need; the
container image name and owner references were never the story.

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	13.293s
```

Green. And to prove the demoted lines aren't gone — just quiet — run
the suite the way you've been running it all along (the suite's
logger runs in dev mode, which shows everything):

```text
2026-09-01T08:36:19+05:30	DEBUG	starting reconciliation
    {"controller": "postgrestenant", ..., "reconcileID":
    "3e1eecb2-...", "Name": "gaskets", "Namespace": "default"}
```

Same line, now stamped DEBUG — visible when debugging, invisible in
the steady hum.

## Red: the password guard

Now the rule with no exceptions. **FleetDB generates passwords. A
password that reaches a log line is in log files, log aggregators,
screenshots, and incident tickets forever — it has stopped being a
secret.** (And the opposite trap is worth naming: the password's
*home*, the Secret's `Data`, is protected by RBAC and by the fact
that nobody logs it — `kubectl get secret -o yaml` shows it only to
people who already had cluster permission.)

The guard test makes that rule mechanical. Same harness, one more
spec: reconcile a fresh tenant, read the generated Secret, and
require that its password never crossed into the log buffer:

```go
	It("never logs the generated password", func() {
		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gaskets", Namespace: "default",
			},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				DatabaseName: "gaskets",
				StorageSize:  mustQuantity("1Gi"),
			},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		var logs string
		Eventually(func() error {
			var err error
			logs, err = reconcileWithLogBuffer("gaskets")
			return err
		}, timeout, interval).Should(Succeed())

		var secret corev1.Secret
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Name: "gaskets-postgres", Namespace: "default",
			}, &secret)
		}, timeout, interval).Should(Succeed())

		Expect(logs).NotTo(ContainSubstring(
			string(secret.Data["POSTGRES_PASSWORD"])))
	})
```

(`secret.Data` is the base64-decoded map Chapter 2 introduced —
`POSTGRES_PASSWORD`'s value as a plain string. That string is
precisely what must never appear in `logs`.)

Run it: **green, immediately** — the reconciler doesn't log
passwords today. A test that passes the day it's written is a
*guard*, and the honest question about any guard is: *would it
actually fire?* A smoke alarm you've never heard ring is a
decoration until you test it. So test it — with a fire.

### The fire drill

Here is the well-meaning line a future you (or a colleague) might
add while debugging — "let's just log what we generated, once":

```go
	var leaked corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Name: resName, Namespace: tenant.Namespace,
	}, &leaked); err == nil {
		log.Info("generated credentials",
			"password", string(leaked.Data["POSTGRES_PASSWORD"]))
	}
```

Plausible, innocent, and exactly what the guard exists for. Run the
suite:

```text
[FAIL] FleetDB logging [It] never logs the generated password
...
2026-09-01T08:36:19+05:30	INFO	generated credentials
    {"controller": "postgrestenant", ..., "password":
    "lx9-bNHyXx47aILUCXTR5KX6fYtTrvYU"}
```

There it is: a freshly generated database password, printed in
plain text, structured and machine-readable, stamped with a
timestamp it will carry into every log aggregation system forever.
(That password was generated by a test run and died with it — yours
will differ. The danger isn't this value; it's how casually it
appeared.) Remove the lines and the guard goes green:

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	13.155s
```

The drill earns one more story, because the first version of the
drill *passed with the leak still in the code*. The leak was placed
inside the `if created` branch — and the suite's manager usually
created the Secret first, so the hand-built reconcile saw
`created == false`, never executed the leak, and the buffer stayed
clean. A green guard, a live leak, no alarm. Two lessons, both
permanent:

- **Guards must be drilled against a real fire, not assumed.** This
  one needed the leak to fire on *every* pass, not just the create
  pass, before it caught anything.
- **A test that can silently pass is not a guard yet.** When you
  write one, break the code on purpose and watch it fail. If it
  doesn't, the test — not the rule — needs fixing.

### The never-log list

The password is the loudest member of a family. The rule that
generalizes: **log identifiers and counts, never contents.**

- Credentials of every kind — passwords, keys, tokens, connection
  strings. FleetDB has exactly one today; password rotation (a
  later chapter) will multiply the ways one can leak.
- Whole Kubernetes objects — the Chapter-3-through-6 versions of
  this book contained `kubectl get -o yaml`-grade dumps in logs;
  any object that *references* a Secret (the StatefulSet's `envFrom`)
  is one format change away from logging the Secret's neighbors.
- Anything a person would be uncomfortable seeing in an incident
  ticket — because that's where logs go.

## Levels in practice

FleetDB now has an explicit level policy, and it fits in a table:

| Level | What belongs there | FleetDB's examples |
| --- | --- | --- |
| `Error` | Something failed; a human should look | CronJob create rejected (the Chapter 4 `spec.schedule` wall — correctly `Error`), status update failures |
| `Info` | Domain events, meaningful at a glance | `secret created`, `BackupScheduled` — the same moments the events and metrics fire |
| `V(1)` (debug) | Per-reconcile lifecycle | `starting reconciliation`, `statefulset not ready, requeuing` |
| never | Contents of anything | passwords, object dumps |

One pattern in that table deserves its own sentence: the `Info`
lines and the Chapter 6 metrics and the events often fire on the
*same line of code* — `secret created` logs, `Inc()`s the counter,
and emits the event together. That's not duplication; each channel
serves a different reader (the grep-hunting human, the rate graph,
`kubectl describe`). When a domain fact happens, announce it on
every channel; when a per-reconcile detail happens, whisper it at
`V(1)`.

## Where logs go

Everything above is about what's *said*. The other half of "in
practice" is what happens after `Info` returns — and Chapter 5's
capture saga was a miniature of the real answer: **a log line
crosses several sinks between your code and a reader, and each one
can drop it.**

- **The logger's sink.** In the suite it's a buffer or Ginkgo's
  writer. In `cmd/main.go` it's stderr — the convention every
  container runtime captures. The scaffolded `zap.Options` block
  (`Development: true`, `opts.BindFlags(...)`) is where the costume
  is chosen: dev mode prints human-friendly console text; without
  it, zap emits the JSON lines you saw in this chapter's red output
  — machine-parseable, which is what log collectors want.
  `--zap-log-level` (already wired by the scaffold) flips between
  the hum and the firehose.
- **`kubectl logs`.** The first stop on a real cluster — pod-scoped,
  current-container only, with `-f` to follow, `--previous` for the
  *last* incarnation (a crash-looping pod's final words), and
  `--since=1h` to bound the firehose. Chapter 18 uses all three.
- **Node rotation.** The kubelet keeps a bounded window per
  container (by default ~10 MB, rotated a handful of times) —
  **when the window slides, old lines are gone.** `kubectl logs`
  is a view of that window, not an archive. Anything you might
  need later must be collected somewhere off the node.
- **Collectors.** The off-node home — Fluentd/Fluent Bit shipping
  to storage, Loki or an ELK stack for querying. FleetDB's own
  Chapter 9 dashboards will read metrics from Prometheus; logs
  deserve the same treatment (a collector shipping each node's
  window into something queryable). The operator's obligation is
  narrower and already met: emit structured, leveled, JSON-parseable
  lines to stderr, with the identifiers a collector needs to
  correlate (`controller`, `name`, `reconcileID`).

## How this differs from Kubebuilder

Nothing does. `logf.FromContext`, `logr` verbosity levels, zap
options and flags, the `DEBUG`/`INFO`/`ERROR` mapping — all
controller-runtime and client-go, identical in a Kubebuilder
project. Even the logger-into-the-context trick this chapter's
tests use is the same mechanism Kubebuilder's envtest suites use
when they want test-assertable logs.

## Commit checkpoint

```
fleetdb/
└── internal/controller/
    ├── postgrestenant_controller.go   # logf logger in Reconcile;
    │                                  # chatter + requeue line at V(1);
    │                                  # requeue logs counts, not objects
    ├── logging_test.go                # NEW: log-buffer harness + 2 specs
    └── (everything else unchanged)
```

`make test` passes: 20 specs (2 new), 86.5% coverage, about 13
seconds. The reconciler now logs deliberately: lifecycle at `V(1)`,
domain facts at `Info`, failures at `Error`, contents never — and
two tests enforce the policy, one of which has already proven it
will fire.

Honest gaps, as always:

- **The guard covers the paths the test exercises.** A password
  could still leak through an error string, a panic dump, or a
  future code path the guard doesn't drive. The drill proved the
  alarm works; it didn't fireproof the building.
- **The demoted lines still run.** `V(1)` costs a little formatting
  even when suppressed (logr short-circuits most of it, and zap's
  level check makes the rest cheap — but a line that logs nothing
  costs nothing at all; don't log per-item in loops).
- **envtest has no collector, no rotation, no `kubectl logs`.** The
  destination half of this chapter is prose until Chapter 18 puts
  the operator on a real cluster.
- **`reconcileID` correlates within one reconcile call.** One
  operation spanning two components — controller and CronJob pod —
  needs the third pillar. That's Chapter 8.

Phase 2 continues: next stop, traces in practice.
