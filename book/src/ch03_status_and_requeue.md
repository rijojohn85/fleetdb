# Chapter 3: Status Conditions and Requeue Strategy

## What we're building

Chapter 2 ended with a cluster that quietly converges: create a
`PostgresTenant` and, some milliseconds later, four owned resources
exist. But the reconciler is *silent* about it, in three specific ways
worth fixing, and this chapter fixes all three:

- There is no way to ask "is tenant acme actually ready?" without
  going and reading the StatefulSet yourself. `PostgresTenant.status`
  is still empty — nothing in it says anything about what the
  reconciler has done or what it's waiting for.
- The reconciler only ever runs when a `PostgresTenant` *changes*. If
  the StatefulSet's pod dies at 3 a.m., or someone deletes the
  Service, the reconciler never finds out — it isn't watching those
  objects, only the tenant.
- It writes no history. `kubectl describe` on the tenant shows nothing
  about credentials being generated or the database becoming ready.

The three fixes are a matched set, and it's worth seeing them as one
design before writing any code. Every controller has to answer three
questions: *what state is my object in* (status, reported as
**conditions** — machine-readable, read by automation and by `kubectl`
wait), *when do I wake up* (the **requeue strategy** — timers you set
yourself, plus **watches** controller-runtime sets up for you), and
*what do I tell the humans* (Kubernetes **events** — append-only,
visible in `kubectl describe`). This chapter builds all three for
FleetDB, and — fair warning — the most subtle bug a controller can
have, one it inflicts on *itself*, lives exactly where these three
meet. We'll hit it head-on in the middle.

## Status: the Ready condition

The Kubernetes convention for "what state is my object in" is a list
of **conditions**: small, uniform status entries, each with a type
name, a True/False/Unknown value, and a few standardized fields of
context. Almost every operator in the ecosystem reports at least one
`Ready` condition, because `kubectl wait --for=condition=Ready` (which
you'll use constantly from Chapter 18 onward) looks for exactly that
type name. FleetDB follows the convention: the reconciler sets a
single `Ready` condition — False with a `Provisioning` reason while
the StatefulSet's pod is starting, True once the pod is actually
ready.

Conditions aren't a freeform map. The type every serious operator
uses is `metav1.Condition` from `k8s.io/apimachinery/pkg/apis/meta/v1`,
and its fields are worth knowing by name before we write one:

- `Type` — what condition this is: `"Ready"`, `"BackupReady"`, and so
  on. An object's status carries one entry per type.
- `Status` — `"True"`, `"False"`, or `"Unknown"`. `Unknown` means "I
  haven't observed this yet" (a freshly created object whose
  controller hasn't run). Our reconciler sets `False` rather than
  `Unknown` while the pod starts, because by the time it writes the
  condition it *has* observed the StatefulSet and knows the answer:
  not ready yet, and here's why.
- `Reason` — a single, short, CamelCase word (`Provisioning`,
  `Ready`, `BackupFailed`). The API server validates the format and
  rejects anything with spaces — you'll see this in the tests only if
  you break it, but `kubectl describe` reads it constantly, so it's
  worth choosing well.
- `Message` — the human sentence that goes with the reason.
- `LastTransitionTime` — when the Status last changed. You never set
  this by hand; the helper we'll use manages it.
- `ObservedGeneration` — the metadata `Generation` this condition
  describes. More on generations below; the two-field dance here
  trips everyone once.

Storing conditions means changing the API, so — TDD — the test comes
first.

### Red: the status test

Add a new `It` to `postgrestenant_controller_test.go`, inside the
same `Describe` as Chapter 2's five specs (the complete import block
for the file as it stands *after* this chapter is printed at the end
of this chapter, so paste freely as we go). It introduces a new
Gomega shape:
`meta.FindStatusCondition` — a helper that looks up a condition by
type in a status list and returns it, or `nil` if absent.

```go
It("reports Ready=False while the StatefulSet is starting", func() {
	tenant := &postgresv1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{Name: "bolts", Namespace: "default"},
		Spec: postgresv1alpha1.PostgresTenantSpec{
			DatabaseName: "bolts",
			StorageSize:  mustQuantity("1Gi"),
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	Eventually(func() string {
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "bolts", Namespace: "default"}, tenant); err != nil {
			return ""
		}
		cond := meta.FindStatusCondition(tenant.Status.Conditions, "Ready")
		if cond == nil {
			return ""
		}
		return string(cond.Status)
	}, timeout, interval).Should(Equal("False"))

	Expect(tenant.Status.ObservedGeneration).To(Equal(tenant.Generation))

	Eventually(func() error {
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "bolts", Namespace: "default"}, tenant); err != nil {
			return err
		}
		tenant.Spec.PostgresVersion = "17"
		return k8sClient.Update(ctx, tenant)
	}, timeout, interval).Should(Succeed())

	Eventually(func() int64 {
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "bolts", Namespace: "default"}, tenant); err != nil {
			return 0
		}
		return tenant.Status.ObservedGeneration
	}, timeout, interval).Should(Equal(int64(2)))
})
```

Three things in that test deserve their own sentences before running
it:

- `meta` is `k8s.io/apimachinery/pkg/api/meta` — a package of helpers
  for working with condition lists (find, set, remove). `FindStatusCondition`
  returning `nil` for "no such condition" is why the closure returns
  `""` when it's absent: a condition that never appears must time out
  the poll loop, not panic it.
- `tenant.Generation` is an integer the API server increments every
  time an object's **spec** changes (status changes don't count). A
  fresh object has generation 1; the test's `PostgresVersion = "17"`
  edit bumps it to 2. `ObservedGeneration` is the reconciler's
  answer: "the last spec generation I fully processed." The test
  asserts the reconciler catches up — and the gap between those two
  numbers is how every tool that ever displays "change pending"
  computes it.
- The first assertion uses the `tenant` the Eventually closure left
  behind — after `Should(Equal("False"))` succeeds, the last poll's
  `Get` populated it, generation and all. That's deliberate reuse,
  not an accident.

Run it:

```text
vet: internal/controller/postgrestenant_controller_test.go:171:51:
tenant.Status.Conditions undefined (type v1alpha1.PostgresTenantStatus
has no field or method Conditions)
```

Real red, at compile time — the API has no `Conditions` field. That's
the API change; make it in `api/v1alpha1/postgrestenant_types.go` (the
file already imports `metav1` for `TypeMeta` and friends, so no import
change):

```go
// PostgresTenantStatus defines the observed state of PostgresTenant.
type PostgresTenantStatus struct {
	// ObservedGeneration is the most recently
	// reconciled generation of this PostgresTenant's Spec
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions is the set of conditions reported by the controller.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}
```

The `+kubebuilder` markers above the field are directives the CRD
generator reads. `+listType=map` with `+listMapKey=type` tells the API
server this list behaves like a map keyed on `Type` — which is what
makes conditions updatable by key instead of by array index, and what
lets `kubectl` and other clients patch one condition without knowing
the list's order. The `patchStrategy`/`patchMergeKey` tags on the Go
side are the client library's mirror of the same instruction.

Because the API type changed, two generated artifacts must be
regenerated: the CRD manifest (`config/crd/bases/...yaml`, which is
what envtest actually validates objects against — without the new
field in the schema, writes of it would be rejected) and the
`DeepCopy` methods in `zz_generated.deepcopy.go`. Both are Makefile
targets — `make manifests` and `make generate` — and `make test`
depends on both, so plain `make test` handles it; just know that's
what it's doing when you see `controller-gen` scroll past.

Now run again. Compiles — and fails at runtime, which is the *real*
red for the behavior we're building:

```text
• [FAILED] [5.003 seconds]
[FAIL] PostgresTenant controller [It] reports Ready=False while the StatefulSet is starting
[FAILED] Timed out after 5.001s.
Expected
    <string>: 
to equal
    <string>: False
```

Five seconds of polling, no condition ever appeared — correct, because
nothing writes one yet. Green time.

### Green: writing the condition

The status the reconciler needs to report is *derived from the
StatefulSet's* status: how many replicas does it want, how many are
ready. Chapter 2's `reconcileStatefulSet` returned only an `error` —
the reconciler needs the StatefulSet object back to read its status,
so the signature changes. Two small functions go in
`postgrestenant_controller.go` next to it:

```go
func (r *PostgresTenantReconciler) reconcileStatefulSet(ctx context.Context, tenant *postgresv1alpha1.PostgresTenant) (*appsv1.StatefulSet, error) {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName(tenant), Namespace: tenant.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		applyStatefulSetSpec(sts, tenant)
		return ctrl.SetControllerReference(tenant, sts, r.Scheme)
	})
	return sts, err
}

// stsReady reports whether the StatefulSet has every replica it wants
// actually running and ready.
func stsReady(sts *appsv1.StatefulSet) bool {
	return sts.Status.ReadyReplicas == *sts.Spec.Replicas
}
```

Nothing inside `reconcileStatefulSet` changed — it already holds the
up-to-date StatefulSet after `CreateOrUpdate` (which fetches the
existing object into `sts` before calling the mutate function, status
included). Returning it costs nothing and gives `Reconcile` the
readiness signal.

And the function that does the actual reporting:

```go
func (r *PostgresTenantReconciler) updateStatus(ctx context.Context, tenant *postgresv1alpha1.PostgresTenant, sts *appsv1.StatefulSet) error {
	condition := metav1.Condition{
		Type:               "Ready",
		ObservedGeneration: tenant.Generation,
		Status:             metav1.ConditionFalse,
		Reason:             "Provisioning",
		Message:            "Waiting for the Postgres replica to become ready",
	}
	if stsReady(sts) {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Ready"
		condition.Message = "Postgres replica is ready"
	}

	changed := meta.SetStatusCondition(&tenant.Status.Conditions, condition)
	if tenant.Status.ObservedGeneration != tenant.Generation {
		tenant.Status.ObservedGeneration = tenant.Generation
		changed = true
	}
	if !changed {
		return nil
	}
	return r.Status().Update(ctx, tenant)
}
```

Wired into `Reconcile` right after the StatefulSet block, with the
requeue decision — this chapter's other subject — right after it:

```go
	sts, err := r.reconcileStatefulSet(ctx, &tenant)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, &tenant, sts); err != nil {
		return ctrl.Result{}, err
	}

	if !stsReady(sts) {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return ctrl.Result{}, nil
```

(`time` joins the import block of `postgrestenant_controller.go`; the
complete file, imports and all, is printed whole later in this
chapter.)

Four decisions in those lines, each worth naming:

**`meta.SetStatusCondition` is the condition writer, and its return
value is load-bearing.** It finds an existing condition of the same
`Type` and replaces it, or appends a new one. Two things it does that
you'd get subtly wrong by hand: it only updates `LastTransitionTime`
when the *Status value actually changes* (an unchanged condition
doesn't lie about when it last transitioned), and it returns `bool` —
`true` if anything about the condition changed, `false` if the
condition it was given is byte-for-byte what was already there.

**That return value is the guard against a controller DoS-ing
itself.** Follow the loop: `Reconcile` writes the tenant's status →
the manager watches `PostgresTenant` (that's what `For(...)` in
`SetupWithManager` means) → a status write *is* a change to a watched
object → the work queue gets an entry → `Reconcile` runs again → it
writes the status again... forever, at full speed, with no failure
anywhere. The `if !changed { return nil }` short-circuit is what
breaks the cycle: once the status matches reality, the reconciler
writes nothing, the watch has nothing to report, and the loop goes
quiet. This is the general shape of a *level-triggered* reconciler —
each run compares desired against actual and does nothing when they
already agree — and the cost of forgetting it is a hot reconcile
loop. Notice it's also the same fetch-first, write-only-if-different
instinct as Chapter 2's `reconcileCreateOnce`, now applied to status.

**`ObservedGeneration` is only bumped when the reconcile got this
far.** If `reconcileStatefulSet` errors out, `Reconcile` returns
before `updateStatus`, leaving `ObservedGeneration` pointing at the
last *successfully processed* generation — which is exactly the
contract: the field answers "which spec version have I actually
acted on?", and an error means you haven't acted on this one yet.
The test's generation-bump assertion pins this catch-up behavior.

**`r.Status().Update` writes the status subresource, not the whole
object.** Chapter 1's CRD enables `subresources: status` — the API
server exposes the status as a separate write endpoint that ignores
spec changes and vice versa. Going through `r.Status()` is what
makes a status write a status write: it can't accidentally clobber
the spec, and a spec write can't accidentally clobber status. The
scaffold's RBAC markers already carry
`resources=postgrestenants/status,verbs=get;update;patch` — the
permission this call needs. envtest won't enforce it (that honest gap
from Chapter 2 still applies), but a real cluster will.

### The requeue strategy, so far

`Reconcile`'s return value is the *entire* scheduling API a controller
has, and Chapter 2 deferred it to this chapter deliberately. The four
answers it can give, exhaustively:

| Return | What the manager does |
| --- | --- |
| `ctrl.Result{}, nil` | Done. Wait for the next watch event. |
| `ctrl.Result{}, err` | Re-queue the key with exponential backoff — the rate-limited retry for *errors*. |
| `ctrl.Result{Requeue: true}, nil` | Re-queue immediately, rate-limited. Rarely what you want; if something's wrong, return the error instead. |
| `ctrl.Result{RequeueAfter: 10 * time.Second}, nil` | Wake me again in 10 seconds, no backoff. For *waiting*, not for *failing*. |

The distinction that matters most: **an error is for "something broke,
retry with backoff"; `RequeueAfter` is for "nothing is broken, but the
world isn't the way I need it yet."** Postgres pods take seconds to
minutes to start. Nothing has failed — the pod simply isn't ready —
so polling with `RequeueAfter` is honest, while returning an error
would claim a failure that didn't happen and earn escalating backoff
for it.

Why 10 seconds, and why only when not ready? The timer is a *fallback
poll* for a value that changes outside our control. Ten seconds is
slow enough to be cheap for an idle cluster and fast enough that a
reader watching `kubectl get postgrestenant -w` sees the condition
flip without suspecting it's stuck. And requeuing *only while not
ready* means a healthy tenant costs nothing — no timer spins, no API
calls happen, the controller is exactly as quiescent as Chapter 2's.

One honesty note about testing this: an integration test can't
directly observe a `RequeueAfter` — it's a scheduling decision inside
the manager, not cluster state. What the tests *can* pin is the
condition the decision is computed from, which the spec just did, and
the flip that a *watch* makes possible, which is next — because it
turns out the timer is the wrong tool for the main event.

## Who wakes the reconciler?

Chapter 2's manager section established the mechanism: watches push
changes into a work queue, and `For(&postgresv1alpha1.PostgresTenant{})`
is the line that requests a watch. Which means everything the
reconciler has ever done was triggered by a change to a
`PostgresTenant` itself. The StatefulSet's pod becoming ready is a
change to a *StatefulSet* — and so far, nobody's watching those.

controller-runtime's answer is `.Owns(...)`: additional watches on
owned object types, with the routing handled automatically. Every
child resource already carries an owner reference pointing at its
tenant (`ctrl.SetControllerReference`, Chapter 2) — `Owns` reads
those references and routes each child's changes back to its owner's
reconcile key. The builder line is the entire mechanism; no callbacks,
no manual queueing.

The pod becoming ready is exactly such a change: it updates the
StatefulSet's status. Red test first — and this one proves both the
condition flip *and* the watch in a single spec:

### Red, green: Ready=True

```go
It("reports Ready=True once the StatefulSet reports a ready replica", func() {
	tenant := &postgresv1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{Name: "hammers", Namespace: "default"},
		Spec: postgresv1alpha1.PostgresTenantSpec{
			DatabaseName: "hammers",
			StorageSize:  mustQuantity("1Gi"),
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	sts := &appsv1.StatefulSet{}
	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{
			Name: "hammers-postgres", Namespace: "default",
		}, sts)
	}, timeout, interval).Should(Succeed())

	Eventually(func() error {
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "hammers-postgres", Namespace: "default",
		}, sts); err != nil {
			return err
		}
		sts.Status.Replicas = 1
		sts.Status.ReadyReplicas = 1
		return k8sClient.Status().Update(ctx, sts)
	}, timeout, interval).Should(Succeed())

	Eventually(func() string {
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "hammers", Namespace: "default"}, tenant); err != nil {
			return ""
		}
		cond := meta.FindStatusCondition(tenant.Status.Conditions, "Ready")
		if cond == nil {
			return ""
		}
		return string(cond.Status)
	}, timeout, interval).Should(Equal("True"))

	events := &corev1.EventList{}
	Eventually(func() bool {
		if err := k8sClient.List(ctx, events, client.InNamespace("default")); err != nil {
			return false
		}
		for _, ev := range events.Items {
			if ev.InvolvedObject.Name == "hammers" && ev.Reason == "TenantReady" {
				return true
			}
		}
		return false
	}, timeout, interval).Should(BeTrue())
})
```

(The `TenantReady` event assertion at the bottom belongs to a later
section — the reconciler doesn't emit events yet. Leave it in; it
will pass once the events section lands, and the import it needs —
`"sigs.k8s.io/controller-runtime/pkg/client"` as `client`, for
`client.InNamespace` — is already in the file's import block by then.
If you're following strictly test-by-test, comment it out until then;
the rest of the spec is this section's red.)

Two things in the middle of that spec need explaining, because
envtest is doing something unusual:

- **There is no kubelet in envtest.** The StatefulSet's pod will never
  actually start — envtest is only the API server and etcd, no node
  components, so `ReadyReplicas` would stay 0 forever. The test
  *simulates* the cluster's work: it writes the StatefulSet's status
  the way a real node's kubelet eventually would.
- **`k8sClient.Status().Update`** is the same status-subresource write
  the reconciler itself uses, exercised from the test side, and it
  needs both fields set: the API server validates that
  `status.readyReplicas` never exceeds `status.replicas`, and we
  found that out the hard way — the first draft of this test set
  only `ReadyReplicas` and was rejected:

```text
StatefulSet.apps "hammers-postgres" is invalid: status.readyReplicas:
Invalid value: 1: cannot be greater than status.replicas
```

  A useful reminder that envtest's API server is a real API server,
  validation and all.

Against the current implementation the spec fails in its final
`Eventually` — the condition never flips:

```text
• [FAILED] [5.108 seconds]
[FAIL] PostgresTenant controller [It] reports Ready=True once the StatefulSet reports a ready replica
[FAILED] Timed out after 5.000s.
Expected
    <string>: False
to equal
    <string>: True
```

Read that failure carefully, because it's the *whole point*: the
condition logic is already correct (it says `False`, not missing —
`updateStatus` ran). What's missing is a **wake-up**. The status
write to the StatefulSet happened, nothing was watching StatefulSets,
no reconcile ran, and the timer would eventually have caught up —
ten seconds later, which is to say too late for a five-second
timeout, and too slow for every 3 a.m. pod failure this operator
will ever have. The fix is one line in `SetupWithManager`:

```go
	return ctrl.NewControllerManagedBy(mgr).
		For(&postgresv1alpha1.PostgresTenant{}).
		Owns(&appsv1.StatefulSet{}).
		Complete(r)
```

Green:

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	7.282s
```

Now the full cycle works: pod becomes ready → StatefulSet status
update → watch fires → work queue → reconcile → `updateStatus` sees
`ReadyReplicas == 1` → condition True. The `RequeueAfter` timer
remains in place as a safety net (and is what drives the condition in
envtest, where no watch-compatible status changes happen until the
test makes one) — but the watch is the fast path, and that
division of labor is exactly how production controllers are built:
**event-driven when possible, timer-driven when not.**

### Red, green: recreating deleted children

Same mechanism, different wake-up worth testing — because it's the
other behavior `.Owns` buys and the one your users will exercise by
accident:

```go
It("recreates the Service if someone deletes it", func() {
	tenant := &postgresv1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{Name: "screws", Namespace: "default"},
		Spec: postgresv1alpha1.PostgresTenantSpec{
			DatabaseName: "screws",
			StorageSize:  mustQuantity("1Gi"),
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	svc := &corev1.Service{}
	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{
			Name: "screws-postgres", Namespace: "default",
		}, svc)
	}, timeout, interval).Should(Succeed())

	Expect(k8sClient.Delete(ctx, svc)).To(Succeed())

	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{
			Name: "screws-postgres", Namespace: "default",
		}, &corev1.Service{})
	}, timeout, interval).Should(Succeed())
})
```

Delete a child with no `Owns(&corev1.Service{})` and the reconciler
never hears about it — the deletion isn't a `PostgresTenant` change —
so the Service stays gone:

```text
• [FAILED] [5.113 seconds]
[FAIL] PostgresTenant controller [It] recreates the Service if someone deletes it
[FAILED] Timed out after 5.000s.
Expected success, but got an error:
    services "screws-postgres" not found
```

Green — the other three owned types join the watch, closing
Chapter 2's named gap for good:

```go
	return ctrl.NewControllerManagedBy(mgr).
		For(&postgresv1alpha1.PostgresTenant{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.StatefulSet{}).
		Complete(r)
```

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	7.546s
```

One honest caveat before moving on, because "the operator recreates
deleted children" sounds more reassuring than it is. Recreation is
*creation*: `reconcileCreateOnce` sees not-found and builds a fresh
object. For the Service or the StatefulSet, that's exactly what you
want — they're derived state, fully specified by the tenant. For the
**Secret**, it means a *new password*: the regenerated credentials
won't match what the running Postgres has in its environment. For the
**PVC**, a fresh claim means a fresh, empty volume — the deleted
claim's data isn't coming back. `.Owns` gives you self-healing for
derived state, not resurrection for stateful one-ofs. (What to do
about the Secret — rotation, adoption, `nomatch` policies — is a
later chapter's problem; the point here is to know the boundary.)

## Events: the human-readable history

Conditions and `ObservedGeneration` are the machine-readable record:
current state, overwritten in place, one snapshot per object. What
they don't give anyone is *what happened when* — and that's what
Kubernetes **events** are for: small, timestamped, append-only notes
attached to an object, surfaced by the tooling your users already run
(`kubectl describe postgrestenant acme` lists them at the bottom) and
by every alerting pipeline that watches events. Two events at the
same object with the same reason get *aggregated* — the API server
collapses repeats into one entry with a count, so emitting an event
on every reconcile doesn't flood anything, though the convention
stands: emit when something *happens*.

The mechanism is one interface, `record.EventRecorder` from
`k8s.io/client-go/tools/record`, with one method you'll use:

```go
r.Recorder.Event(&tenant, corev1.EventTypeNormal, "SecretCreated", "Generated database credentials")
```

The four arguments: the object the event attaches to (the tenant —
events *about* the tenant's children still belong to the tenant),
the event type (`corev1.EventTypeNormal` for expected things,
`corev1.EventTypeWarning` for problems), a short CamelCase reason
(same conventions as a condition's reason), and a human message.
You never construct a recorder: the manager builds one
(`GetEventRecorderFor("postgrestenant")` — the string is the
*source component* recorded on every event) and hands it to the
reconciler, the same way it handed over the client and scheme.

`★ Insight ─────────────────────────────────────`
That "you never construct one" is the **Dependency Inversion
Principle** again, from the other side. In Chapter 2, the
reconciler's policy depended on an abstraction (`build func()
(client.Object, error)`) that concrete builders plugged into. Here
the reconciler depends on the *interface* `record.EventRecorder`
without knowing or caring what's behind it — in production, a
broadcaster writing to the API server; in principle, a fake that
records events in memory for tests. The manager assembles the
concrete dependency and injects it at construction time, which is
why the `Recorder` field is filled in `suite_test.go` and `cmd/main.go`
rather than created anywhere inside the controller package.
`─────────────────────────────────────────────────`

Which events should FleetDB emit? The chapter's design: one event per
created child resource, `StatefulSetCreated`/`StatefulSetUpdated`
from the `CreateOrUpdate` result, and `TenantReady`/`TenantNotReady`
when the condition flips — *only* when it flips, thanks to the same
`changed` guard that keeps the reconcile loop quiet. Red first:

```go
It("records an event when it creates the Secret", func() {
	tenant := &postgresv1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{Name: "nuts", Namespace: "default"},
		Spec: postgresv1alpha1.PostgresTenantSpec{
			DatabaseName: "nuts",
			StorageSize:  mustQuantity("1Gi"),
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	events := &corev1.EventList{}
	Eventually(func() bool {
		if err := k8sClient.List(ctx, events, client.InNamespace("default")); err != nil {
			return false
		}
		for _, ev := range events.Items {
			if ev.InvolvedObject.Name == "nuts" && ev.Reason == "SecretCreated" {
				return true
			}
		}
		return false
	}, timeout, interval).Should(BeTrue())
})
```

Three new names, all small: `corev1.EventList` is the list type for
event objects; `InvolvedObject` is the reference each event carries
back to the object it's about (here, the tenant); and
`client.InNamespace("default")` is a list option — the same
namespace-scoping `kubectl get events -n default` does. The closure
returns a `bool` instead of an `error` or a value: `Eventually`
applies `BeTrue()` to it, same three-way shape as the string-returning
closures in Chapter 2.

```text
• [FAILED] [5.004 seconds]
[FAIL] PostgresTenant controller [It] records an event when it creates the Secret
[FAILED] Timed out after 5.001s.
```

No events exist — nothing emits them. The implementation needs four
edits, all in `postgrestenant_controller.go` unless named otherwise.

**First, the recorder field** on the reconciler, plus the RBAC marker
for writing events (the first marker in this project whose group is
the empty string — core objects like `Event` live in the core API
group, which RBAC spells as `groups=""`):

```go
type PostgresTenantReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=postgres.rijojohn.xyz,resources=postgrestenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=postgres.rijojohn.xyz,resources=postgrestenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=postgres.rijojohn.xyz,resources=postgrestenants/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
```

That adds `"k8s.io/client-go/tools/record"` to the imports. And — the
honest-gap echo — envtest does not enforce RBAC, so a missing events
marker would sail through this chapter's tests and fail with
`Forbidden` on a real cluster. The marker costs one line now; the
failure it prevents costs a debugging session in Chapter 18.

**Second, the wiring.** In `suite_test.go`, the reconciler literal
gains the recorder:

```go
	err = (&PostgresTenantReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorderFor("postgrestenant"),
	}).SetupWithManager(k8sManager)
```

and `cmd/main.go` gets the identical three lines at its construction
site, using `mgr` instead of `k8sManager`:

```go
	if err := (&controller.PostgresTenantReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("postgrestenant"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PostgresTenant")
		os.Exit(1)
	}
```

**Third, `reconcileCreateOnce` reports whether it created.** Events
record things that *happened*, and the function currently discards
exactly that bit. The signature widens from `error` to `(bool, error)`,
each branch returning whether it acted:

```go
func (r *PostgresTenantReconciler) reconcileCreateOnce(
	ctx context.Context,
	tenant *postgresv1alpha1.PostgresTenant,
	existing client.Object,
	build func() (client.Object, error),
) (bool, error) {
	key := types.NamespacedName{Name: resourceName(tenant), Namespace: tenant.Namespace}
	err := r.Get(ctx, key, existing)
	if err == nil {
		return false, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, err
	}

	obj, err := build()
	if err != nil {
		return false, err
	}
	if err := ctrl.SetControllerReference(tenant, obj, r.Scheme); err != nil {
		return false, err
	}
	return true, r.Create(ctx, obj)
}
```

**Fourth, the emission sites.** In `Reconcile`, each call site now
captures the bool and emits (showing the Secret block in full — the
PVC and Service blocks are identical in shape, each with its own
reason and message, and all three appear in the complete listing
below):

```go
	created, err := r.reconcileCreateOnce(ctx, &tenant, &corev1.Secret{}, func() (client.Object, error) {
		return desiredSecret(&tenant)
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if created {
		r.Recorder.Event(&tenant, corev1.EventTypeNormal, "SecretCreated", "Generated database credentials")
	}
```

`reconcileStatefulSet` reads the operation result `CreateOrUpdate`
already returns — created and updated are both worth an event (the
update one is what a version change like the `cogs` test triggers):

```go
	operation, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		applyStatefulSetSpec(sts, tenant)
		return ctrl.SetControllerReference(tenant, sts, r.Scheme)
	})
	switch operation {
	case controllerutil.OperationResultCreated:
		r.Recorder.Event(tenant, corev1.EventTypeNormal, "StatefulSetCreated", "Created the Postgres StatefulSet")
	case controllerutil.OperationResultUpdated:
		r.Recorder.Event(tenant, corev1.EventTypeNormal, "StatefulSetUpdated", "Updated the Postgres StatefulSet")
	}
```

and `updateStatus` emits on transitions — placed *after* the
`if !changed` guard so it fires once per flip, never per reconcile.
It needs the previous state, so a lookup joins the top of the
function (this is the complete final version):

```go
func (r *PostgresTenantReconciler) updateStatus(ctx context.Context, tenant *postgresv1alpha1.PostgresTenant, sts *appsv1.StatefulSet) error {
	condition := metav1.Condition{
		Type:               "Ready",
		ObservedGeneration: tenant.Generation,
		Status:             metav1.ConditionFalse,
		Reason:             "Provisioning",
		Message:            "Waiting for the Postgres replica to become ready",
	}
	if stsReady(sts) {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Ready"
		condition.Message = "Postgres replica is ready"
	}

	prev := meta.FindStatusCondition(tenant.Status.Conditions, "Ready")
	wasReady := prev != nil && prev.Status == metav1.ConditionTrue

	changed := meta.SetStatusCondition(&tenant.Status.Conditions, condition)
	if tenant.Status.ObservedGeneration != tenant.Generation {
		tenant.Status.ObservedGeneration = tenant.Generation
		changed = true
	}
	if !changed {
		return nil
	}

	switch {
	case condition.Status == metav1.ConditionTrue && !wasReady:
		r.Recorder.Event(tenant, corev1.EventTypeNormal, "TenantReady", condition.Message)
	case condition.Status == metav1.ConditionFalse && wasReady:
		r.Recorder.Event(tenant, corev1.EventTypeWarning, "TenantNotReady", condition.Message)
	}

	return r.Status().Update(ctx, tenant)
}
```

The symmetry is deliberate: becoming ready is `Normal` (`TenantReady`),
*becoming* not-ready after having been ready is `Warning`
(`TenantNotReady`) — a running database going down is exactly what
warnings are for, and the initial Unknown→False provisioning flip
deliberately emits nothing, because the per-resource create events
already told that story seconds earlier.

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	7.447s
```

Green — including the `TenantReady` assertion that's been waiting in
the hammers spec since the watch section.

## The complete Reconcile

The chapter changed `Reconcile`'s middle and tail; here is the whole
function as it stands at the commit checkpoint:

```go
func (r *PostgresTenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var tenant postgresv1alpha1.PostgresTenant
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	created, err := r.reconcileCreateOnce(ctx, &tenant, &corev1.Secret{}, func() (client.Object, error) {
		return desiredSecret(&tenant)
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if created {
		r.Recorder.Event(&tenant, corev1.EventTypeNormal, "SecretCreated", "Generated database credentials")
	}

	created, err = r.reconcileCreateOnce(ctx, &tenant, &corev1.PersistentVolumeClaim{}, func() (client.Object, error) {
		return desiredPVC(&tenant), nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if created {
		r.Recorder.Event(&tenant, corev1.EventTypeNormal, "PersistentVolumeClaimCreated", "Requested storage for the database")
	}

	created, err = r.reconcileCreateOnce(ctx, &tenant, &corev1.Service{}, func() (client.Object, error) {
		return desiredService(&tenant), nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if created {
		r.Recorder.Event(&tenant, corev1.EventTypeNormal, "ServiceCreated", "Created the headless database Service")
	}

	sts, err := r.reconcileStatefulSet(ctx, &tenant)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, &tenant, sts); err != nil {
		return ctrl.Result{}, err
	}

	if !stsReady(sts) {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}
```

The order from Chapter 2 still holds — Secret, PVC, Service before
the StatefulSet that consumes them all — and the three additions
append in dependency order: the StatefulSet must exist (its status
read), then the status derived from it is written, then the requeue
decision falls out of the same readiness answer. One function, one
pass, four kinds of output: resources created, status updated, an
event or two emitted, and a scheduling decision returned.

For completeness, the file's full import block after this chapter —
`time`, `meta`, and `record` are the three this chapter added:

```go
import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	postgresv1alpha1 "github.com/yourusername/fleetdb/api/v1alpha1"
)
```

and the test file's, which gained `meta` and `client` this chapter:

```go
import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	postgresv1alpha1 "github.com/yourusername/fleetdb/api/v1alpha1"
)
```

## How this differs from Kubebuilder

Nothing does. `metav1.Condition` and `meta.SetStatusCondition` are
apimachinery; `record.EventRecorder` is client-go;
`RequeueAfter`, `.Owns(...)`, and the status subresource client are
controller-runtime. Every API this chapter touched is exactly what a
Kubebuilder-scaffolded project uses, down to the `GetEventRecorderFor`
call. (One placeholder worth replacing with something real someday:
Operator SDK's own docs and `operator-sdk scorecard` — Chapter 17 —
assume conditions named exactly like these, which is one more reason
the `Ready` convention isn't optional.)

## Commit checkpoint

```
fleetdb/
├── api/v1alpha1/
│   ├── postgrestenant_types.go        # status gains Conditions []metav1.Condition
│   └── zz_generated.deepcopy.go       # regenerated (make generate)
├── cmd/
│   └── main.go                        # Recorder: mgr.GetEventRecorderFor(...)
├── config/crd/bases/                  # CRD regenerated with conditions (make manifests)
├── internal/controller/
│   ├── postgrestenant_controller.go   # updateStatus, stsReady, Recorder field,
│   │                                  # Owns watches, events, RequeueAfter
│   ├── postgrestenant_resources.go    # unchanged from Chapter 2
│   ├── postgrestenant_api_test.go     # Chapter 1's validation tests
│   ├── postgrestenant_controller_test.go  # 9 controller specs
│   └── suite_test.go                  # Recorder: k8sManager.GetEventRecorderFor(...)
└── config/                            # (crd regenerated; rbac will regenerate too)
```

`make test` passes: 13 specs (9 controller, 4 API), 86.0% coverage,
about 7.4 seconds. Your coverage number will drift a little with
your own Chapter 1 test details — the shape is what matters. Given a
`PostgresTenant`, `kubectl get postgrestenant acme -o jsonpath='{.status.conditions}'`
now answers "is it ready, and why not"; `kubectl describe` tells the
story of how it got there; a deleted child rebuilds itself; a pod
that dies flips the condition back to False and fires a Warning —
all without a single timer being the primary mechanism for any of it.

Honest gaps this chapter left open, all named in passing and none
testable in envtest:

- RBAC is not enforced here: the events marker, the status marker,
  and every child-resource verb are untested promises until Chapter
  18 deploys the operator for real.
- `RequeueAfter` itself is a scheduling decision no integration test
  can observe directly; the tests pin the condition it's computed
  from.
- There is no kubelet in envtest, so `Ready=True` was reached by
  simulating the StatefulSet's status write the way a real node
  would.
- The Secret self-heals into a *new password*, and a deleted PVC
  self-heals into an *empty volume* — recreation is not restoration.

Chapter 4 stays in Phase 1 and puts a second controller alongside
this one — scheduled backups — where two reconcilers share one
manager, and the requeue strategy stops being a ten-second poll and
becomes an actual schedule.
