# Chapter 0: Toolchain Setup and OLM Smoke Test

## Why start here, before writing any FleetDB code

The whole point of this project is to get hands-on with the parts of
Operator SDK that sit on top of what Kubebuilder already gives you. If
the tooling underneath that layer is broken — the wrong CLI version,
a cluster that can't run it, a piece of infrastructure that never got
installed — we won't find out until several chapters in, at the worst
possible time, with FleetDB code already half-written on top of a
shaky foundation. So before writing a single line of the operator, we
prove the whole toolchain works using something disposable: a
five-minute "hello world" operator that we build, deploy, and then
throw away.

Nothing in this chapter is FleetDB-specific. There's no `PostgresTenant`
API yet, no reconciler, no tests in the TDD sense — the "test" here is
the toolchain itself.

## What is Operator SDK, and how is it different from Kubebuilder?

If you've used Kubebuilder, you already know most of what Operator SDK
does day-to-day, because **Operator SDK is built on the same
foundation** — [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime),
the Go library that actually implements the watch/reconcile loop.
`operator-sdk create api` scaffolds a Go struct for your custom
resource and a controller, exactly like `kubebuilder create api` does.
The reconcile loop, the client, the manager, the test framework
(`envtest`) — all identical, because it's the same underlying code.

Where they diverge is *scope*. Kubebuilder answers one question: "how
do I write a controller that reconciles a custom resource?" Operator
SDK answers a second question Kubebuilder deliberately leaves alone:
"how does a cluster administrator who has never seen my code discover,
install, upgrade, and remove my operator?" That second question is
what pulls in everything this book spends most of its time on:
conversion webhooks, admission webhooks, bundle images, and OLM.

We'll call out each divergence explicitly as we hit it, starting with
the very first command below.

## What is OLM?

**OLM** stands for **Operator Lifecycle Manager**. Strip away the
Kubernetes-flavored name and it's doing something you've seen before:
it's a package manager, the same category of tool as `apt` on Debian,
`yum` on Red Hat, or a Helm repository — except the "packages" it
manages are Kubernetes operators, and it runs *as* a set of operators
itself, inside your cluster.

To see why this needs its own layer, think about what `make deploy`
does in a plain Kubebuilder project: it renders a pile of YAML with
Kustomize and pipes it straight into `kubectl apply`. That works fine
when *you* are the one deploying your own operator onto a cluster you
control. It falls apart the moment someone else needs to:

- **find** your operator without you telling them the exact GitHub
  repo and `kubectl apply` incantation,
- **install** it without hand-editing RBAC and namespace YAML for
  their cluster,
- **upgrade** it later without you personally walking them through
  what changed,
- or run *several* operators, from different vendors, side by side,
  without their CRDs or RBAC colliding.

OLM exists to make all of that mechanical instead of manual. Once it's
installed on a cluster (which is what we did with `operator-sdk olm
install` a moment ago), a cluster admin installs an operator the same
way they'd `apt install` a package: point at where it lives, ask for
it, and let the package manager work out what it needs and keep it
current.

The [OLM install log](./phase0-smoke-test.log) shows several new kinds
of Kubernetes object appearing on the cluster to make this possible.
Each one maps onto a concept you already understand from ordinary
package management — here's the translation:

| OLM object | Package-manager equivalent | What it actually is |
|---|---|---|
| `CatalogSource` | An `apt` repository URL | A source that lists which operators (and which versions of each) are available to install. We installed `operatorhubio-catalog`, which points at the community catalog on [OperatorHub.io](https://operatorhub.io). |
| `ClusterServiceVersion` (**CSV**) | A single package's manifest, e.g. a `.deb` control file | A live Kubernetes object describing one specific version of one operator: the deployment to run, the RBAC it needs, and which CRDs it "owns." This is the thing OLM actually installs and upgrades. |
| `Subscription` | `apt install <package> ` (with auto-update on) | Your declaration that you want a given operator, on a given update channel, kept current automatically. You create this; OLM does the rest. |
| `InstallPlan` | `apt install --dry-run`, then approve | OLM works out everything a `Subscription` requires — the CSV, its CRDs, its RBAC — and writes it down as an `InstallPlan` *before* applying it, so there's a concrete, inspectable (and optionally manually-approved) step between "I asked for this" and "this now exists on my cluster." |
| `OperatorGroup` | N/A — no real analogue | Kubernetes-specific: tells OLM which namespaces the operators living in a given namespace are allowed to watch. Needed because several unrelated operators can share one cluster, and OLM needs to know each one's blast radius. |
| `packageserver` | The `apt` / `dpkg` binary itself | The actual OLM component that reads `CatalogSources` and exposes them as a queryable API (`kubectl get packagemanifests`) — the thing doing the work behind every row above. |

We won't touch most of these directly until Chapter 6 onward, when
FleetDB itself becomes something with a `ClusterServiceVersion` of its
own. For now, all we need is confirmation that OLM's own machinery —
`olm-operator`, `catalog-operator`, and `packageserver` — starts up
cleanly on our cluster. It did; see the log.

## What we validated

We worked through this checklist, using a kind cluster named `fleetdb`
so it doesn't collide with anything else on the machine:

1. **`operator-sdk` CLI is installed and runs.** `brew install
   operator-sdk` got us v1.42.3.
2. **A clean local cluster exists.** Deleted any pre-existing `kind`
   and `k3d` clusters, then created a fresh one:
   `kind create cluster --name fleetdb`.
3. **OLM installs cleanly on that cluster.** `operator-sdk olm
   install` — every CRD and Deployment it creates rolled out
   successfully (`olm-operator`, `catalog-operator`, `packageserver`
   all reached `Running`).
4. **The full local dev loop works end to end**, using a throwaway
   "hello world" operator (group `demo`, kind `Hello`) so we're not
   yet depending on any FleetDB code:
   - `operator-sdk init` and `operator-sdk create api` scaffold a
     working Go project.
   - `make manifests` generates a CRD from the Go struct.
   - `make docker-build` builds a controller image.
   - `kind load docker-image` gets that image onto the cluster
     without needing a registry.
   - `make install` applies the CRD; `make deploy` runs the
     controller.
   - The controller pod reaches `Running`, wins its leader-election
     lease, and starts watching `Hello` resources.
   - Creating a sample `Hello` object is picked up with no errors in
     the controller logs.

Every command and its output is preserved in
[`phase0-smoke-test.log`](./phase0-smoke-test.log). The throwaway
project itself was deleted afterward — it served its one purpose,
proving the toolchain works, and there's nothing FleetDB-specific to
keep from it.

## How this differs from Kubebuilder

| Step | Kubebuilder | Operator SDK |
|---|---|---|
| Project init | `kubebuilder init` | `operator-sdk init` — same flags, same scaffold shape, because it's the same underlying machinery. No practical difference yet; that starts in Chapter 1 when the generated `PROJECT` file and Makefile targets diverge slightly. |
| Deploying locally | `make deploy` applies raw manifests directly | Also available, and what we used here — but Operator SDK *additionally* gives you `operator-sdk olm install` and, later, `operator-sdk run bundle`, an alternate install path that goes through OLM instead of raw `kubectl apply`. |
| Packaging for others | Not provided — you write your own Helm chart or raw manifests | `operator-sdk generate bundle` produces an OLM-installable package (a CSV plus CRDs) — Chapter 6. |
| Install-time lifecycle (upgrades, dependency resolution, discovery) | Out of scope entirely | OLM's job, as described above. |

## Commit checkpoint

At the end of this chapter, the repository looks like this:

```
fleet-db/
├── book/                          # this mdBook
│   ├── book.toml
│   └── src/
│       ├── SUMMARY.md
│       ├── introduction.md
│       ├── ch00_toolchain_and_olm.md
│       ├── phase0-smoke-test.log
│       └── ch01_...ch10_...md     # stubs, filled in as we go
├── fleetdb/                       # empty — FleetDB's Go module starts in Chapter 1
└── fleetdb-operator-sdk-book-prompt.md
```

There's no FleetDB code yet — that's correct. What's committable here
is proof that `operator-sdk`, `kind`, and OLM work together on this
machine, and the first chapter of the book that says so.

Next: Chapter 1, where `fleetdb/` stops being empty — we scaffold the
`PostgresTenant` v1alpha1 API and call out exactly where
`operator-sdk init` produces something different from what
`kubebuilder init` would have.
