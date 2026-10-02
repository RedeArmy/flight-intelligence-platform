# ADR-036: Adopt OWASP ASVS 5.0.0, target Level 2

- **Status:** Accepted (2026-10-02)
- **Date:** 2026-10-02
- **Review date:** 2027-04-02 (earlier if OWASP publishes a new ASVS release or user identity, OAuth/OIDC or a web frontend is introduced)
- **Deciders:** owner (RedeArmy)

## Context
The Constitution (section 40) names "ASVS 5.x" as a security baseline without a revision or a level, and open question C13 and backlog item
X-2 require both to be pinned in an ADR "before E1 exit", with the chapter mapping verified at that point. NFR-07 and
`docs/security/security-architecture.md` say the same.

The standard itself leaves the level to the organisation: it says that, rather than prescribing a level, an organisation should
analyse its risks and decide. Level 1 holds about 20% of the requirements (the basic, first-layer ones), Level 2 about another 50%
(so about 70% in total, for "most applications"), and Level 3 the remaining 30% (defence in depth and hard-to-implement controls).
The latest stable release is **5.0.0, published in May 2025**, with seventeen chapters (V1 to V17).

## Decision
**Version.** ASVS **5.0.0**. It is the revision the Constitution points at ("5.x") and the current stable one. A newer release is adopted
by an amendment to this ADR, which also reviews the mapping, not silently.

**Level.** **Level 2 is the target; Level 1 is the minimum from now on.** Concretely:
- Every change is reviewed against the in-scope chapters at Level 1 as part of the Definition of Done ("security review item completed").
- **Level 1 is complete for the in-scope chapters, requirement by requirement, before E2 exits.**
- **Level 2 is complete before the API is exposed outside the local machine.** Until then the platform runs only on a local stack (D1),
  so no external user is exposed to a Level 2 gap.
- Level 3 is not a goal. Individual Level 3 controls may be adopted where they are cheap (for example the append-only audit log), and
  that is recorded, but no claim is made.

**Scope.** Chapters that describe a surface the platform does not have are marked not applicable, with the reason, and are re-checked
whenever that surface appears. The current scope and its evidence are in `docs/security/asvs-coverage.md`:

| In scope | Not applicable yet (reason) |
|----------|-----------------------------|
| V1 Encoding and Sanitization, V2 Validation and Business Logic, V4 API and Web Service, V6 Authentication, V8 Authorization, V11 Cryptography, V12 Secure Communication, V13 Configuration, V14 Data Protection, V15 Secure Coding and Architecture, V16 Security Logging and Error Handling | V3 Web Frontend Security (no frontend), V5 File Handling (no upload or file processing), V7 Session Management (stateless API keys, no sessions), V9 Self-contained Tokens (keys are opaque, no JWT), V10 OAuth and OIDC (planned with user identity, ADR-016), V17 WebRTC (not used) |

**Mapping depth.** This ADR and the coverage document map **by chapter**: which project controls address it, where the evidence is, and
whether it is implemented, partial or not yet. The **requirement-by-requirement** mapping is done per phase, in the security review of the
Definition of Done, for the code that phase adds, because most of the platform (search, verification, history, monitoring, the AI agent)
does not exist yet and a mapping of code that is not written would be guesswork.

**What this is not.** A coverage map is not an assessment: **no individual ASVS requirement has been verified yet**, and nothing here is a
certification or a regulatory claim. The first requirement-level pass is the Level 1 gate before E2 exits.

## Alternatives considered
- **ASVS 4.0.3 (the previous stable release):** more third-party tooling still maps to it, but the Constitution names 5.x and a new
  platform should start on the current revision rather than migrate later.
- **Level 1 only:** the lightest, but it would leave controls the platform already has (insert-only audit, key rotation, request
  throttling by failure) outside what counts, and Level 1 is described as the minimum, not as the goal for a service that holds
  credentials and commercial data.
- **Level 3:** a declared goal with no realistic way to meet it; it would turn the baseline into a document nobody can honour.
- **A full requirement-level mapping now:** several hundred requirements, most of them "not applicable yet" or speculation about code
  that does not exist; it would be out of date by the first feature of E2.

## Consequences
+ A fixed yardstick for security reviews, with a gate at two moments that matter (before E2 exits, before leaving the local machine).
+ Not-applicable chapters are explicit and have a trigger to be re-checked, so a new surface cannot slip in unreviewed.
- The requirement-level work is deferred, not avoided: it lands in each phase's review and in the Level 1 gate, and it takes real time.
- Several Level 2 items depend on decisions not yet made (a managed secret store, TLS termination at the edge, a delivery channel for
  alerts); they stay `Partial` or `Not yet` in the coverage document until then.
- V10 (OAuth and OIDC) will become in scope when user identity arrives, which is a re-planning trigger for this ADR.

## Rejected options
Claiming a level before it has been assessed; marking a chapter not applicable without a reason; adopting a new ASVS release without
reviewing the mapping.
