---
description: Review for code smells
---
Review the code for code smells and change friction: patterns that make the next modification riskier, harder, or more expensive than it should be. Focus on maintainability hazards and refactoring signals, not style-only issues.

**Look for:**
- Long methods, large classes, or oversized modules accumulating multiple responsibilities
- Deep nesting or hard-to-follow control flow
- Repeated conditionals or parallel logic that should have one source of truth
- Shotgun surgery: one change requiring edits in many places
- Feature envy or logic living far from the data it manipulates
- Data clumps, long parameter lists, or primitive obsession
- Boolean flags or mode parameters that create multiple code paths in one API
- Temporary fields, speculative generality, or abstractions with no real caller need
- God objects or god functions
- Copy-paste duplication or near-duplicate logic
- TODO or FIXME comments that hide unresolved design debt
- Temporal coupling or order-sensitive APIs that are easy to misuse

**In a charm, also check for:**
- God charm classes that handle every relation, action, config, and workload concern directly
- Duplicated reconcile, configure, or status logic across multiple event handlers
- Framework event code mixed tightly with business or workload logic
- Hardcoded relation names, config keys, container names, or service constants scattered across files
- New behavior requiring edits in many handlers instead of one shared path

**Questions to answer:**
- What will make the next change risky or repetitive?
- Which abstractions or APIs are accumulating too many responsibilities?
- Where do duplication or temporal coupling suggest the wrong shape of code?
- What small refactor would buy the largest reduction in future change cost?
