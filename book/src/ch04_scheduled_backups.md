# Chapter 4: Scheduled Backups — a Second Controller

## What we're building

FleetDB provisions tenants, reports their state, and heals their
children — but it never backs anything up. This chapter adds
scheduled backups, and the interesting decision isn't the backup
itself; it's *where the code lives*. The chapter title says it: a
**second controller**.

Until now, one reconciler has done everything, and every line of it
was about one concern: making the cluster match a tenant's spec. A
backup is a different concern with a different lifecycle: it doesn't
exist until someone asks for it, it disappears when they stop asking,
and its schedule has nothing to do with StatefulSet readiness. Bolting
it onto the existing `Reconcile` would work — and would be the wrong
shape. So this chapter builds `BackupReconciler` in its own file with
its own work queue, registered alongside the tenant controller in the
same manager. That's the **Single Responsibility Principle** at the
largest scale the book has used it: not one function with one job, but
one *controller* with one job. Two controllers also get two work
queues, which means a flood of backup errors can never starve tenant
reconciliation or vice versa — retry backoff is per-controller.

The backup itself is delegated to the right tool. The "when" — cron
scheduling, missed-run handling, retrying failed runs — is exactly
what Kubernetes' built-in `CronJob` resource exists to do, and
reimplementing a scheduler inside a reconciler would be a mistake.
So FleetDB's job is narrow: watch tenants, and make sure a CronJob
exists (or doesn't) to match each tenant's wishes. The "what" is a
`pg_dump` run by a plain Postgres container.

One honest limitation up front: the dump goes to the pod's stdout, so
backups land in container logs. That's obviously not durability —
real backup storage is a later chapter's problem. This chapter's
subject is the *controller pattern*; the mechanism being managed is
kept deliberately boring.

## The backupSchedule field

A tenant asks for backups through a new optional spec field — a cron
expression. Empty means "no backups requested", which will matter a
lot in the middle of this chapter.

Test first. It won't compile:

```go
It("creates a CronJob when the tenant requests backups", func() {
	tenant := &postgresv1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{Name: "drills", Namespace: "default"},
		Spec: postgresv1alpha1.PostgresTenantSpec{
			DatabaseName:   "drills",
			StorageSize:    mustQuantity("1Gi"),
			BackupSchedule: "0 3 * * *",
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
	// ...assertions below...
```

```text
vet: internal/controller/backup_controller_test.go:38:5: unknown field
BackupSchedule in struct literal of type v1alpha1.PostgresTenantSpec
```

Same two-step red as Chapter 3's `Conditions`: API change first, then
behavior. Add the field to `PostgresTenantSpec` in
`api/v1alpha1/postgrestenant_types.go`:

```go
	// BackupSchedule is a cron expression (eg "0 3 * * *") for when to
	// back the database up. Empty means backups are not requested.
	// +optional
	BackupSchedule string `json:"backupSchedule,omitempty"`
```

A plain `string` needs no `DeepCopy` changes, but the CRD schema does
need regenerating so the API server accepts the new field — `make
manifests`, or just `make test`, which runs it for you (same
mechanism as Chapter 3).

## Red: three behaviors, one controller

The new specs live in a new file,
`internal/controller/backup_controller_test.go` — same reasoning as
Chapter 2's split: a different controller's behavior is a different
concern, and its own file makes that visible. Same package, so
`mustQuantity` and the package-level suite variables (`ctx`,
`k8sClient`) are available without redeclaring them.

One small thing to notice in the file's opening: it declares its own
`timeout` and `interval` constants, *inside* the `Describe`, even
though the other test file has its own pair. That's not duplication —
Chapter 2 scoped those constants inside their `Describe` closure, and
a closure's constants aren't visible outside it, so each `Describe`
brings its own. (Had they been package-level in the first file, a
second declaration here would be a compile error — the scoping
choice in Chapter 2 is what makes this file simple.)

```go
package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	postgresv1alpha1 "github.com/yourusername/fleetdb/api/v1alpha1"
)

// findEnv returns the env var with the given name, or nil.
func findEnv(envs []corev1.EnvVar, name string) *corev1.EnvVar {
	for i := range envs {
		if envs[i].Name == name {
			return &envs[i]
		}
	}
	return nil
}

var _ = Describe("PostgresTenant backup controller", func() {
	const timeout = 5 * time.Second
	const interval = 100 * time.Millisecond

	// It blocks go here
})
```

`batchv1` is `k8s.io/api/batch/v1` — the CronJob's API group is
`batch`, and `k8s.io/api` ships a package per group, imported with
the group-alias convention you've used since Chapter 1. `apierrors`
is back (Chapter 2's friend) because the third spec asserts on a
*not-found* error. `findEnv` is this file's only helper: the CronJob's
container carries several env vars and the tests need to pick one out
by name; returning a pointer (`&envs[i]`) preserves the whole
`EnvVar` — name, value, and any `ValueFrom` source.

The first spec is the tenant from the API section, now with its
assertions attached. Read them as a wiring diagram the same way
Chapter 2's StatefulSet assertions were:

```go
It("creates a CronJob when the tenant requests backups", func() {
	tenant := &postgresv1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{Name: "drills", Namespace: "default"},
		Spec: postgresv1alpha1.PostgresTenantSpec{
			DatabaseName:   "drills",
			StorageSize:    mustQuantity("1Gi"),
			BackupSchedule: "0 3 * * *",
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	cronJob := &batchv1.CronJob{}
	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{
			Name: "drills-postgres-backup", Namespace: "default",
		}, cronJob)
	}, timeout, interval).Should(Succeed())

	Expect(cronJob.Spec.Schedule).To(Equal("0 3 * * *"))
	Expect(cronJob.Spec.ConcurrencyPolicy).To(Equal(batchv1.ForbidConcurrent))
	container := cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
	Expect(container.Image).To(Equal("postgres:16"))
	Expect(container.Command).To(Equal([]string{"pg_dump"}))
		Expect(cronJob.Spec.JobTemplate.Spec.Template.Spec.RestartPolicy).
			To(Equal(corev1.RestartPolicyNever))

		password := findEnv(container.Env, "PGPASSWORD")
		Expect(password).NotTo(BeNil())
		Expect(password.ValueFrom.SecretKeyRef.LocalObjectReference.Name).
			To(Equal("drills-postgres"))
	Expect(password.ValueFrom.SecretKeyRef.Key).To(Equal("POSTGRES_PASSWORD"))
})
```

Every one of those assertions connects to something earlier in the
book:

- `Schedule: "0 3 * * *"` — the tenant's wish, copied verbatim. The
  CronJob's controller (the one that lives in kube-controller-manager,
  not in our operator) owns everything about *when* this runs.
- `ConcurrencyPolicy: ForbidConcurrent` — if a backup from 3 a.m. is
  still running at 4 a.m., skip the 4 a.m. run rather than stacking
  two `pg_dump` processes on one small database.
- `Image: "postgres:16"` — the *same* image as the tenant's server,
  from the *same* spec field. This is not cosmetic: `pg_dump` must be
  the same major version as the server it dumps, or it refuses (or
  worse, produces something unusable). One field, two resources,
  kept consistent by construction.
- `Command: pg_dump` — the dump writes to stdout, which is where the
  honest "backups land in logs" limitation lives.
- `PGHOST: drills-postgres` — the tenant's *headless Service* from
  Chapter 2. Same-namespace DNS needs no suffix: that name resolves
  to the database pod. This is the artifact connection again — the
  Service wasn't just for the StatefulSet.
- `PGPASSWORD` via `SecretKeyRef` — the credential Secret from
  Chapter 2, pulled one *key* at a time. Note the contrast with the
  StatefulSet: the server needs the whole `POSTGRES_*` family, so it
  uses `envFrom` to inject everything; `pg_dump` reads one
  conventional variable (`PGPASSWORD`, the password the whole
  client family honors), so it uses `valueFrom.secretKeyRef` to
  extract exactly that key from the same Secret. Whole Secret vs one
  key — two mechanisms, one source of truth.
- `RestartPolicy: Never` — a detail with a rule behind it: Job pods
  must not restart forever (`Always`), because the Job itself already
  owns retrying failed runs via its `backoffLimit`. The API server
  rejects a Job pod template with `RestartPolicy: Always`; `Never`
  says "a failed dump is a failed Job, let the Job decide about
  retries."

The second spec pins the Chapter 2 `cogs` lesson for this resource:
changes propagate. Create the tenant with one schedule, then edit it:

```go
It("updates the CronJob when the backup schedule changes", func() {
	tenant := &postgresv1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{Name: "washers", Namespace: "default"},
		Spec: postgresv1alpha1.PostgresTenantSpec{
			DatabaseName:   "washers",
			StorageSize:    mustQuantity("1Gi"),
			BackupSchedule: "0 3 * * *",
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	cronJob := &batchv1.CronJob{}
	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{
			Name: "washers-postgres-backup", Namespace: "default",
		}, cronJob)
	}, timeout, interval).Should(Succeed())

		Eventually(func() error {
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "washers", Namespace: "default",
			}, tenant); err != nil {
				return err
			}
			tenant.Spec.BackupSchedule = "30 4 * * *"
			return k8sClient.Update(ctx, tenant)
		}, timeout, interval).Should(Succeed())

	Eventually(func() string {
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "washers-postgres-backup", Namespace: "default",
		}, cronJob); err != nil {
			return ""
		}
		return cronJob.Spec.Schedule
	}, timeout, interval).Should(Equal("30 4 * * *"))
})
```

The third spec is the genuinely new *behavior* — not a Chapter 2 echo
at all. In every resource so far, desired state could only grow: the
tenant wanted a Secret, a PVC, a Service, a StatefulSet, forever. A
backup schedule is the first thing a tenant can *unwant*, and when it
does, the CronJob must go. The spec: create with a schedule, wait for
the CronJob, clear the field, and poll until `Get` answers not-found:

```go
It("removes the CronJob when backups are no longer requested", func() {
	tenant := &postgresv1alpha1.PostgresTenant{
		ObjectMeta: metav1.ObjectMeta{Name: "pincers", Namespace: "default"},
		Spec: postgresv1alpha1.PostgresTenantSpec{
			DatabaseName:   "pincers",
			StorageSize:    mustQuantity("1Gi"),
			BackupSchedule: "0 3 * * *",
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	cronJob := &batchv1.CronJob{}
	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{
			Name: "pincers-postgres-backup", Namespace: "default",
		}, cronJob)
	}, timeout, interval).Should(Succeed())

		Eventually(func() error {
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "pincers", Namespace: "default",
			}, tenant); err != nil {
				return err
			}
			tenant.Spec.BackupSchedule = ""
			return k8sClient.Update(ctx, tenant)
		}, timeout, interval).Should(Succeed())

	Eventually(func() bool {
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "pincers-postgres-backup", Namespace: "default",
		}, &batchv1.CronJob{})
		return apierrors.IsNotFound(err)
	}, timeout, interval).Should(BeTrue())
})
```

The final closure returns a `bool` — `true` exactly when the error is
the not-found one, so a *different* error (a network blip) keeps the
poll alive instead of accidentally passing.

All three go red against the current code, for the usual reason:

```text
• [FAILED] [7.015 seconds]
[FAILED] Timed out after 5.001s.
Expected success, but got an error:
    cronjobs.batch "drills-postgres-backup" not found
```

`washers` and `pincers` fail identically at their own `Get`s — the
first CronJob in each spec never appears. Time to write the
controller.

## Green: the second controller

New file, `internal/controller/backup_controller.go`, complete. It
looks deliberately like a smaller echo of everything Chapter 2 and 3
built — same skeleton, one resource, plus one behavior the tenant
controller doesn't have:

```go
package controller

import (
	"context"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	postgresv1alpha1 "github.com/yourusername/fleetdb/api/v1alpha1"
)

// BackupReconciler reconciles the backup side of a PostgresTenant:
// whenever a tenant requests scheduled backups, this controller makes
// the CronJob that runs them.
type BackupReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete

// backupCronJobName is the name of the backup CronJob for a tenant —
// "acme-postgres-backup" for a tenant named "acme".
func backupCronJobName(tenant *postgresv1alpha1.PostgresTenant) string {
	return resourceName(tenant) + "-backup"
}

func (r *BackupReconciler) Reconcile(ctx context.Context,
	req ctrl.Request) (ctrl.Result, error) {
	var tenant postgresv1alpha1.PostgresTenant
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if err := r.reconcileBackupCronJob(ctx, &tenant); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *BackupReconciler) reconcileBackupCronJob(
	ctx context.Context, tenant *postgresv1alpha1.PostgresTenant,
) error {
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupCronJobName(tenant),
			Namespace: tenant.Namespace,
		},
	}
	operation, err := controllerutil.CreateOrUpdate(ctx, r.Client,
		cronJob, func() error {
		applyBackupCronJobSpec(cronJob, tenant)
		return ctrl.SetControllerReference(tenant, cronJob, r.Scheme)
	})
	if operation == controllerutil.OperationResultCreated {
		r.Recorder.Event(tenant, corev1.EventTypeNormal, "BackupScheduled",
			"Scheduled database backups at "+tenant.Spec.BackupSchedule)
	}
	return err
}

func applyBackupCronJobSpec(cronJob *batchv1.CronJob,
	tenant *postgresv1alpha1.PostgresTenant) {
	cronJob.Spec.Schedule = tenant.Spec.BackupSchedule
	cronJob.Spec.ConcurrencyPolicy = batchv1.ForbidConcurrent
	cronJob.Spec.JobTemplate.Spec.Template.Spec.RestartPolicy =
		corev1.RestartPolicyNever
	cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers = []corev1.Container{{
		Name:    "pg-dump",
		Image:   "postgres:" + tenant.Spec.PostgresVersion,
		Command: []string{"pg_dump"},
		Env: []corev1.EnvVar{
			{Name: "PGHOST", Value: resourceName(tenant)},
			{Name: "PGDATABASE", Value: tenant.Spec.DatabaseName},
			{Name: "PGUSER", Value: "postgres"},
			{Name: "PGPASSWORD", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: resourceName(tenant),
					},
					Key: "POSTGRES_PASSWORD",
				},
			}},
		},
	}}
}

// SetupWithManager sets up the backup controller with the Manager.
func (r *BackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&postgresv1alpha1.PostgresTenant{}).
		Owns(&batchv1.CronJob{}).
		Complete(r)
}
```

Nothing here is a new mechanism — that's the point. `CreateOrUpdate`
plus a mutate function is Chapter 2's StatefulSet policy (the
schedule must propagate, so create-once is wrong for this resource
too — the `washers` spec pins it); `SetControllerReference` gives
cascade deletion *and* is what `.Owns(&batchv1.CronJob{})` will
route on; the `Recorder` field and `BackupScheduled` event are
Chapter 3's pattern re-applied (the mechanism is already tested
there, so this chapter doesn't re-test it — it just uses it). Even
the `applyBackupCronJobSpec` shape — a function that fills fields on
an existing object rather than returning a new one — is the same
`applyStatefulSetSpec` decision, made for the same reason.

One design note: `applyBackupCronJobSpec` lives in *this* file, not
in `postgrestenant_resources.go`. That file is the tenant
controller's builders; this file is the entire home of the backup
concern — controller, builder, name helper. When you can point at a
file and say "that's the backup subsystem," the Single Responsibility
Principle is doing structural work, not just describing functions.

### Registering a second controller: the first real failure

The tests can't run the new controller until the suite registers it.
In `suite_test.go`, right after the tenant controller's registration,
add its twin:

```go
	err = (&BackupReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorderFor("postgrestenant"),
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())
```

(`cmd/main.go` gets the identical block, with `mgr` in place of
`k8sManager`, placed *before* the `// +kubebuilder:scaffold:builder`
comment — that comment is where future scaffolding inserts new
controllers, and code placed after it can get shuffled by tooling.)

Run, and hit a failure that isn't about your logic at all:

```text
[FAILED] Unexpected error:
    controller with name postgrestenant already exists. Controller
    names must be unique to avoid multiple controllers reporting the
    same metric. This validation can be disabled via the
    SkipNameValidation option
```

Every controller has a *name*, used to label its Prometheus metrics
(`controller_runtime_reconcile_total{controller="..."}`) and its log
lines. When you don't choose one, controller-runtime derives it from
the primary watched type — and both of these controllers watch
`PostgresTenant`, so both claimed the name `postgrestenant`. Two
controllers with one name would make their metrics indistinguishable,
so controller-runtime refuses at startup rather than letting you
discover it in a Grafana graph six months later. The fix is an
explicit name on the builder:

```go
	return ctrl.NewControllerManagedBy(mgr).
		For(&postgresv1alpha1.PostgresTenant{}).
		Named("backup").
		Owns(&batchv1.CronJob{}).
		Complete(r)
```

A valuable failure precisely because nothing was wrong with the
reconciliation logic — the *registration* was ambiguous. With
`Named("backup")` in place:

```console
$ make test
...
Ran 16 of 16 Specs ... SUCCESS! -- 15 Passed | 1 Failed | 0 Pending | 0 Skipped
```

Fifteen pass — `drills` and `washers` included — and exactly one
spec still fails, and it's the one the implementation genuinely
doesn't satisfy yet:

```text
[FAIL] PostgresTenant backup controller [It] removes the CronJob when backups are no longer requested
[FAILED] Timed out after 5.001s.
Expected
    <bool>: false
to be true
```

The `pincers` tenant's CronJob was created on schedule and still
exists after the schedule was cleared — the closure keeps observing
`false` because `Get` keeps succeeding. Which is correct behavior for
the code as written: `Reconcile` only ever *adds*.

## Green again: the missing half of level-triggered

Chapter 3's mantra was "write only when reality differs from the
record." There's a mirror-image duty hiding in it: when the record
says something should *stop existing*, a level-triggered reconciler
has to delete it. Desired state can shrink; the reconciler has to
follow. Add the branch to `Reconcile`, above the create path:

```go
	if tenant.Spec.BackupSchedule == "" {
		return ctrl.Result{}, r.deleteBackupCronJob(ctx, &tenant)
	}

	if err := r.reconcileBackupCronJob(ctx, &tenant); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
```

and the helper below `reconcileBackupCronJob`:

```go
// deleteBackupCronJob removes the backup CronJob when the tenant no
// longer requests backups. Deleting something that doesn't exist is
// a no-op, not an error.
func (r *BackupReconciler) deleteBackupCronJob(
	ctx context.Context, tenant *postgresv1alpha1.PostgresTenant,
) error {
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupCronJobName(tenant),
			Namespace: tenant.Namespace,
		},
	}
	err := r.Delete(ctx, cronJob)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
```

The `IsNotFound` swallow deserves a second look, because it's the
same idempotence rule as `reconcileCreateOnce`, mirrored: create-if-
absent and delete-if-present are both written so that *already being
done* reads as success. `Delete` on a missing object returns
not-found; treating that as an error would poison every future
reconcile of a tenant that never had backups.

A quick timeline of the pincers tenant, end to end — because the
delete branch has a self-referential wrinkle worth seeing spelled out:

- **Tick 1** — tenant created with `BackupSchedule: "0 3 * * *"`.
  Both controllers wake (both watch the tenant; more on that below).
  The tenant controller builds its four resources; the backup
  controller creates `pincers-postgres-backup`.
- **Tick 2** — the test clears the field. The API server bumps
  `Generation`; both controllers wake again. The backup controller
  takes the new branch and deletes the CronJob.
- **Tick 3** — that deletion is itself a change to a watched object
  (`Owns(&batchv1.CronJob{})`), so the backup controller wakes a
  third time, finds the schedule still empty, and calls `Delete`
  again — which lands as not-found and is swallowed. No write
  happens, nothing else is watching, and the loop stops.
- **Tick 4 onward** — quiescent. A future tenant edit wakes both
  controllers; the backup controller deletes nothing (already gone),
  creates nothing (nothing requested).

Notice what tick 3 really shows: the delete branch is *also*
level-triggered — it converges to "do nothing" exactly the way the
create path does, and the `IsNotFound` swallow is what makes that
convergence free instead of an error loop.

```console
$ make test
...
ok  	github.com/yourusername/fleetdb/internal/controller	13.370s	coverage: 82.0% of statements
```

All sixteen specs green.

## Two queues, one manager

Worth pausing on what the suite now runs, because it's the chapter's
actual deliverable: one manager, two controllers, each with its own
`For` watch feeding its own work queue and its own worker pool. A
tenant event now triggers *two* reconciles — one per controller —
and that's fine at FleetDB's scale; each controller re-reads the
tenant and acts only on its own concern. (If one controller ever
needed to react to only a *subset* of tenants, the tool for that is
event `predicate`s — filters on the watch — which this chapter
deliberately skips: two cheap reconciles beat an early optimization.)

The queues are also where the SRP pays off mechanically: retry
backoff is per-controller. A tenant whose CronJob create keeps
failing backs off in *its* queue while tenant reconciliation in the
other queue proceeds untouched. One `Reconcile` function can't offer
that separation, no matter how it's organized internally.

## How this differs from Kubebuilder

Almost nothing does — with one honest asymmetry. Everything in this
chapter is controller-runtime: a second `Reconcile`, a second
`SetupWithManager`, `Named`, `batchv1`, CronJob creation. Kubebuilder
projects write precisely the same code. The asymmetry is in
scaffolding: `kubebuilder create api` scaffolds a controller *per
API type*, and there's no generator for "a second controller watching
an existing type" — in Kubebuilder you'd hand-write this file exactly
as this chapter did. Operator SDK's scaffolding (the
`+kubebuilder:scaffold:builder` anchor in `cmd/main.go`) picks up
both controllers the same way Kubebuilder's does: whatever registers
itself before the anchor gets built into `main.go`'s startup. The
tools converge on hand-written controllers.

## Commit checkpoint

```
fleetdb/
├── api/v1alpha1/
│   └── postgrestenant_types.go         # spec gains BackupSchedule
├── cmd/
│   └── main.go                         # registers BackupReconciler too
├── config/crd/bases/                   # CRD regenerated (backupSchedule field)
├── internal/controller/
│   ├── postgrestenant_controller.go    # unchanged from Chapter 3
│   ├── postgrestenant_resources.go     # unchanged from Chapter 2
│   ├── backup_controller.go            # NEW: BackupReconciler +
│   │                                   # applyBackupCronJobSpec
│   │                                   # backupCronJobName, deleteBackupCronJob
│   ├── postgrestenant_api_test.go      # Chapter 1's validation tests
│   ├── postgrestenant_controller_test.go  # Chapters 2–3 specs
│   ├── backup_controller_test.go       # NEW: this chapter's 3 specs + findEnv
│   └── suite_test.go                   # registers both controllers
└── config/                             # rbac regenerated (cronjobs)
```

`make test` passes: 16 specs (3 new), 82.0% coverage, about 13
seconds. A tenant can now ask for backups with
`backupSchedule: "0 3 * * *"` and get a CronJob that runs `pg_dump`
against its database on that schedule — or stop asking, and lose the
CronJob — with the work split across two controllers whose only
shared machinery is the manager they both register with.

Honest gaps, as always:

- **No backup actually runs in these tests.** envtest has no
  kube-controller-manager, so the CronJob never spawns a Job. The
  tests pin the CronJob object; the first real backup happens when
  FleetDB deploys to a kind cluster in Phase 4.
- **Dumps land in pod logs.** Stdout is not a backup strategy;
  durability is explicitly future work.
- **A typo'd schedule fails forever.** The API server validates cron
  expressions when a CronJob is *created* — a bad
  `backupSchedule` means every reconcile's create is rejected, with
  rate-limited retries and a Warning-shaped gap in the world. The
  real fix is rejecting the tenant itself at admission time, which is
  Chapter 15's subject.
- **RBAC for `batch/cronjobs`** is promised by a marker and enforced
  nowhere in envtest — same standing gap, one more line to verify in
  Chapter 18.

Phase 1 ends here: FleetDB's API has desired and observed state, and
two controllers turn the first into the second. Phase 2 changes
vantage points — from the operator's *behavior* to the operator's
*observability*, starting with the question "what are metrics, logs,
and traces, really?"
