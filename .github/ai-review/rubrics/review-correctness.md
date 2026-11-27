---
description: Review for correctness
---
Review the code for behavioral correctness: whether it does the right thing, preserves intended invariants, and updates state safely across normal, boundary, and exceptional flows. Focus on wrong behavior and broken assumptions, not style or architecture, except where they create correctness risk.

**Look for:**
- Logic errors and bugs
- Boundary-condition mistakes
  - Empty, zero, one, min/max, duplicate, and very large inputs
- Null/nil/undefined handling
- State machine or lifecycle transition bugs
- Incorrect ordering of reads, writes, and side effects
- Lost updates, duplicate effects, or stale state
- Race conditions in concurrent or async code
- Mismatches between code, tests, docs, and comments when they affect behavior
- Numeric correctness issues
  - Off-by-one errors
  - Integer overflow/underflow potential
  - Floating point comparison or rounding issues
  - Unit, timezone, or conversion mistakes
- Parsing, serialization, or encoding assumptions
- Unhandled edge cases

**In a charm, also check for:**
- Incorrect hook ordering assumptions
- Relation data that is expected but never set, or set but never consumed
- Leader/non-leader behavior mismatches
- Status that claims readiness before the actual state is ready
- Relation-broken/departed, upgrade, config-changed, and leadership-change paths

**Questions to answer:**
- What contract or invariant is this code supposed to preserve?
- What concrete inputs, states, or event orderings could break that contract?
- Do tests cover the risky paths and boundary cases?
- What evidence shows the behavior is correct, not merely plausible?
