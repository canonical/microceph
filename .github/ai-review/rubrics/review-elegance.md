---
description: Review for elegance
---
Review the code for design quality: whether the structure makes the behavior easy to understand, change, and extend without accidental complexity. Focus on design fit and clarity, not formatting or naming nits unless they obscure the design.

**Look for:**
- Abstractions that do not match the problem domain
- Unclear naming or unclear ownership of responsibilities
- Functions, classes, or modules doing too many things
- Missing abstractions, leaky abstractions, or over-engineered abstractions
- Coupling that should be loose, or boundaries that are poorly defined
- Dependencies that flow the wrong direction
- Data flow or control flow that is hard to follow
- Hidden side effects or surprising mutation
- Special-case logic that obscures the common path
- Magic numbers, strings, or protocol details without a named home
- Inconsistent APIs or patterns across similar code
- Reinventing existing utilities, library features, or framework capabilities

**In a charm, also check for:**
- Event handlers that contain business logic instead of translating events into domain operations
- Charm code where relation, config, workload, and lifecycle concerns are not clearly separated
- Direct subprocess, container, or service-management calls scattered throughout instead of behind an adapter or service layer
- Status computation that is spread across handlers instead of derived from a clear source of truth
- Public or operator-facing APIs that expose framework mechanics instead of domain concepts

**Questions to answer:**
- Which abstractions and boundaries fit the domain, and which fight it?
- If a new requirement lands, is it obvious where the change should go?
- Does the structure keep the common path simple and the exceptional path explicit?
- What small design change would simplify future work the most?
