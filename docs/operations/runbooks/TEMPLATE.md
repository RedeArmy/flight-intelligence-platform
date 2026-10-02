# Runbook: <what this does, as a verb phrase>

Copy this file, keep every heading and its order, and delete this paragraph. A test (`internal/archtest`) fails when a runbook
lacks one of the headings below or a valid status. Write for someone who is tired and was not part of the design: exact
commands, the output to expect, and what to do when it is not what you expect.

## Status
**State:** Ready | Skeleton | Blocked
**Last verified:** YYYY-MM-DD, or `never`

What was actually executed to verify it, where, and what was not covered. A runbook that was never run says `Skeleton` or
`Blocked` and `never`: do not mark a procedure `Ready` from reading it.

## When to use
The symptoms or events that call for this runbook, and the ones that look similar but need another (link it).

## Impact
What users and other components notice while it runs, and for how long.

## Before you start
Access, tools, the state to check first, and anything to back up. Say which shell the commands assume.

## Steps
Numbered, each one a single action with the exact command. Say what the output should show.

## Verification
How to know it worked: commands and the expected output, not "check that it works".

## If it goes wrong
How to undo this runbook, and who or what to escalate to when that fails.

## Follow-up
What to record afterwards (change log, audit), what to review, and what to improve in this runbook.
