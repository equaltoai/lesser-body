---
name: adversarial-pr-review
description: "Drive an adversarial, security-first review of a pull request on your governed github_* surface — as a client that did NOT implement the change: confirm the implementing client and that you are not it, ground on the real diff at a pinned head SHA, threat-model the change, sweep it through named security lenses, then attempt to refute every candidate finding before reporting — post the survivors as a COMMENT-only PR review naming both clients. NOT for reviewing a change your own client implemented; NOT for approving, requesting changes, merging, or gating a PR; NOT a replacement for repo CI, SAST/CodeQL, or human security review; NOT for repositories outside your github_list_repos scope; NOT a whole-repo security audit."
---

# Adversarial PR review (security emphasis)

An ordinary review asks "does this look right?" This skill asks "how does this change break,
and who can make it break?" You take the attacker's seat against the diff, then take the
defender's seat against your own findings. The refutation pass is the skill: **a finding you
have not tried to kill is a guess, not a finding.** The cadence applies — Ground on the real
PR state, Act (find, then refute), Record the outcome — and the review is of one pinned head
SHA, named in the report.

Everything here runs on your agent route's governed `github_*` tools, as your own agent
principal, inside the repository scope `github_list_repos` returns. The skill grants nothing:
if your route does not expose the surface, the review degrades to a report for your principal
and posting is BLOCKED.

## The cross-client requirement

**The reviewing client must not be the client that implemented the change.** Codex implemented
→ Claude reviews. Claude implemented → Codex reviews. This is a property of the review, not a
preference about it: a model reviewing its own work carries forward the assumptions that
produced the defect, and the blind spot that let a flaw through is the same blind spot that
will wave it past. Independent eyes are the entire point of the pass.

So this review has two prerequisites, and both are checked before any diff is read:

1. **The implementing client is known and recorded.** If nobody recorded which client
   produced the change, the cross-client property cannot be asserted — say so and report the
   result as an ordinary adversarial review, not a cross-client one.
2. **You are not that client.** If you are, stop and report back to whoever dispatched you so
   the review can be re-dispatched on the other client. Do not proceed "because the context is
   already loaded" — that convenience is exactly the failure this rule exists to prevent.

This review is one of two gates, never the only one. A parent orchestrator's own review
follows it and holds the merge decision. Your job is to hand that review an adversarial pass
it can trust — not to decide anything.

## When to use

- You have been dispatched to adversarially review a PR that a **different** client
  implemented, in your governed repository scope.
- Your principal, steward duties, or a parent orchestrator ask for an adversarial or
  security review of a pull request in your governed repository scope.
- A PR touches security-relevant surface: authn/authz, parsing or deserialization, secrets
  or credentials, network fetching, cryptography, dependency or install-script changes,
  resource limits, error/permission paths.
- An external or unfamiliar contribution needs hostile-eyes reading before the normal
  review path proceeds.

## When NOT to use

- **To review a change your own client implemented** — stop and report back for
  re-dispatch on the other client.
- To approve, request changes on, merge, or close out a PR — this skill is comment-only;
  gate authority lives with humans and the merge-gate discipline, not here.
- As the repository's only security assurance — it does not replace repo CI, SAST/CodeQL,
  or human security review.
- On a repository your `github_list_repos` does not return — report the scope gap; never
  post reviews through ambient credentials instead.
- For a whole-repo security audit — this procedure is diff-anchored; a repo audit is a
  different, separately-scoped work event.

## Inputs

- The repository and PR number, inside your governed scope.
- **The implementing client** (codex / claude / other), from the dispatching brief, the
  assignment record, or the PR's own provenance — and **your own client identity**, so the
  two can be compared.
- `github_get_pr`, `github_list_pr_files`, `github_list_pr_commits`,
  `github_list_pr_review_threads` on your agent route.
- Read access to the repository checkout, so reachability questions can be answered from
  surrounding code, not just hunks.

## Procedure

1. **Ground.** Confirm the repo appears in `github_list_repos`. **Establish the implementing
   client and confirm you are not it** — if you are, stop and report for re-dispatch; if it is
   unrecorded, note that the cross-client property cannot be asserted and continue as an
   ordinary adversarial review. Fetch the PR, its files, and commits; **pin the head SHA** —
   the review is of that head and says so. Read existing review threads so you extend the
   conversation instead of duplicating it.
2. **Threat-model the diff.** For each changed surface ask: what does it parse, trust,
   execute, authorize, store, log, fetch, or depend on? Which inputs can an attacker
   influence? Which trust boundaries does the change cross or move? Triage changed files
   into security-relevant vs. mechanical, and say which is which.
3. **Lens sweep (find).** Pass over the security-relevant hunks once per lens, each time
   with attacker intent, collecting candidate findings:
   - **Injection** — SQL, command, template, path traversal, header/log injection.
   - **Authn/authz** — missing or weakened checks, IDOR, privilege escalation, tenancy
     bleed, default-allow and fail-open paths.
   - **Secrets** — hardcoded or committed credentials, secrets in logs or error output.
   - **Input handling** — validation gaps, unsafe deserialization, parser abuse.
   - **Network** — SSRF, open redirects, unvalidated fetch targets, TLS handling.
   - **Crypto** — weak or homemade primitives, misuse of sound ones, nonce/IV reuse,
     secret-dependent timing.
   - **Data exposure** — PII in logs, verbose errors, over-broad API responses.
   - **Supply chain** — new or changed dependencies, unpinned versions, install scripts,
     provenance of vendored code.
   - **Resource safety** — unbounded allocation, recursion, or concurrency; TOCTOU;
     missing timeouts or limits.

   When reachability or exploitability depends on code outside the hunk, read the
   surrounding code — never confirm or dismiss from the hunk alone. Non-security defects
   noticed along the way are kept as clearly-labeled secondary findings, not dropped.
4. **Refute (adversarial verify).** Switch sides. For every candidate finding, first
   articulate the concrete attack path — who controls the input, how it reaches the sink,
   what actually breaks. Then attack your own claim: is there an upstream guard? Is the
   path reachable in production? Is it test-only code? Was it already true before this PR
   (then it is pre-existing context, flagged as such, not a defect of this change)?
   **A candidate with no articulable attack path defaults to refuted** — it may survive as
   an honest question, never as an asserted vulnerability. Survivors get a severity
   (critical / high / medium / low) and a confidence (confirmed / plausible).
5. **Report.** Post one `github_create_pr_review` with **event `COMMENT`**: an inline
   comment at a real diff position for each surviving finding — what it is, the attack
   path, and a suggested direction — plus a summary body stating **the implementing client,
   the reviewing client (you)**, the head SHA reviewed, the lenses run, finding counts by
   severity, how many candidates the refutation pass killed, and the explicit non-claims
   below. Where an existing thread already raises a finding, reply in that thread
   (`github_reply_pr_review_thread`) instead of duplicating. If the governed surface is
   unavailable or refuses, deliver the full report to your principal and mark posting
   **BLOCKED** — never post through `gh` instead.
6. **Record.** Memory-append the review event: repo, PR, head SHA, **implementing client and
   reviewing client**, lenses run, findings by severity, refutation kills, and whether
   posting succeeded or was BLOCKED.

## Output

A comment-only PR review on the pinned head SHA — inline findings plus a summary naming both
clients, lens coverage, severity counts, refutation-kill count, and non-claims — or, when the
governed surface is unavailable, the same report delivered to the principal with posting
marked BLOCKED. Never an approval, a change-request, or a merge.

## Red flags (refuse)

- **Reviewing a change your own client implemented** — refuse and report for re-dispatch on
  the other client. "It's faster, the model already has the context" is the bypass form; the
  loaded context is the contamination, not a shortcut.
- **Claiming a cross-client review when the implementing client is unknown or unrecorded** —
  refuse the claim, not the review. Run it, and report it as an ordinary adversarial pass
  with the gap named.
- **Submitting `APPROVE` or `REQUEST_CHANGES`, or merging/closing out the PR** as part of
  this skill — comment-only; gate authority is not yours here.
- **Posting the review via `gh` or ambient credentials** — because the governed tool is
  missing, refused you, or the repo is out of scope. A refusal is policy; out-of-scope
  means report-only.
- **Reporting a finding that skipped or failed the refutation pass** — every reported
  finding names its attack path and survived an attempt to kill it.
- **Asserting a vulnerability without an articulable attack path** — downgrade to a
  question or drop it.
- **Confirming or dismissing a finding from the hunk alone** when surrounding code decides
  reachability — read the code first.
- **Publishing weaponized proof-of-concept payloads in public PR threads** — describe the
  flaw and the attack scenario; do not arm it.
- **Treating a finding-free review as a security guarantee** — it is evidence of one
  adversarial pass, nothing more.
- **Treating this review as sufficient on its own** — the orchestrator's own review still
  follows and still holds the decision.
- **Duplicating findings that existing review threads already raise** — reply in-thread.
- **Silently reviewing a moved head** — if the head changed mid-review, re-pin, re-check
  affected findings, and name the SHA actually reviewed.

Non-claims to keep honest in every report: this review is comment-only and carries no
approval, merge, or gate authority; it does not replace the orchestrator's own review, repo
CI, SAST/CodeQL, or human security review; a clean pass is not proof the change is secure;
and cross-client independence is a property of who reviewed, not evidence that the change
is correct.

## Related patterns

`cross-client-adversarial-review`, `refusal-list`, `consultation-surface`,
`cadence-five-body`, `smoke-test-the-built-artifact` (the refutation pass is its review-time
analog), and each agent's own `github-via-theorymcp` skill — the governed surface this
procedure drives.
