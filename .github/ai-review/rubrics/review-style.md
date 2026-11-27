---
description: Review for style
---
Review the code for style and convention fit: whether it matches the repository's established patterns, communicates intent clearly, and stays readable without distracting inconsistency. Focus on readability, consistency, and operator-facing communication, not architectural redesign.

**Look for:**
- Naming that violates language, framework, or repo conventions
- Formatting or import organization inconsistencies
- Inconsistent patterns across similar modules, APIs, or call sites
- Comments that are missing where context is needed, outdated, redundant, or misleading
- Documentation gaps for public APIs, CLI flags, config, or user-visible behavior
- Error, log, and status messages that are unclear, noisy, or inconsistent in tone or level
- Test naming or organization that makes intent hard to understand
- Lint/format violations or local style drift from the rest of the codebase
- Public interfaces that force readers to infer basic usage from implementation details

**In a charm, also check for:**
- Config keys using names or casing that do not follow Juju conventions
- Status messages that are unclear to operators; they should be concise, specific, and actionable
- Inconsistent naming between metadata.yaml or charmcraft.yaml identifiers and Python references
- Relation, container, storage, and action names that drift between YAML, code, and tests
- Operator-facing log and status text that lacks enough context to troubleshoot safely

**Questions to answer:**
- What repo or framework conventions should this code be following?
- Where does inconsistency make the code harder to read, use, or maintain?
- Are public and operator-facing messages clear, consistent, and appropriately documented?
- What style drift is likely to spread if left uncorrected?
