---
description: Review for wiring gaps
---
Review the code for wiring completeness: whether dependencies, config, abstractions, and declared features are actually connected to the live code paths that run. Focus on integration gaps and incomplete migrations, not whether the connected code is itself well designed.

This catches subtle bugs where the implementer thinks they integrated something, but the old implementation is still being used.

**Look for:**
- New dependency in a manifest but never imported, referenced, or invoked
  - Go: module in go.mod but no import or call site
  - Rust: crate in Cargo.toml but no `use` or runtime path
  - Node: package in package.json but no import or require in the live path
  - Python: package in requirements.txt or pyproject.toml but no import or runtime use
- New SDK, client, or wrapper added but the legacy implementation still handles real traffic
- Config, env vars, feature flags, or schema fields defined but never loaded, propagated, or honored
- New code paths, adapters, or abstractions added but never called from production entrypoints
- Migrations or refactors that are only partially applied
  - Docs, tests, or config updated, but runtime behavior still uses the old path
- Duplicate implementations where only one is actually wired
- Validation, observability, or policy hooks declared but never invoked

**In a charm, also check for:**
- New relation defined in metadata.yaml or charmcraft.yaml but no corresponding event handling or observe() wiring in the charm
- New config option defined but never read via self.config[...] or self.config.get(...)
- New secret, storage, resource, container, action, or port declared but not wired into runtime behavior
- Library added to lib/ or an integration package but never imported in charm code or tests
- Pebble layer or service definition present but never applied, updated, or checked
- Status or relation data fields that are written but never read, or read but never written

**Questions to answer:**
- For each new dependency, config, or feature, where is the live call path that uses it?
- What old path still exists that suggests an incomplete migration?
- Do YAML, code, tests, and docs describe the same wiring?
- What declared capability is still effectively dead?
