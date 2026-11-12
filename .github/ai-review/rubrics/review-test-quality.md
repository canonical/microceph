---
description: Review for test quality
---
Review the tests for bug-detecting power across unit, integration, contract, functional, and end-to-end levels: whether they would fail for real regressions, cover risky behavior at the right layer, and provide clear evidence that the system works from the outside. Focus on meaningful protection rather than superficial coverage metrics or over-indexing on any one test layer.

Coverage numbers alone are not enough. A test that cannot fail meaningfully, or only proves internals, provides little value.

**Look for:**
- Weak or non-specific assertions
  - Only checking != nil / !== null / is not None
  - Using .is_ok() without checking the value
  - assertTrue(true) or equivalent
  - Asserting mock calls instead of observable behavior
- Overreliance on white-box or implementation-coupled tests
  - Mocked so heavily the test is disconnected from reality
  - Testing internal call order, helpers, or private structure instead of behavior
  - Refactors would break tests without changing user-visible behavior
- Missing higher-level coverage
  - No integration tests across important component boundaries
  - No black-box tests through public interfaces such as APIs, CLIs, UIs, events, or jobs
  - No functional or end-to-end coverage for critical workflows
- Poor test-layer balance
  - Important behavior tested only at the lowest layer
  - Critical regressions detectable only in a few slow, brittle end-to-end tests
- Missing negative, boundary, or invalid-input cases
  - Happy path only, no failure cases
  - No boundary testing
  - No invalid input testing
- Missing assertions on externally visible outcomes
  - Persisted state, emitted events, downstream effects, returned values, files, logs, status, or user-visible output not verified
- Missing contract or compatibility coverage
  - Schemas, serialization formats, migrations, backward compatibility, version skew, or inter-service contracts not tested
- Environment realism gaps
  - Fixtures unlike production reality
  - No tests with representative config, permissions, data volume, network/process boundaries, or dependency behavior
- Flaky test indicators
  - Sleep or delay in tests
  - Time-dependent assertions
  - Shared mutable state, ordering dependence, or hidden external dependencies
- Tests that are too broad to diagnose failures or too narrow to protect real behavior
- Fixtures, factories, or helpers that hide important assumptions

**In a charm, also check for:**
- Mocking so much of the Juju framework that the test only verifies wiring, not actual charm or workload behavior
- No harness, scenario, or integration coverage for key lifecycle events (install, config-changed, upgrade-charm, relation-joined/changed/departed/broken)
- Tests that only check status without verifying Pebble plan, config files, relation data, workload behavior, or user-visible service behavior
- Missing cases for leadership changes, deferred or retried hooks, missing relation data, container-not-ready states, relation loss, or restart/upgrade recovery
- No cross-app tests that verify relation contracts and actual behavior between the charm, its workload, and related applications
- Integration tests that prove the unit becomes active but not that the workload is actually configured correctly and behaving as expected

**Questions to answer:**
- Which important regressions are caught by unit tests, and which require integration or black-box coverage?
- What critical workflows are not tested through public interfaces?
- Do tests verify externally visible outcomes and contracts, not just internal calls?
- Are tests concentrated at the easiest layer rather than the most valuable one?
- If production behavior regressed, would the suite fail loudly and specifically?
