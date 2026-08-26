---
name: feedback-book-writing-only
description: "For the FleetDB operator-sdk book project, Claude's job is writing the book, not building the real fleetdb/ project directory — the user runs/types those commands themselves when they work through it."
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 2c49e501-9f09-466a-b80d-e419d2e61d6c
  modified: 2026-08-26T05:40:56.127Z
---

For the FleetDB book (see [[project-fleetdb-book]]), do not run scaffolding
commands (`operator-sdk init`, `create api`, etc.) or write source files
directly into the real `fleetdb/` directory in the repo. That directory is
the user's own workspace — they run the commands and write the code
themselves as they work through each chapter.

**Why:** The user explicitly stopped me mid-command in Chapter 1 with:
"no I will run these myself when I test out the book, your only job is to
write the book." They want the hands-on experience of typing the commands
and code themselves, not a pre-built project handed to them.

**How to apply:** For every chapter, write the book prose/markdown with the
exact commands and code the reader should run, but verify correctness by
actually running/building it in a scratch space (e.g. `/tmp/...`, or a
throwaway kind cluster) — never in `fleet-db/fleetdb/`. Delete scratch
verification artifacts when done, same pattern as the Phase 0 smoke test
(kept only the log, not the throwaway operator project). This applies to
all future chapters, not just Chapter 1.
