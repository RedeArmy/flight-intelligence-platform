## What and why
<!-- Problem, approach, link to the backlog item (e.g. E1-5) -->

## Checklist (Definition of Done, docs/architecture/08-engineering-standards.md section 3)
- [ ] Requirement and design linked; **ADR added or updated** if this is an architectural decision
- [ ] Unit tests added or updated; integration/contract tests where relevant
- [ ] Errors classified; failure modes considered
- [ ] **Security impact reviewed** (new input, auth, secrets, outbound calls, logging); no secrets or provider data committed
- [ ] Logs, metrics and traces added for new behavior
- [ ] OpenAPI and docs updated; no breaking change within `/v1`
- [ ] Migration is expand/contract-safe (or N/A)
- [ ] Dependency changes justified (or N/A)
- [ ] `make ci` passes locally (or `scripts\dev.ps1 ci` on Windows)

## Risk and rollback
<!-- What could go wrong, how to revert -->
