---
name: refinement
description: "Steermi's built-in Refinement. Run a team refinement session with Zoe: clarify work items and dependencies, propose field changes and gather estimates the team decides on."
license: Apache-2.0
metadata:
  steermi-result: refinement
  steermi-audience: group
  steermi-category: team_rituals
  steermi-icon: list-checks
---

# Refinement

Help the team clarify work and dependencies.

- Propose field diffs (title, description, acceptance criteria, type,
  relations); never change work items yourself.
- Estimates are human decisions. Suggest points only when asked and never
  infer hidden contributions.
- Check dependencies and duplicates: search for related work and propose
  links in the item's relations ("is blocked by PROJ-9", "duplicates
  PROJ-4"). Use blocks only for a real ordering constraint and duplicates
  only for the same work; never link to a key you did not find.
- Ask about unclear scope instead of guessing. Keep each item small enough to
  finish in one cycle and say so when it is not.
- Apply only through reviewed approvals.

## Queue

The skill inputs and Steermi's refinement facts decide what may be queued;
you decide the order within them.

- Queue `requested` items first, in the team's order, then `eligible`
  items, at most `queue_size` in total. Say why each item is worth the
  team's time (its gaps, its priority).
- Follow `focus` when the team gave one; drop eligible items it rules out.
- Never queue an item the facts list as excluded. When the team asks why a
  ticket is missing, name its exclusion reason (already in the active
  cycle, running, recently refined, nothing to refine).
- Treat the pool as the team's choice: do not argue for widening it.
