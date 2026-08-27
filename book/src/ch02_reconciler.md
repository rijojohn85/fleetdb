# Chapter 2: The Reconciler

## What we're building

`PostgresTenant` currently does nothing — it's a schema the API server
validates, and that's it. This chapter makes it real: given a
`PostgresTenant`, the reconciler creates a `Secret` (credentials), a
`PersistentVolumeClaim` (storage), a `Service` (network identity), and
a `StatefulSet` (the actual Postgres pod) — four owned resources from
one object, exactly as the book's opening description promised.

None of the watch/reconcile/owner-reference mechanics here are new to
you from Kubebuilder — this chapter moves fast on that ground, same as
Chapter 1. What's worth slowing down for is a couple of decisions that
are specific to *this* resource shape: which of the four owned
resources are safe to blindly overwrite on every reconcile, and which
aren't — and why that split exists.

## Turning the test suite into an integration suite

Chapter 1's tests created `PostgresTenant` objects and asserted
directly on what the API server did with them — no reconciler
involved, so `suite_test.go` only ever needed a client, not a running
controller. This chapter's tests need to assert on what the
*reconciler* does in response to a create, which means a controller
actually has to be running and watching. Add that to `BeforeSuite`,
right after `k8sClient` is built:

```go
By("starting the manager with the PostgresTenant reconciler registered")
k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
	Scheme:  scheme.Scheme,
	Metrics: metricsserver.Options{BindAddress: "0"},
})
Expect(err).NotTo(HaveOccurred())

err = (&PostgresTenantReconciler{
	Client: k8sManager.GetClient(),
	Scheme: k8sManager.GetScheme(),
}).SetupWithManager(k8sManager)
Expect(err).NotTo(HaveOccurred())

go func() {
	defer GinkgoRecover()
	err := k8sManager.Start(ctx)
	Expect(err).NotTo(HaveOccurred())
}()
```

This snippet needs two new imports in `suite_test.go` that Chapter 1's
version didn't need — `ctrl` for `ctrl.NewManager`, and
`metricsserver` for `metricsserver.Options`:

```go
import (
	// ...everything already there from Chapter 1...
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)
```

`metricsserver` is exactly the import alias `cmd/main.go` already uses
for the same package — if your editor can't resolve
`metricsserver.Options`, this import is almost certainly the missing
piece; `go build ./...` will also refuse to compile without it, so
there's no way to silently miss it.

One value in that snippet deserves its own word:
`Metrics: metricsserver.Options{BindAddress: "0"}` disables the
manager's Prometheus metrics endpoint — `"0"` is the sentinel for
"don't bind a port at all." The test environment has nothing to scrape
that endpoint with, and concurrent test runs would otherwise fight
over the same port, so off it goes.

### What `ctrl.NewManager` actually does

`ctrl.NewManager` is the one call underneath everything a controller
does, so it's worth being precise about what it actually builds,
rather than treating it as a black box you call once and forget.

A reconciler by itself is just a function: given a name (a
`Request`), go figure out what should exist and make it so. It has no
way to know *when* to run, and calling the Kubernetes API directly
every time you want to check "has anything changed?" would mean
constantly asking a busy server the same question over and over. The
manager's job is to solve exactly that problem — it is the piece that
watches Kubernetes for you and decides when your reconciler should run.

Concretely, `ctrl.NewManager` builds three things and wires them
together, and understanding what each one does explains why the whole
system behaves the way it does:

- **A cache.** The manager opens one long-lived connection per
  resource type you watch (`PostgresTenant`, `Secret`, `PersistentVolumeClaim`,
  `Service`, `StatefulSet`) and keeps an up-to-date, in-memory copy of
  every object of that type. Think of it like a subscription to a
  newsletter instead of refreshing a webpage: rather than you polling
  "did anything change?", Kubernetes pushes every change to this open
  connection as it happens, and the cache updates itself. This is also
  why `k8sManager.GetClient()` is fast — most of the time, reading
  through it (a `Get` or `List`) is just reading this local cache, not
  making a network call at all.
- **A work queue.** Every time something the manager watches changes —
  a `PostgresTenant` created, a `StatefulSet` you own edited by someone
  else — the cache adds one entry to a queue: "something about this
  object may need attention." It's deliberately just a name, not the
  full object and not a description of what changed; by the time your
  reconciler actually runs, it re-reads current state itself, so the
  queue only ever needs to say *what* might need reconciling, never
  *why*.
- **Workers that drain the queue.** The manager runs a small number of
  goroutines that continuously pull the next entry off the queue and
  call your `Reconcile` function with it. This is the actual
  watch-event → work-queue → `Reconcile()` pipeline Chapter 12 will
  trace live, hop by hop; this chapter only needs to know that it
  exists and that it's what makes `Eventually` in these tests
  necessary — reconciliation genuinely happens on a delay, on a
  separate goroutine, not synchronously inside `k8sClient.Create`.

A concrete walk-through: when a test in this chapter calls
`k8sClient.Create(ctx, tenant)`, here's what actually happens, in
order — (1) the API server accepts and stores the new `PostgresTenant`,
(2) the manager's open watch connection for `PostgresTenant` receives
that creation as a push notification and updates its cache, (3) the
cache adds `{Name: "acme", Namespace: "default"}` to the work queue,
(4) a worker goroutine picks it up and calls `Reconcile(ctx, req)`
with that name, and only then (5) your `Reconcile` code runs and
starts creating the Secret. Steps 2 through 5 all happen
asynchronously, some milliseconds after the `Create` call already
returned — which is exactly the gap `Eventually` is there to wait out.

`SetupWithManager` (already scaffolded in Chapter 1, unchanged since)
is the line that tells the manager which resource type to open that
watch connection for in the first place — `For(&postgresv1alpha1.PostgresTenant{})`
is a direct instruction: "open a cache and a watch for PostgresTenant,
and route every change to my `Reconcile` function." Nothing about the
Secret, PVC, Service, or StatefulSet types is registered as *watched*
yet in this chapter — the reconciler creates and updates them, but
doesn't yet re-run automatically if someone else edits or deletes them
out from under it. That's a real gap, and it's `.Owns(...)` — coming
in a later chapter — that closes it.

The manager starts on a goroutine specifically because `k8sManager.Start(ctx)`
blocks forever, running that watch/queue/worker loop until `ctx` is
cancelled — it's the identical call `cmd/main.go` makes to run the real
operator, just running here as a background goroutine instead of as
the whole program. `GinkgoRecover()` makes sure a panic inside that
goroutine gets reported as a test failure instead of crashing the
whole test binary silently.

This is also why Chapter 1 flagged `Eventually` as something you'd
need "constantly" starting here: every test below creates a
`PostgresTenant` and then has to wait for this now-running reconciler
to notice and react, on its own schedule.

## TDD, one owned resource at a time

Create a new file, `internal/controller/postgrestenant_controller_test.go`
— this chapter's tests are about reconciler *behavior*, which is a
different concern from Chapter 1's API *validation* tests, so it gets
its own file rather than growing `postgrestenant_api_test.go`. It's
still `package controller`, same as every other file in this
directory, so it shares that package's imports and helpers — in
particular, `mustQuantity`, the small helper Chapter 1 added to
`postgrestenant_api_test.go` to build a `*resource.Quantity` for
`StorageSize`, is reused here as-is. Don't redefine it — Go will
refuse to compile two functions with the same name in the same
package, so if you paste the snippets below and hit a "redeclared"
error, that's the tell.

```go
package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	postgresv1alpha1 "github.com/yourusername/fleetdb/api/v1alpha1"
)

var _ = Describe("PostgresTenant controller", func() {
	const timeout = 5 * time.Second
	const interval = 100 * time.Millisecond

	// It blocks go here
})
```

Every Ginkgo spec needs a top-level container to live in — `It` on its
own isn't something Ginkgo can discover and run; `Describe` is what
registers a branch of the test tree with the suite (the same role
`Describe` played in Chapter 1's `postgrestenant_api_test.go`, just a
different name — `"PostgresTenant controller"` instead of `"PostgresTenant
API validation"`, since this file is testing different behavior). Every
`It` block for the rest of this chapter goes inside this one `Describe`,
in place of the `// It blocks go here` comment.

The `var _ =` in front of `Describe` is a Go idiom worth pausing on,
because it looks like noise until you know why it's there. `Describe(...)`
returns a value we deliberately throw away (`_`) — we don't want the
value, we want the *side effect*: calling `Describe` is what registers
that branch of the test tree with Ginkgo, and because the call sits at
package level, it runs when the test package initializes. A bare
`Describe(...)` call at the top level of a file won't even compile —
Go only allows declarations there — so the assignment exists purely to
make the legal version of "call this for its side effect."

Two names used inside every `It` below need no import in this file:
`ctx` and `k8sClient` are the package-level variables `suite_test.go`
declared and wired up in `BeforeSuite` (Chapter 1). This file uses them
without declaring them because it's the same package.

`timeout` and `interval` are declared once, right inside that
`Describe`, because every `Eventually(...)` call in every `It` block
below reuses the same two values — no reason to repeat
`5*time.Second, 100*time.Millisecond` at every call site. They're
scoped inside the `Describe` rather than at the top of the file so
they can't collide with a same-named constant some other test file in
this package might declare later.

### The shape of Reconcile

Before writing any of the four owned resources, look at the frame
they'll all be added to. The scaffolded `Reconcile` stub in
`postgrestenant_controller.go` already has this signature; what it
gains in this chapter is the fetch of the tenant and, one section at
a time, a block per resource. Here's the frame:

```go
func (r *PostgresTenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var tenant postgresv1alpha1.PostgresTenant
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil // tenant deleted; nothing left to reconcile
		}
		return ctrl.Result{}, err
	}

	// one block per owned resource gets added here, one section at a time

	return ctrl.Result{}, nil
}
```

Five things in those twelve lines explain most of what a reconciler
*is*:

- `req ctrl.Request` is exactly the queue entry the manager section
  described — nothing but a name. Its type `types.NamespacedName` is a
  two-field struct (`Name`, `Namespace`), the composite key every
  namespaced Kubernetes object is addressed by. The queue entry says
  *what* might need attention; the very first `Get` fetches the live
  object to find out *why*.
- The not-found branch handles the case where the tenant was deleted
  between the queue entry landing and this `Get` running. The right
  response is to do nothing, quietly — all four owned resources carry
  owner references, so Kubernetes itself garbage-collects them when
  the tenant goes away. The reconciler has no cleanup of its own to do.
- `apierrors.IsNotFound(err)` — Kubernetes errors are matched, not
  compared. The API server wraps every failure in a `*StatusError`
  carrying a reason string, so asking "is this the not-found one?"
  goes through helpers in `k8s.io/apimachinery/pkg/api/errors`, which
  the project (following controller-runtime convention) imports under
  the alias `apierrors` — the alias exists because the package's real
  name is `errors`, which would collide with Go's standard library
  package of the same name.
- `ctrl.Result` is the reconciler's answer back to the manager: "done,
  for now." An empty `Result` means nothing more to schedule. What the
  fields actually control — explicit requeues, rate-limited retries —
  is the requeue strategy, and that's Chapter 3's entire subject. Until
  then, every path in `Reconcile` returns an empty one.
- The signature's `(ctrl.Result, error)` pair is the contract with the
  work queue: return an error and the manager re-queues that name to
  retry later; return nil and it doesn't. That's the whole retry
  mechanism — there is no other.

`postgrestenant_controller.go` needs these imports for everything this
chapter adds to it (the scaffolded file already has most of them):

```go
import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	postgresv1alpha1 "github.com/yourusername/fleetdb/api/v1alpha1"
)
```

### Red: the Secret

```go
It("creates a Secret holding the tenant's database credentials", func() {
	tenant := &postgresv1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "default"},
		Spec: postgresv1alpha1.PostgresTenantSpec{
			DatabaseName: "acme",
			StorageSize:  mustQuantity("1Gi"),
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	secret := &corev1.Secret{}
	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{
			Name: "acme-postgres", Namespace: "default",
		}, secret)
	}, timeout, interval).Should(Succeed())

	Expect(secret.Data).To(HaveKey("POSTGRES_USER"))
	Expect(secret.Data).To(HaveKey("POSTGRES_PASSWORD"))
	Expect(secret.Data["POSTGRES_DB"]).To(Equal([]byte("acme")))
})
```

One apparent inconsistency in that test is worth clearing up before
it bites: the assertions read `secret.Data`, but the builder you'll
write below sets `StringData`. Both are real Secret fields, and the
difference trips everyone exactly once. `Data` holds the final,
base64-encoded bytes (`map[string][]byte`) as the API server actually
stores them. `StringData` is a *write-only* convenience: you supply
plain strings, and the API server does the base64 encoding when the
object is created — it never reads back. So the builder writes
`StringData` because plaintext is easier to produce, and the test
reads `Data` because that's what the API server stores — the test
sees the object the way the cluster does, not the way the builder
did.

`make test` against the still-empty `Reconcile` stub:

```text
[FAILED] Timed out after 5.001s.
Expected success, but got an error:
    <*errors.StatusError | 0xc0005397c0>:
    secrets "acme-postgres" not found
```

Real red, for the reason you'd expect — nothing creates anything yet.

### Green: the Secret

Start a new file, `internal/controller/postgrestenant_resources.go` —
this is where every `desired*` builder function in this chapter lives,
kept separate from `postgrestenant_controller.go`'s orchestration
logic (`Reconcile` itself). First, two small helpers every owned
resource in this chapter needs:

```go
package controller

import (
	"crypto/rand"
	"encoding/base64"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	postgresv1alpha1 "github.com/yourusername/fleetdb/api/v1alpha1"
)

// resourceName is the name every owned resource for a tenant shares —
// "acme-postgres" for a tenant named "acme".
func resourceName(tenant *postgresv1alpha1.PostgresTenant) string {
	return tenant.Name + "-postgres"
}

// generatePassword returns a random, base64-encoded password.
func generatePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
```

`crypto/rand`, not `math/rand` — this password ends up controlling
access to a real database, so it needs to come from a
cryptographically secure random source, not the faster but predictable
generator `math/rand` provides.

With those in place, the Secret builder:

```go
func desiredSecret(tenant *postgresv1alpha1.PostgresTenant) (*corev1.Secret, error) {
	password, err := generatePassword()
	if err != nil {
		return nil, err
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName(tenant),
			Namespace: tenant.Namespace,
		},
		StringData: map[string]string{
			"POSTGRES_USER":     "postgres",
			"POSTGRES_PASSWORD": password,
			"POSTGRES_DB":       tenant.Spec.DatabaseName,
		},
	}, nil
}
```

Those three key names aren't arbitrary, and it's worth knowing why
*these* keys before moving on: the official `postgres` container
image reads its initial configuration from environment variables.
`POSTGRES_USER` names the superuser to create, `POSTGRES_PASSWORD`
sets that user's password, and `POSTGRES_DB` names the database to
create on first startup. Writing exactly those keys is what makes the
Secret and the Postgres container connect later — in the StatefulSet
section, the pod will hand this entire Secret to the container as
environment variables, and the image will find values where it knows
to look.

and in `Reconcile`:

```go
key := types.NamespacedName{Name: resourceName(&tenant), Namespace: tenant.Namespace}
var existing corev1.Secret
err := r.Get(ctx, key, &existing)
if apierrors.IsNotFound(err) {
	secret, err := desiredSecret(&tenant)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := ctrl.SetControllerReference(&tenant, secret, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, secret); err != nil {
		return ctrl.Result{}, err
	}
} else if err != nil {
	return ctrl.Result{}, err
}
```

Notice the shape: the create happens *inside* the `IsNotFound`
branch, and when the Secret already exists this code simply falls
through to whatever comes next — it does not `return`. At this
instant `Reconcile` handles only the Secret, so an early return would
be harmless, but writing the fall-through version now is deliberate:
the moment a second resource is added below, an early `return` here
would silently skip it. Keep that shape in mind; it's what the
`reconcileCreateOnce` refactor generalizes in a few sections.

`ctrl.SetControllerReference` is the same call you'd already use in
Kubebuilder — it stamps an owner reference on `secret` pointing back
at the `PostgresTenant`, which is what makes `kubectl delete
postgrestenant acme` cascade-delete its Secret, and what lets the
manager's cache route a change to the Secret back to the right
`Reconcile` call later. Nothing new here.

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	5.786s	coverage: 65.6% of statements
```

Green. One design decision worth naming before moving on: `Reconcile`
only creates the Secret if `Get` returns not-found — it never updates
one that already exists. That's deliberate, not an oversight. The
password inside a tenant's Secret may already be in use by a running
Postgres instance; regenerating it on every reconcile (which runs
repeatedly, not just once) would silently rotate a password out from
under a database that's actively using it. "Create once, never touch
again" is the correct policy specifically for a Secret full of
credentials — it won't be the right call for every resource, which
matters in the next section.

### Red, green: the PersistentVolumeClaim

Same shape, added as its own `It` block — and this time the whole
thing, because it's the template every remaining spec in the chapter
follows: declare a tenant with a *fresh name*, create it,
`Eventually`-poll for the owned resource, assert on what appeared.

```go
It("creates a PersistentVolumeClaim sized from the tenant's spec", func() {
	tenant := &postgresv1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{Name: "widgets", Namespace: "default"},
		Spec: postgresv1alpha1.PostgresTenantSpec{
			DatabaseName: "widgets",
			StorageSize:  mustQuantity("5Gi"),
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	pvc := &corev1.PersistentVolumeClaim{}
	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{
			Name: "widgets-postgres", Namespace: "default",
		}, pvc)
	}, timeout, interval).Should(Succeed())

	Expect(pvc.Spec.Resources.Requests.Storage().String()).To(Equal("5Gi"))
	Expect(pvc.Spec.AccessModes).To(ConsistOf(corev1.ReadWriteOnce))
})
```

Each tenant in this chapter gets its own name (`acme`, `widgets`,
`gizmos`, `sprockets`, `cogs`) so no two specs share cluster state —
the tests in a package share one envtest API server, and a
left-behind `acme` from a previous spec would make this spec's
assertions read someone else's resources. Later specs will abbreviate
the tenant setup to a comment, but this is what it expands to every
time.

Two vocabulary items before the implementation, since this is the
spec's first appearance: a PersistentVolumeClaim is a *request for
storage* — "I need this much, with these access rules" — that
Kubernetes satisfies with an actual volume behind the scenes. The pod
never talks to the volume directly; it references the claim, and
Kubernetes does the binding. And `ReadWriteOnce` means the volume may
be mounted read-write by a *single node* — exactly right for a
one-pod database, and wrong (restrictive) for anything that needs
many pods sharing storage.

Fails the same way the Secret test did — `persistentvolumeclaims
"widgets-postgres" not found` — and the fix is the same shape too:

```go
func desiredPVC(tenant *postgresv1alpha1.PostgresTenant) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName(tenant),
			Namespace: tenant.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: *tenant.Spec.StorageSize,
				},
			},
		},
	}
	if tenant.Spec.StorageClassName != "" {
		pvc.Spec.StorageClassName = &tenant.Spec.StorageClassName
	}
	return pvc
}
```

wired into `Reconcile` with the same get-or-create block as the
Secret, just swapping the type. One small Go detail in the middle of
the builder: `*tenant.Spec.StorageSize` dereferences the pointer that
Chapter 1's `resource.Quantity` bug taught us to use — the spec
stores a `*resource.Quantity`, and the `ResourceList` map wants the
value itself.

The reason a PVC is also
create-once, never updated, is different from the Secret's reason but
lands in the same place: most storage drivers can't shrink a volume or
change its access mode after the fact, so pretending an update would
resize it would be a lie — leaving it alone is the honest behavior.

### Stop copy-pasting: reconcileCreateOnce

At this point `Reconcile` has two blocks that are identical except for
the type and the builder function. A third (the Service, next) would
make it three — the conventional line for "this is a real pattern, not
a coincidence." Rather than paste a third copy, pull the shared shape
into one function:

```go
func (r *PostgresTenantReconciler) reconcileCreateOnce(
	ctx context.Context,
	tenant *postgresv1alpha1.PostgresTenant,
	existing client.Object,
	build func() (client.Object, error),
) error {
	key := types.NamespacedName{Name: resourceName(tenant), Namespace: tenant.Namespace}
	err := r.Get(ctx, key, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	obj, err := build()
	if err != nil {
		return err
	}
	if err := ctrl.SetControllerReference(tenant, obj, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, obj)
}
```

`Reconcile` now calls it once per resource:

```go
if err := r.reconcileCreateOnce(ctx, &tenant, &corev1.Secret{}, func() (client.Object, error) {
	return desiredSecret(&tenant)
}); err != nil {
	return ctrl.Result{}, err
}

if err := r.reconcileCreateOnce(ctx, &tenant, &corev1.PersistentVolumeClaim{}, func() (client.Object, error) {
	return desiredPVC(&tenant), nil
}); err != nil {
	return ctrl.Result{}, err
}
```

`★ Insight ─────────────────────────────────────`
This is the **Dependency Inversion Principle** showing up in ordinary
Go, not a design pattern bolted on for its own sake. `reconcileCreateOnce`
is the high-level policy ("create it if it's missing, otherwise leave
it alone") and it depends only on an abstraction — a `func() (client.Object, error)`
that knows how to build *some* resource — never on the concrete detail
of what a Secret or a PVC actually looks like. `desiredSecret` and
`desiredPVC` are low-level details that plug into that abstraction.
Notice which direction the dependency points: the policy doesn't
import knowledge of Secrets or PVCs, and adding a fourth create-once
resource later means writing one more `desired*` function and one more
three-line call in `Reconcile` — not touching `reconcileCreateOnce` at
all.
`─────────────────────────────────────────────────`

Rerun the full suite to confirm the refactor didn't silently break
anything:

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	6.222s	coverage: 66.0% of statements
```

Both tests still green. Refactoring under a passing suite, then
re-running it immediately, is the actual point of having the suite —
it's not just there to catch new bugs, it's there to prove the
cleanup didn't introduce one.

### Red, green: the Service

```go
It("creates a headless Service on the Postgres port", func() {
	// ...tenant "gizmos", created exactly like "widgets" above,
	// with DatabaseName "gizmos" and StorageSize mustQuantity("1Gi")...
	svc := &corev1.Service{}
	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{
			Name: "gizmos-postgres", Namespace: "default",
		}, svc)
	}, timeout, interval).Should(Succeed())

	Expect(svc.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
	Expect(svc.Spec.Ports).To(ConsistOf(corev1.ServicePort{
		Name: "postgres", Port: 5432,
		TargetPort: intstr.FromInt32(5432), Protocol: corev1.ProtocolTCP,
	}))
	Expect(svc.Spec.Selector).To(Equal(map[string]string{"postgrestenant": "gizmos"}))
})
```

Red the same way, then:

```go
func selectorLabels(tenant *postgresv1alpha1.PostgresTenant) map[string]string {
	return map[string]string{"postgrestenant": tenant.Name}
}

func desiredService(tenant *postgresv1alpha1.PostgresTenant) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName(tenant),
			Namespace: tenant.Namespace,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  selectorLabels(tenant),
			Ports: []corev1.ServicePort{{
				Name: "postgres", Port: 5432,
				TargetPort: intstr.FromInt32(5432), Protocol: corev1.ProtocolTCP,
			}},
		},
	}
}
```

plus one more `reconcileCreateOnce` call in `Reconcile`. `ClusterIP:
corev1.ClusterIPNone` is what makes this a *headless* Service — no
virtual IP, no load-balancing. That's the right choice here because a
StatefulSet's whole point is a stable, individually-addressable
identity per pod; load-balancing across replicas that don't exist (a
tenant runs exactly one Postgres pod) would be solving a problem this
system doesn't have. `selectorLabels` is pulled out on its own because
the StatefulSet's pod template needs to carry the *same* labels the
Service selects on — one shared source of truth for that pair, not two
copies that could quietly drift apart.

One type on the port deserves a first mention: `TargetPort:
intstr.FromInt32(5432)`. `TargetPort`'s Go type is `intstr.IntOrString`
— a Kubernetes API type that accepts either a number or a name,
because the API itself allows ports to be identified either way (named
ports matter when multiple containers share a pod). We have a plain
number, so `FromInt32` wraps it in that either-or type. You'll see
`intstr` types all over the Kubernetes Go API; now you know what the
name means.

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	6.782s	coverage: 66.0%
```

Three resources down, one to go — and the fourth is where
create-once stops being the right policy.

### Red, green: the StatefulSet, and why it needs a different policy

The StatefulSet is the piece everything else was built for, and it's
worth defining properly before writing it, because if this is your
first workload controller it isn't obvious why a Deployment won't do.
A Deployment treats its pods as interchangeable: kill one, a
replacement appears, and no promise is made about *which* pod is
which. A StatefulSet makes exactly those promises — each pod gets a
stable, predictable name (the first pod of `sprockets-postgres` is
always `sprockets-postgres-0`, across every restart), a stable DNS
identity, and a fixed pairing between a pod and its storage. Postgres
is the textbook case: the data on disk must survive restarts and stay
attached to the same logical database, and `postgres-0`'s volume must
never quietly become `postgres-1`'s. That's why the tenant's pod runs
under a StatefulSet and not a Deployment, and it's the reason the
headless Service in the last section was the right call — the two
designs are a matched pair, as you're about to see in the assertions.

```go
It("creates a single-replica StatefulSet running the requested Postgres version", func() {
	tenant := &postgresv1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{Name: "sprockets", Namespace: "default"},
		Spec: postgresv1alpha1.PostgresTenantSpec{
			DatabaseName:    "sprockets",
			StorageSize:     mustQuantity("1Gi"),
			PostgresVersion: "15",
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	sts := &appsv1.StatefulSet{}
	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{
			Name: "sprockets-postgres", Namespace: "default",
		}, sts)
	}, timeout, interval).Should(Succeed())

	Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
	Expect(sts.Spec.ServiceName).To(Equal("sprockets-postgres"))
	Expect(sts.Spec.Selector.MatchLabels).To(Equal(map[string]string{"postgrestenant": "sprockets"}))
	Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("postgres:15"))
	Expect(sts.Spec.Template.Spec.Containers[0].EnvFrom[0].SecretRef.Name).To(Equal("sprockets-postgres"))
	Expect(sts.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath).To(Equal("/var/lib/postgresql/data"))
	Expect(sts.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName).To(Equal("sprockets-postgres"))
})
```

Read those assertions as a set and notice they're not four unrelated
checks — they're the StatefulSet *wiring itself to the other three
resources this chapter already built*:

- `ServiceName: "sprockets-postgres"` — a StatefulSet requires a
  *governing Service*: a headless Service that gives its pods their
  stable DNS names. This is the Service from the previous section,
  and it's the real reason that Service had to be headless. The two
  resources were never independent; this field is the link.
- `EnvFrom[0].SecretRef.Name` — the pod injects the entire credential
  Secret as environment variables. That's the mechanism behind the
  "exactly those key names" promise from the Secret section: the
  `postgres` image reads `POSTGRES_USER`, `POSTGRES_PASSWORD`, and
  `POSTGRES_DB` from its environment, and `envFrom` is how they get
  there.
- `MountPath: "/var/lib/postgresql/data"` — the directory the
  `postgres` image stores its data in. Mounting our volume there is
  what puts the database's files on the tenant's PVC instead of the
  pod's ephemeral filesystem.
- `ClaimName: "sprockets-postgres"` — the volume *is* the PVC the
  reconciler created two sections ago, referenced by name. Pod dies,
  replacement pod starts, Kubernetes reattaches the same claim — the
  data survives.

Then a second `It`, written before any StatefulSet code exists at all,
that pins down behavior create-once *can't* give us:

```go
It("updates an existing StatefulSet's image when postgresVersion changes", func() {
	tenant := &postgresv1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{Name: "cogs", Namespace: "default"},
		Spec: postgresv1alpha1.PostgresTenantSpec{
			DatabaseName:    "cogs",
			StorageSize:     mustQuantity("1Gi"),
			PostgresVersion: "15",
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	sts := &appsv1.StatefulSet{}
	Eventually(func() string {
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "cogs-postgres", Namespace: "default",
		}, sts); err != nil {
			return ""
		}
		return sts.Spec.Template.Spec.Containers[0].Image
	}, timeout, interval).Should(Equal("postgres:15"))

	Eventually(func() error {
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "cogs", Namespace: "default"}, tenant); err != nil {
			return err
		}
		tenant.Spec.PostgresVersion = "16"
		return k8sClient.Update(ctx, tenant)
	}, timeout, interval).Should(Succeed())

	Eventually(func() string {
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "cogs-postgres", Namespace: "default",
		}, sts); err != nil {
			return ""
		}
		return sts.Spec.Template.Spec.Containers[0].Image
	}, timeout, interval).Should(Equal("postgres:16"))
})
```

A new Gomega shape appears here, twice: closures that return a value
(a `string`) instead of an `error`. `Eventually` polls whatever the
closure returns and applies the matcher to that value — an `error`
return gets the `Succeed()` matcher, a `string` return gets
`Equal(...)`. Returning `""` on a failed `Get` keeps the poll loop
alive through the window where the StatefulSet doesn't exist yet,
rather than letting a `nil`-map panic escape the closure.

The fetch-then-`Update` pattern for changing the tenant's spec —
rather than mutating a stale copy — is there because `k8sClient.Update`
requires the object's current `resourceVersion`; wrapping it in
`Eventually` handles the case where a background write (the reconciler
setting a status field, say) bumped that version between when the test
first read the tenant and when it tries to write it back — a real
retry loop, not a decoration.

Both fail — `statefulsets.apps "sprockets-postgres" not found` and
`"cogs-postgres" not found` — for the same underlying reason as
before. But the fix this time genuinely can't be `reconcileCreateOnce`:
the second test is explicitly asserting that an *existing* StatefulSet
gets its image updated, which is exactly the behavior create-once
refuses to do. This is the point where the pattern that served three
resources correctly stops applying to the fourth — worth noticing
that rather than forcing it through.

Two functions fix it, and they live in different files —
`applyStatefulSetSpec` goes in `postgrestenant_resources.go`
alongside the other builders, and `reconcileStatefulSet` goes in
`postgrestenant_controller.go` next to `reconcileCreateOnce`, with
`Reconcile` calling it right after the three create-once blocks (the
complete listing at the end of this chapter shows exactly where):

```go
func applyStatefulSetSpec(sts *appsv1.StatefulSet, tenant *postgresv1alpha1.PostgresTenant) {
	replicas := int32(1)
	labels := selectorLabels(tenant)

	sts.Spec.Replicas = &replicas
	sts.Spec.ServiceName = resourceName(tenant)
	sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
	sts.Spec.Template.ObjectMeta.Labels = labels
	sts.Spec.Template.Spec.Containers = []corev1.Container{{
		Name:  "postgres",
		Image: "postgres:" + tenant.Spec.PostgresVersion,
		EnvFrom: []corev1.EnvFromSource{{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: resourceName(tenant)},
			},
		}},
		VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/var/lib/postgresql/data"}},
	}}
	sts.Spec.Template.Spec.Volumes = []corev1.Volume{{
		Name: "data",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: resourceName(tenant),
			},
		},
	}}
}

func (r *PostgresTenantReconciler) reconcileStatefulSet(ctx context.Context, tenant *postgresv1alpha1.PostgresTenant) error {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName(tenant), Namespace: tenant.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		applyStatefulSetSpec(sts, tenant)
		return ctrl.SetControllerReference(tenant, sts, r.Scheme)
	})
	return err
}
```

`controllerutil.CreateOrUpdate` is the controller-runtime helper for
exactly this shape of problem: it fetches `sts` into place if it
exists, calls your mutate function, and either creates the result (if
it didn't exist) or patches only the fields that changed (if it did).
The mutate function is the same either way — `applyStatefulSetSpec`
doesn't know or care whether it's being called against a blank
`StatefulSet{}` or one that's been running for months. That's why the
field-writing logic was pulled into its own function (`applyStatefulSetSpec`)
instead of being written inline as part of a "build" function the way
`desiredSecret` and `desiredPVC` were — `CreateOrUpdate` needs a
function it can call *against* an existing object, not one that
returns a brand-new one.

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	6.870s	coverage: 73.0% of statements
```

All five specs green — three create-once resources through one shared
policy function, and one resource (the StatefulSet) with a genuinely
different policy because its own behavior requirement — "update the
image when the version changes" — is different in kind, not just in
type, from the other three.

### The complete Reconcile

The chapter built `Reconcile` one fragment at a time; here is the
whole thing in one piece, exactly as it stands at the commit
checkpoint:

```go
func (r *PostgresTenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var tenant postgresv1alpha1.PostgresTenant
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if err := r.reconcileCreateOnce(ctx, &tenant, &corev1.Secret{}, func() (client.Object, error) {
		return desiredSecret(&tenant)
	}); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileCreateOnce(ctx, &tenant, &corev1.PersistentVolumeClaim{}, func() (client.Object, error) {
		return desiredPVC(&tenant), nil
	}); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileCreateOnce(ctx, &tenant, &corev1.Service{}, func() (client.Object, error) {
		return desiredService(&tenant), nil
	}); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileStatefulSet(ctx, &tenant); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
```

Read top to bottom, the order isn't arbitrary either. The three
create-once resources come first because the StatefulSet *consumes*
all of them: its pod injects the Secret as environment variables,
mounts the PVC as its data directory, and takes its DNS identity from
the Service. Creating them first means that by the time the Postgres
pod starts, everything it needs already exists — get the order wrong
and you get pods stuck in `ContainerCreating` or crash-looping on
missing credentials, the classic operator bug whose cause is an
ordering choice three functions earlier.

### One honest gap: RBAC

Something the tests can't catch, and worth naming before it surprises
you: everything in this chapter runs under envtest, and envtest does
not enforce RBAC. The reconciler here `Get`s and `Create`s Secrets,
PersistentVolumeClaims, Services, and StatefulSets with total freedom
— but in a real cluster, the operator's ServiceAccount is only
allowed what RBAC says it's allowed, and the scaffolded
`postgrestenant_controller.go` carries `+kubebuilder:rbac` markers
only for `postgrestenants` itself. Deploy this reconciler as-is and
every child-resource create fails with `Forbidden`. The fix — adding
the RBAC markers so `make manifests` generates the right Roles —
happens when FleetDB first gets built and deployed as a real image,
in a later chapter. Nothing in this chapter's tests is wrong; the
test environment just can't see the problem.

## How this differs from Kubebuilder

Nothing does. Everything in this chapter — `client.Client`,
`ctrl.SetControllerReference`, `controllerutil.CreateOrUpdate`, owner
references and cascade deletion, the manager/cache/watch plumbing
`SetupWithManager` wires up — is controller-runtime, and Operator SDK's
scaffolding hands you the exact same APIs Kubebuilder would. The
divergence between the two tools lives entirely in scaffolding and
packaging (Chapters 0, 1, and everything from Chapter 14 onward), not
in how a reconciler is written.

## Commit checkpoint

```
fleetdb/
├── api/v1alpha1/          # unchanged since Chapter 1
├── internal/controller/
│   ├── postgrestenant_controller.go     # Reconcile, reconcileCreateOnce,
│   │                                     # reconcileStatefulSet, SetupWithManager
│   ├── postgrestenant_resources.go      # desiredSecret, desiredPVC,
│   │                                     # desiredService, applyStatefulSetSpec,
│   │                                     # resourceName, selectorLabels
│   ├── postgrestenant_api_test.go       # Chapter 1's validation tests
│   ├── postgrestenant_controller_test.go # this chapter's 5 specs
│   └── suite_test.go                     # now starts a real manager + reconciler
└── config/                # unchanged since Chapter 1
```

`make test` passes: 5 specs, 73% coverage. Given a `PostgresTenant`,
the cluster now actually has a Secret, a PersistentVolumeClaim, a
headless Service, and a StatefulSet running Postgres — four resources
from one object, cascade-deleted together, with one resource (the
StatefulSet) correctly distinguished from the other three by the
policy it needs.

What's still missing: nothing in `PostgresTenant.status` reflects any
of this. Right now there's no way to ask "is tenant acme actually
ready yet?" without going and checking the StatefulSet yourself. That
gap — status conditions, and the requeue strategy that keeps them
current — is Chapter 3.
