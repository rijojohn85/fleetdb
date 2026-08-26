---
name: feedback-tdd-solid-discipline
description: "For the FleetDB book, TDD and SOLID principles must be actively practiced and surfaced every chapter, not just listed as style bullets in the prompt."
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 2c49e501-9f09-466a-b80d-e419d2e61d6c
  modified: 2026-08-26T05:49:59.636Z
---

Every chapter of the FleetDB book must genuinely practice TDD (write the
failing test before the implementation, show real run output, not just
narrate it) and surface SOLID principles inline wherever they naturally
arise in the code being written — not bolted on as a footnote.

**Why:** After Chapter 1 shipped, the user explicitly reminded: "Remember
to use TDD and SOLID principles" — even though this is already listed
under "Book style" in the source prompt ([[project-fleetdb-book]] if that
memory exists). Treat it as a standing instruction to check against for
every chapter, not a one-time style note that's satisfied by having been
read once.

**How to apply:**
- TDD: write the test file first in the chapter narrative, run it, show
  actual failure output when there is one (Chapter 1's `resource.Quantity`
  pointer bug is the model — a real failure surfaced by actually running
  the test, not a hypothetical). Only then write/fix the implementation.
- SOLID: when a chapter introduces multiple child-resource builders,
  interfaces, or reconciler helpers (e.g. Chapter 2's StatefulSet/Service/
  Secret/PVC ownership), call out which principle is at play as it comes
  up — e.g. one function per resource type (Single Responsibility), a
  builder interface the reconciler depends on rather than concrete structs
  (Dependency Inversion) — in plain language, not as a separate lecture
  section.
- Before finalizing a chapter, self-check: did I write a test before the
  implementation it covers, and did I name at least one SOLID principle
  where the design actually exercises it?
