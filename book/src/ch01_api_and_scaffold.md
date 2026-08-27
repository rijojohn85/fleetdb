# Chapter 1: The PostgresTenant API and Project Scaffold

## What we're building

By the end of this book, a `PostgresTenant` resource asks for a fully
operated tenant database: storage, a scheduled backup, metrics, logs,
traces, a Grafana dashboard, and a pgAdmin instance. All of that starts
from one object with four fields. This chapter defines those four
fields, gets the project scaffolded, and — because we write tests
first — walks into a real bug in the process, the kind that only shows
up once you actually run the test instead of assuming the validation
marker does what its name suggests.

You already know what a CRD and a controller are from Kubebuilder, so
we won't re-explain the watch/reconcile pattern here. This chapter
moves fast on familiar ground and slows down only where Operator SDK
does something different, or where the API design itself has a
non-obvious wrinkle worth stopping for.

## Scaffolding the project

```console
$ operator-sdk init --domain=fleetdb.io --repo=github.com/yourusername/fleetdb
```

Swap in your own module path for `github.com/yourusername/fleetdb` —
wherever this project will actually live.

This is the same shape as `kubebuilder init --domain=... --repo=...`,
and it produces the same kind of scaffold: a `go.mod`, a `Makefile`, a
`cmd/main.go` that wires up a controller-runtime manager, and a
`PROJECT` file tracking what's been scaffolded so far. Open that
`PROJECT` file and there's the first concrete difference:

```yaml
domain: fleetdb.io
layout:
- go.kubebuilder.io/v4
plugins:
  manifests.sdk.operatorframework.io/v2: {}
  scorecard.sdk.operatorframework.io/v2: {}
projectName: fleetdb
repo: github.com/yourusername/fleetdb
version: "3"
```

The `layout` is identical to what plain Kubebuilder would write —
`go.kubebuilder.io/v4` — because Operator SDK's Go scaffolding *is*
Kubebuilder's scaffolding underneath. The new part is `plugins`. Those
two entries are Operator SDK bolting extra scaffolding generators onto
the same base: one that knows how to produce OLM-installable manifests
later, one that knows how to produce scorecard tests later. Nothing
observable happens yet — we won't touch either until Phase 4 — but
they're already registered, waiting.

## Scaffolding the API

```console
$ operator-sdk create api \
    --group=postgres --version=v1alpha1 --kind=PostgresTenant \
    --resource --controller
```

Again, identical invocation to `kubebuilder create api`. It generates:

- `api/v1alpha1/postgrestenant_types.go` — the Go structs for the CRD
- `api/v1alpha1/groupversion_info.go` — scheme registration boilerplate
- `internal/controller/postgrestenant_controller.go` — an empty
  reconciler stub (Chapter 2's job)
- `internal/controller/suite_test.go` — an envtest harness: it starts a
  real (if minimal) Kubernetes API server in-process, with our CRD
  installed, so tests can create real objects and get real API-server
  validation back. If you've used `envtest` from Kubebuilder before,
  this file will look exactly the same, because it is.

It also updates `config/`, and this is where the second difference
shows up. List the top-level directories under `config/`:

```console
$ find config -maxdepth 1 -type d
config/crd
config/default
config/manager
config/manifests      # <- not in a plain Kubebuilder project
config/network-policy
config/prometheus
config/rbac
config/samples
config/scorecard      # <- not in a plain Kubebuilder project
```

`config/manifests` and `config/scorecard` are what those two plugins
from the `PROJECT` file just did. A plain Kubebuilder scaffold stops at
`config/crd`, `config/rbac`, `config/manager`, and so on — everything
needed to `kubectl apply` the operator directly. Operator SDK adds two
more directories that only matter once you're packaging the operator
for OLM: `config/manifests` becomes the base that `operator-sdk
generate bundle` builds a `ClusterServiceVersion` from (Chapter 16), and
`config/scorecard` holds the scorecard test configuration (Chapter
17). Both sit empty of real content right now — we're just noting
where they came from before they matter.

## Designing PostgresTenantSpec

Open `api/v1alpha1/postgrestenant_types.go`. The scaffolded
`PostgresTenantSpec` has one placeholder field (`Foo string`) with a
comment telling you to replace it. Here's what it becomes:

```go
// PostgresTenantSpec defines the desired state of PostgresTenant.
type PostgresTenantSpec struct {
	// DatabaseName is the name of the Postgres database created for this
	// tenant. It must be a valid Postgres identifier: lowercase letters,
	// digits, and underscores, starting with a letter.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	// +kubebuilder:validation:MaxLength=63
	DatabaseName string `json:"databaseName"`

	// StorageSize is how much storage to request for the tenant's
	// PersistentVolumeClaim, e.g. "10Gi".
	// +kubebuilder:validation:Required
	StorageSize resource.Quantity `json:"storageSize"`

	// StorageClassName selects which StorageClass to provision the PVC
	// from. If empty, the cluster's default StorageClass is used.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// PostgresVersion is the Postgres image tag to run.
	// +kubebuilder:default="16"
	// +optional
	PostgresVersion string `json:"postgresVersion,omitempty"`
}

// PostgresTenantStatus defines the observed state of PostgresTenant.
type PostgresTenantStatus struct {
	// ObservedGeneration is the most recently reconciled generation of
	// this PostgresTenant's spec.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}
```

`Status` stays almost empty on purpose — `ObservedGeneration` is enough
to say "the controller has seen this spec" for now. Real status
conditions (`Ready`, `Provisioning`, etc.) are Chapter 3's job; adding
them before we have a reconciler that sets them would just be dead
code.

Four fields is deliberately small. `DatabaseName` and `StorageSize` are
required — you can't provision a database with no name or a PVC with
no size. `StorageClassName` and `PostgresVersion` are optional, one
left to the cluster's default, the other defaulted to `"16"` via the
`+kubebuilder:default` marker, which — like everything under
`+kubebuilder:validation:*` — controller-gen reads to build the CRD's
OpenAPI schema. If you've written Kubebuilder markers before, all of
these are the exact same vocabulary; nothing here is Operator
SDK-specific.

## What envtest and Ginkgo actually are

Before writing any tests, it's worth being clear on what the two tools
in `suite_test.go` actually do, since neither is a normal part of
writing Go.

**envtest** solves a specific problem: to properly test a controller,
you need something to react against — an actual Kubernetes API server
that accepts objects, validates them, and stores them. Spinning up a
whole real cluster for every test run would be slow and would tie your
tests to whatever cluster happens to be available. envtest instead
starts the real `kube-apiserver` and `etcd` binaries — the same code a
real cluster runs — as plain local processes, for the lifetime of the
test run only. It's "real" in the sense that it's the genuine API
server doing genuine validation; it's "fake" in the sense that nothing
it creates actually *does* anything. If a test creates a Pod, the API
server records that a Pod object exists — no container ever starts, no
node ever schedules it. That's exactly the right amount of realism for
testing a controller: your reconciler's whole job is talking to the
API server, so testing against a real one (even an inert one) tells
you your code is correct, without needing a real cluster.

**Ginkgo** is a different way of structuring test files, used together
with its assertion library, **Gomega**. Plain Go tests
(`func TestFoo(t *testing.T)`) work fine for small, single-step checks,
but controller tests tend to be longer stories: create an object, then
check that something else reacted correctly, possibly after some
delay. Ginkgo gives that story a shape instead of one flat function:

```go
var _ = Describe("PostgresTenant API validation", func() {
    It("rejects a PostgresTenant with no storageSize", func() {
        // create the object, then assert on what came back
    })
})
```

`Describe` names the thing under test, `It` names one expected
behavior. This is purely organizational — it reads like a spec of
behavior, and when a test fails, Ginkgo's output shows you exactly
which named case broke, not just a function name. Assertions read as
`Expect(x).To(Equal(y))` (Gomega), rather than Go's `if x != y {
t.Fatal(...) }`.

The one Ginkgo/Gomega feature this book leans on repeatedly is
`Eventually`. Because a controller reacts to changes on its own
schedule — you create an object, and *something else* (your
reconciler, running in the background) reacts to it a moment later —
you often can't assert immediately after creating something. Instead:

```go
Eventually(func() error {
    return k8sClient.Get(ctx, key, &sts)
}, timeout, interval).Should(Succeed())
```

This retries the check on an interval until it passes or times out.
It's the biggest mental shift from ordinary unit testing: you're
testing that something becomes true, not that a function call
immediately returns the right value. This chapter's tests don't need
`Eventually` yet — the API server itself rejects or accepts an object
immediately on `Create`, no reconciler involved — but Chapter 2's
tests, which check what the reconciler creates in response, will use
it constantly.

### A closer look at the scaffolded `suite_test.go`

`create api` doesn't just leave you a note saying "use envtest and
Ginkgo" — it writes a complete, working file that does the wiring for
you: `internal/controller/suite_test.go`. This is the one file in the
whole scaffold worth reading line by line before you write a single
test, because every test file in this chapter (and every chapter after
it) depends on the pieces it sets up. Here's what `create api` puts in
it, trimmed to the parts that matter:

```go
package controller

import (
	"context"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	postgresv1alpha1 "github.com/yourusername/fleetdb/api/v1alpha1"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = postgresv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).NotTo(HaveOccurred())
})
```

Walking through it piece by piece:

**The package-level variables** (`cfg`, `k8sClient`, `testEnv`, `ctx`,
`cancel`) exist because Ginkgo test files are split across multiple
`_test.go` files that all need to share the same envtest environment
and the same client. `postgrestenant_api_test.go` from the next
section uses `k8sClient` and `ctx` without ever creating them itself —
they're set up once, here, and every test in the package reuses them.
Think of it like a shared workbench: one file sets up the workbench
before anyone starts working, and every other file just walks up and
uses it.

**`TestControllers`** is the one and only function Go's own `go test`
knows how to run directly — it's a completely ordinary Go test
function, nothing Ginkgo-specific about its signature. All it does is
hand control to Ginkgo (`RunSpecs`), which then goes and finds every
`Describe`/`It` block in the package and runs them. This is the bridge
between "plain Go testing" and "Ginkgo testing" — without this
function, `go test` would have no idea Ginkgo tests exist at all.

**`BeforeSuite`** is Ginkgo's name for "run this once, before any test
in this package starts." Inside it:

- `testEnv = &envtest.Environment{...}` describes what kind of fake API
  server to start. `CRDDirectoryPaths` tells it where to find our
  `PostgresTenant` CRD's schema (the same YAML that `make manifests`
  generates from our validation markers) — without this, the fake API
  server would have no idea what a `PostgresTenant` even is, and every
  `Create` call would fail with "unknown resource." `ErrorIfCRDPathMissing`
  just means: fail loudly and immediately if that directory doesn't
  exist, rather than silently starting a server with no CRDs installed
  and producing confusing failures ten steps later.
- `testEnv.Start()` actually launches the `kube-apiserver` and `etcd`
  binaries as background processes and returns `cfg`, a `*rest.Config`
  — this is the exact same kind of object `kubectl` builds from your
  `~/.kube/config` file when it talks to a real cluster. It's the
  address-and-credentials bundle needed to talk to *this* particular
  API server.
- `postgresv1alpha1.AddToScheme(scheme.Scheme)` solves a different
  problem: a generic Kubernetes client knows how to talk to built-in
  types like Pod and Service out of the box, but it has never heard of
  `PostgresTenant` — that's a type we invented. "Adding it to the
  scheme" is registering our type with the client library so that when
  it needs to turn a `PostgresTenant` Go struct into JSON to send over
  the wire (or turn JSON back into a struct), it knows how. Skip this
  line and every `k8sClient.Create(ctx, tenant)` call would fail before
  it even reached the network, because the client wouldn't recognize
  the type being passed to it.
- `client.New(cfg, ...)` builds `k8sClient`, the actual object our
  tests call `.Create()` and `.Get()` on. `cfg` says *where* to send
  requests; the scheme says *what* the client is allowed to send.

**`AfterSuite`** is the mirror image — "run this once, after every
test in the package has finished" — and its job is cleanup:
`cancel()` stops the shared context, and `testEnv.Stop()` shuts down
the `kube-apiserver`/`etcd` processes `Start()` launched. Without this,
every test run would leave orphaned processes running in the
background.

Put together: `BeforeSuite` builds one fake cluster and one client
pointed at it, shared by the whole test package; `AfterSuite` tears
both down; `TestControllers` is the thin adapter that lets plain
`go test` discover and run all of it. This is also why the tests in
this chapter don't need any per-test setup for the API server itself —
`postgrestenant_api_test.go`'s `It` blocks jump straight to building a
`PostgresTenant` and calling `k8sClient.Create(ctx, tenant)`, because
the workbench is already there waiting.

## Writing the tests first

The validation markers above are a claim: "the API server will reject
a `PostgresTenant` that's missing a required field." Don't take that
on faith — write the test that exercises it, using the same `envtest` +
Ginkgo harness `create api` already scaffolded in `suite_test.go`.

Create `internal/controller/postgrestenant_api_test.go`:

```go
package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	postgresv1alpha1 "github.com/yourusername/fleetdb/api/v1alpha1"
)

var _ = Describe("PostgresTenant API validation", func() {

	It("rejects a PostgresTenant with no databaseName", func() {
		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "missing-db-name",
				Namespace: "default",
			},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				StorageSize: resource.MustParse("1Gi"),
			},
		}
		err := k8sClient.Create(ctx, tenant)
		Expect(err).To(HaveOccurred())
	})

	It("rejects a databaseName that isn't a valid Postgres identifier", func() {
		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bad-db-name",
				Namespace: "default",
			},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				DatabaseName: "1-not-valid",
				StorageSize:  resource.MustParse("1Gi"),
			},
		}
		err := k8sClient.Create(ctx, tenant)
		Expect(err).To(HaveOccurred())
	})

	It("rejects a PostgresTenant with no storageSize", func() {
		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "missing-storage-size",
				Namespace: "default",
			},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				DatabaseName: "acme",
			},
		}
		err := k8sClient.Create(ctx, tenant)
		Expect(err).To(HaveOccurred())
	})

	It("accepts a valid PostgresTenant and defaults postgresVersion to 16", func() {
		tenant := &postgresv1alpha1.PostgresTenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "acme-prod",
				Namespace: "default",
			},
			Spec: postgresv1alpha1.PostgresTenantSpec{
				DatabaseName: "acme",
				StorageSize:  resource.MustParse("10Gi"),
			},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		created := &postgresv1alpha1.PostgresTenant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name:      "acme-prod",
			Namespace: "default",
		}, created)).To(Succeed())

		Expect(created.Spec.PostgresVersion).To(Equal("16"))
	})
})
```

Four cases: two required-field rejections, one pattern rejection, one
happy path that also checks the default got applied. Generate the CRD
from our markers and run it:

```console
$ make manifests
$ make test
```

Three of the four pass. The fourth doesn't:

```text
[FAILED] rejects a PostgresTenant with no storageSize
Expected an error to have occurred.  Got:
    <nil>: nil
```

The test that leaves `StorageSize` unset and expects the API server to
reject it — doesn't get rejected. The CRD schema is right; check
`config/crd/bases/postgres.fleetdb.io_postgrestenants.yaml` and
`storageSize` is correctly listed under `required`. The bug isn't in
the schema. It's in what the Go client actually sends over the wire.

### Why the required check doesn't fire

`resource.Quantity` is a **struct**, not a primitive, and Go's
`encoding/json` only honors `omitempty` for values it can cheaply tell
are "empty" on its own: zero numbers, empty strings, nil pointers,
empty slices and maps. It does not know how to ask an arbitrary struct
whether it's empty — so a plain, non-pointer `Quantity` field is
**always** written to the JSON body, `omitempty` tag or not. Its zero
value doesn't marshal to nothing; it marshals to the string `"0"`,
because `Quantity` defines its own `MarshalJSON`. From the API server's
point of view, the key `storageSize` was present with a valid value.
Required was satisfied — just not the way we meant.

Two ways to actually enforce this. You could add a CEL validation rule
that rejects a quantity that isn't positive; that's the more general
tool and it's worth knowing about, but it's more machinery than this
field needs. The direct fix is simpler: make the field a **pointer**,
so a genuinely absent value has something to be — `nil` — that
`omitempty` can actually detect.

```go
	// StorageSize is how much storage to request for the tenant's
	// PersistentVolumeClaim, e.g. "10Gi".
	// +kubebuilder:validation:Required
	StorageSize *resource.Quantity `json:"storageSize,omitempty"`
```

Changing a field's type invalidates the generated `DeepCopy` method
controller-runtime relies on to safely hand out cached objects, so
regenerate it before rebuilding:

```console
$ make generate    # regenerates zz_generated.deepcopy.go
$ make manifests    # regenerates the CRD
```

Skipping `make generate` here fails loudly — the compiler will tell
you `zz_generated.deepcopy.go` still expects the old, non-pointer type
— so there's no way to silently miss this step.

Update the test file's two now-pointer-typed call sites
(`resource.MustParse("1Gi")` → a `*resource.Quantity`) with a small
helper:

```go
// mustQuantity returns a pointer to a parsed resource.Quantity. StorageSize
// is a pointer specifically so a genuinely missing value can be represented
// as nil and omitted from JSON — see the note above on why a plain
// (non-pointer) Quantity can't be validated as required.
func mustQuantity(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}
```

and use `mustQuantity(...)` everywhere the tests previously called
`resource.MustParse(...)` directly for `StorageSize`.

One more file needs attention: `create api` also scaffolded
`internal/controller/postgrestenant_controller_test.go`. It creates a
bare `PostgresTenant{}` with no spec at all, which fails our new
`databaseName` and `storageSize` validation just like the case above —
but patching its spec isn't enough to make it pass, because that
file's real job is a different one entirely: it calls the reconciler's
`Reconcile` function directly and asserts on what it did. Our
reconciler is still the empty stub `create api` generated — giving it
real behavior is Chapter 2's job, not this one. Fixing the spec would
just trade one failure for another, less honest one: a test that looks
like it's checking reconciler behavior while actually checking
nothing, because there's no behavior yet to check.

The right move is to delete the file outright, not patch around it:

```console
$ rm internal/controller/postgrestenant_controller_test.go
```

This chapter's test suite should only claim to cover what this chapter
actually built — API validation. Chapter 2 will write its own
reconciler test from scratch, TDD-style, at the point where there's an
actual reconciler to test against. Keeping a stale, half-working test
file around now would just be dead weight that happens to pass, which
is worse than no test at all: it looks like coverage that isn't real.

Run it again:

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	6.9s	coverage: 80.0% of statements
```

All green. The lesson stands on its own: a validation marker's name is
a description of intent, not a guarantee — it's only as good as
whether your client can actually produce the value the marker is
checking for. That's true in plain Kubebuilder too; it just happened
to surface here.

## How this differs from Kubebuilder

| Area | Kubebuilder | Operator SDK |
|---|---|---|
| `init` / `create api` | Same commands, same flags | Identical — same underlying scaffolding engine |
| `PROJECT` file | `layout` only | Adds a `plugins` section (`manifests.sdk.operatorframework.io/v2`, `scorecard.sdk.operatorframework.io/v2`) registering OLM-oriented generators for later |
| `config/` directories | `crd`, `default`, `manager`, `network-policy`, `prometheus`, `rbac`, `samples` | Same, plus `manifests` (bundle base, Chapter 16) and `scorecard` (Chapter 17) |
| `api/`, `internal/controller/`, `envtest` harness | — | Identical; controller-runtime underneath doesn't change |
| Makefile | Standard `build`/`test`/`deploy`/`manifests` targets | Same, plus `bundle`, `bundle-build`, `catalog-build` targets (unused until Phase 4) |

## Commit checkpoint

```
fleetdb/
├── PROJECT
├── go.mod / go.sum
├── Makefile
├── cmd/
│   └── main.go
├── api/v1alpha1/
│   ├── postgrestenant_types.go       # DatabaseName, StorageSize (*Quantity),
│   │                                  # StorageClassName, PostgresVersion
│   ├── groupversion_info.go
│   └── zz_generated.deepcopy.go
├── internal/controller/
│   ├── postgrestenant_controller.go   # still an empty stub — Chapter 2
│   ├── postgrestenant_api_test.go     # the 4 validation cases from this chapter
│   └── suite_test.go                  # scaffolded postgrestenant_controller_test.go deleted — see above
└── config/
    ├── crd/bases/postgres.fleetdb.io_postgrestenants.yaml
    ├── manifests/                     # present, empty of real content
    ├── scorecard/                     # present, empty of real content
    └── ...
```

`make test` passes. `PostgresTenant` has a schema the API server
enforces, and we have evidence — not an assumption — that it works.
The reconciler is still an empty stub; that's next.

Next: Chapter 2, where the reconciler stops being a stub and starts
actually owning a `StatefulSet`, `Service`, `Secret`, and `PVC` per
tenant.
