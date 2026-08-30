---
name: feedback-reader-level-and-completeness
description: "FleetDB book readers are new to Ginkgo and controller development, beginners at Go and Kubernetes. Every chapter's code must be complete and compilable as shown, with every term explained inline — caught in a Chapter 2 review (2026-08-28)."
metadata: 
  node_type: memory
  type: feedback
  modified: 2026-08-28T00:00:00.000Z
---

Assume the reader is **new to Ginkgo, new to controller development, a
beginner at Go, and a beginner at Kubernetes** — even though the book's
premise mentions prior Kubebuilder exposure. Review every chapter against
this profile before finalizing it. Found while reviewing Chapter 2: it
read well at the narrative level but a beginner could not have assembled
working code from it.

**Checklist to apply to every chapter before finalizing:**

- **Show the complete final version of any function the chapter builds**
  (Chapter 2's `Reconcile` was never shown whole — only fragments).
  Fragments must say where they live ("inside `Reconcile`, after the
  fetch of the tenant") and where undefined variables come from.
- **Every code snippet must compile as printed.** Declare every variable
  the snippet uses; if a test body is elided, mark the elision with a
  comment that points at a previously shown complete version, and show
  the first occurrence of any repeated pattern in full.
- **Give import blocks for implementation files**, not just test files
  (`apierrors`, `controllerutil`, `client`, `types` all appeared unexplained).
- **No latent bugs in illustrative code.** A snippet that returns early
  from `Reconcile` when one resource exists (skipping the rest) passed
  the chapter's tests but was wrong as a whole reconciler. Verify the
  logic of fragments, not just that they're plausible.
- **Explain terms inline at first use**, even small ones: Secret
  `Data` vs `StringData`, the `postgres` image's `POSTGRES_*` env-var
  convention and `envFrom`, what a StatefulSet is vs a Deployment,
  governing Service, PVC claims/`ReadWriteOnce`, `ctrl.Result`,
  `types.NamespacedName`, `intstr.IntOrString`, `apierrors.IsNotFound`,
  Ginkgo's `var _ = Describe(...)` idiom, `metricsserver` `BindAddress:
  "0"`, pointer derefs that trace back to earlier chapters' bugs.
- **Connect the artifacts.** Don't present related resources as
  independent creations — name the wiring (StatefulSet's `ServiceName`
  needs the headless Service; `envFrom` needs the Secret's exact keys;
  `ClaimName` mounts the PVC; ordering in `Reconcile` matters).
- **Name honest gaps explicitly** (envtest doesn't enforce RBAC, no
  `.Owns(...)` watch yet) rather than letting tests imply production
  correctness.
- **Actually run code in scratch space before printing output** —
  coverage percentages and compile output must be real, not plausible.
  User is verifying Chapter 2 themselves; confirm its printed numbers
  when it's their turn to run.
- **Loops, guards, and state machines get timeline walk-throughs** —
  a tick-by-tick concrete example (e.g. Chapter 3's five-tick
  walkthrough of the `changed` guard), not just prose about why the
  code is correct. Found in a Chapter 3 review (2026-08-31): the
  abstract "guard against DoS" explanation confused the reader until
  the concrete walkthrough was added.
