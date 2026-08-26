# Introduction

This book builds **FleetDB**, a Kubernetes operator that provisions
Postgres databases for multiple tenants. Point it at a `PostgresTenant`
resource and it creates everything a tenant needs: a `StatefulSet` to
run the database, a `Service` so other things in the cluster can reach
it, a `Secret` holding the generated password, and a
`PersistentVolumeClaim` so the data survives restarts. From there it
keeps going — a scheduled backup via `CronJob`, a metrics/logs/traces
pipeline with an auto-provisioned Grafana dashboard per tenant, and a
pgAdmin instance already wired up to that tenant's credentials. By the
end, `PostgresTenant` isn't just "give me a database" — it's "give me
a database I can operate."

That first part — "operator watches a custom resource, creates child
resources, keeps them in sync" — is not a novel idea; it's the same
pattern behind most operators you'll find in the wild. If you've
already built one with [Kubebuilder](https://book.kubebuilder.io/),
the first few chapters of this book — scaffolding the API, writing the
reconciler, adding status conditions, then a second controller for
backups — will feel familiar on purpose. We move quickly through them.

Three things come after that, and none of them get the quick
treatment.

The first is **observability**. Once FleetDB is managing real tenants,
"is it working?" stops being answerable by staring at pod status. This
book doesn't assume you've used Prometheus, Grafana, or OpenTelemetry
before, or that you already know what separates a metric from a log
from a trace. We build that vocabulary from zero, with plain-language
analogies, before writing a single line of instrumentation — then wire
FleetDB up to emit all three, and have it provision its own Grafana
dashboards as part of reconciling a tenant, the same way it provisions
a `StatefulSet`.

The second is **controller-runtime itself**. It's entirely possible to
build a working Kubebuilder operator without ever needing to know how
the manager's cache actually stays in sync with the cluster, what an
informer is doing underneath `client.Get()`, or what happens if two
replicas of your controller both think they're allowed to reconcile at
the same time. Plenty of operators run fine on autopilot without that
knowledge — right up until something goes wrong in production and the
autopilot isn't enough. This book doesn't assume that ground is
already covered either. We build a watch loop by hand with client-go's
raw informer machinery, before controller-runtime's abstraction over
it, trace one real `Reconcile()` call from watch event through to your
code, and run FleetDB with three replicas so we can kill the leader
pod ourselves and watch a new one take over the lease.

The third is what comes with **Operator SDK** specifically — the tool
we're using instead of plain Kubebuilder. It adds a whole layer that
Kubebuilder deliberately leaves out: how your operator gets
*installed*, *upgraded*, and *found* by other people, not just how it
reconciles resources. That layer is called **OLM**, the Operator
Lifecycle Manager, and the back half of this book is about the
mechanics that only show up once you take OLM seriously: converting a
custom resource from one API version to another without breaking
anyone using the old version, validating and defaulting requests
before they ever reach your reconciler, packaging an operator as
something a cluster administrator can install with one command, and
proving — automatically, in CI — that the thing you shipped actually
works.

## How this book works

Every chapter leaves the code in a state you could hand to someone
else and say "this works." We write tests before implementation, the
same discipline you'd use on any production Kubernetes controller.
Concepts are explained the first time they come up, in plain language,
before we write any code that depends on them. If a term already means
something specific in Kubebuilder or plain controller-runtime, we say
so — Operator SDK is built *on top of* controller-runtime, not instead
of it, so most of what you already know still applies. Where it
doesn't, we call it out explicitly.

Phase 0, next, doesn't touch the FleetDB code at all. It exists to
answer one question before we build anything: does the toolchain
actually work? Finding out the hard way, three chapters in, that OLM
was never installed correctly is a bad way to spend an afternoon. The
same discipline applies again right before Phase 2: before writing any
observability code, we confirm Prometheus and Grafana actually install
and stay reachable on the kind cluster, so a broken stack doesn't
surface mid-chapter either.
