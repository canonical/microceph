---
description: Review for resilience
---
Review the code for resilience and failure handling: whether it fails safely, surfaces actionable errors, and can recover cleanly from transient, partial, and repeated failures. Focus on degraded and recovery behavior, not general business-logic correctness unless it affects failure handling.

**Look for:**
- Swallowed errors, empty catch blocks, or failure paths that silently continue
- Missing error propagation or loss of useful context
- Error messages that are vague, misleading, or not actionable
- Missing timeout, cancellation, or deadline handling
- Insufficient retry, backoff, jitter, or retry-budget logic
- Retries that are unsafe because operations are not idempotent
- Resource leaks or missing cleanup on failure (files, locks, connections, temp state)
- Partial failure states or incomplete rollback/compensation
- Startup, shutdown, or restart paths that leave the system inconsistent
- Missing circuit breakers, rate limiting, or backpressure for external dependencies
- Unhelpful panic/crash behavior or over-broad failure domains
- Recovery path gaps after dependency outages, malformed input, or corrupted state
- Logging and metrics gaps that make failures hard to diagnose

**In a charm, also check for:**
- Hooks that raise unhandled exceptions instead of setting actionable BlockedStatus or WaitingStatus
- Missing guards for not-yet-ready relations, containers, storage, or leadership state
- Deferred, replayed, or retried hooks that repeat side effects or leave inconsistent state
- Partial configuration of workload state that leaves Pebble, config files, or relation data inconsistent
- Status messages that hide failure details or fail to reflect degraded state
- Missing recovery behavior when a relation disappears, a workload container restarts, or an external service becomes reachable again

**Questions to answer:**
- What happens when dependencies are slow, unavailable, or return bad data?
- Which failures leave partial state, ambiguous status, or operator confusion?
- Are retries, timeouts, and recovery paths safe, bounded, and observable?
- If this fails in production, will operators have enough signal to diagnose and fix it?
