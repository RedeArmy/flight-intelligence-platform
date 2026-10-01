# ADR-021: AI tool boundary: Tool Gateway and Policy Engine

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
LLMs must never hold arbitrary system access; provider text is untrusted (Constitution §42-§43, §79-§81, R-6).

## Decision
Future agents call only typed tools via a Tool Gateway; a Policy Engine authorizes each call (principal, tool, args, context) as allow, deny or require-explicit-confirmation. Read-only tools are auto-allowed; transactional tools always need confirmation. Platform APIs are authoritative; LLM output is never used for authorization or executed. External content is sanitized and delimited. All calls are audit-logged. Implemented in E11, not before.

## Alternatives considered
- Let the LLM call HTTP directly: rejected.
- Embed LLM calls in domain services: couples the domain to a non-deterministic component.

## Consequences
+ Containment of prompt injection and tool abuse.
- An extra layer to build and test (red-team suite).

## Rejected options
LLM access to arbitrary HTTP/DB; LLM-computed prices.
