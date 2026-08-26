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
generate bundle` builds a `ClusterServiceVersion` from (Chapter 9), and
`config/scorecard` holds the scorecard test configuration (Chapter
10). Both sit empty of real content right now — we're just noting
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

One more spot needs the same fix: `create api` also scaffolded
`internal/controller/postgrestenant_controller_test.go`, which creates
a bare `PostgresTenant{}` with no spec at all — that will now fail our
`databaseName` and `storageSize` validation too. Give it a valid spec
in its `BeforeEach`:

```go
			if err != nil && errors.IsNotFound(err) {
				storageSize := resource.MustParse("1Gi")
				res := &postgresv1alpha1.PostgresTenant{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: postgresv1alpha1.PostgresTenantSpec{
						DatabaseName: "acme",
						StorageSize:  &storageSize,
					},
				}
				Expect(k8sClient.Create(ctx, res)).To(Succeed())
			}
```

(Renamed the local variable from `resource` to `res` — the original
scaffold's name collided with the `resource` package we now need to
import for `resource.MustParse`.)

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
| `config/` directories | `crd`, `default`, `manager`, `network-policy`, `prometheus`, `rbac`, `samples` | Same, plus `manifests` (bundle base, Chapter 9) and `scorecard` (Chapter 10) |
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
│   ├── postgrestenant_controller_test.go
│   ├── postgrestenant_api_test.go     # the 4 validation cases from this chapter
│   └── suite_test.go
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
