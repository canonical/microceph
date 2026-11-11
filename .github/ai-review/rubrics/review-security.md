---
description: Review for security
---
Review the code for security posture: whether it enforces trust boundaries, validates untrusted input, protects sensitive data, and limits impact under attack or misuse. Focus on exploitable behavior and missing controls, not generic correctness issues unless they create security exposure.

**Look for:**
- Trust-boundary mistakes or input validation gaps
- Authentication, authorization, or privilege-escalation issues
- Injection vulnerabilities (SQL, XSS, command, template, LDAP, shell)
- Sensitive data exposure in logs, errors, responses, metrics, or storage
- Hardcoded secrets, credentials, tokens, or insecure secret handling
- Insecure cryptographic usage, weak randomness, or broken key management assumptions
- Path traversal, unsafe file handling, archive extraction, or permission issues
- SSRF, open redirect, or unsafe outbound request behavior
- Insecure deserialization or parsing of untrusted input
- Cross-tenant or cross-user data access leaks
- Resource-exhaustion or denial-of-service vectors caused by unbounded work or input
- Missing defense-in-depth controls such as least privilege, safe defaults, or blast-radius reduction

**In a charm, also check for:**
- Treating relation data, config, action parameters, and workload responses as trusted when they are not
- Subprocess calls, file paths, or environment variables built from user or relation input without proper validation or escaping
- Secrets or credentials written to unit logs, status messages, config files, or plaintext relation data instead of Juju secrets
- Over-privileged container, filesystem, or service-management operations
- Admin or operational actions that lack sufficient validation, authorization, or auditability

**Questions to answer:**
- What can an attacker or compromised dependency control, observe, or influence here?
- Which inputs cross a trust boundary, and how are they validated or constrained?
- If one control fails, what sensitive data, capability, or system access could be exposed?
- What defense-in-depth measure would most reduce impact?
