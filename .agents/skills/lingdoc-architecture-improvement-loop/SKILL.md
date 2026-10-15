---
name: lingdoc-architecture-improvement-loop
description: "Run a gated LingDoc architecture-improvement loop from fresh origin/main through candidate selection, spec, tickets, implementation, PR health scoring, fixes, and a reviewer request. Use when an architecture improvement should end in a review-ready PR."
---

# LingDoc Architecture Improvement Loop

Use this skill when the user wants LingDoc architecture work to move from an evidence-based candidate to an implemented, health-scored pull request. The loop is deliberately gated: do not silently choose a candidate, skip spec or ticket approval, merge the PR, or hide unresolved evidence gaps.

## Required workflow

1. **Prepare an isolated integration branch.** Read `AGENTS.md`, `docs/README.md`, `docs/TEAM_DEVELOPMENT_TASKS.md`, the issue-tracker instructions, and the linked skill files. Record the current branch and worktree status. Never discard unrelated changes. Fetch `origin/main`, then create a fresh `codex/` branch from it. If the worktree is dirty, use a separate worktree or ask the user before touching the dirty checkout. Use the exact commands and branch/commit in the run state described in [workflow-contract.md](references/workflow-contract.md).
2. **Find candidates.** Run `$improve-codebase-architecture`. Let it inspect the repository with CodeGraph and produce its report. Preserve its candidate vocabulary (`module`, `interface`, `depth`, `seam`, `adapter`, `leverage`, `locality`). Present the candidates with evidence, expected leverage, risks, and dependencies. Ask the user which candidate(s) to pursue; record rejected candidates and the reason. Do not propose implementation interfaces before this selection gate.
3. **Process selected candidates one at a time.** For each selected candidate, run `$mattpocock-skills:to-spec`, `$mattpocock-skills:to-tickets`, and `$implement-spec` in that order. Stop at the seam/spec approval and ticket-granularity approval gates required by those skills. Keep all selected candidates on the same integration branch and do not start the next candidate until the current candidate's implementation, tests, and code-review findings are resolved. Do not invoke `grill-me` as part of this loop; it is for the user to ask questions about skill design.
4. **Create the PR only after the candidate queue is complete.** Refresh CodeGraph, run the applicable formatting, focused tests, integration tests, and repository checks, and inspect `git diff --check`. Create a draft PR from the repository template so the health review can drive follow-up fixes. Link the specs and tickets, state the exact base/head SHA, and document commands and known limitations. Do not merge it.
5. **Score and repair.** Run `$lingdoc-health-scoring` at PR stage. It must report both LingDoc internal health (`H_L`) and LingDoc-to-host-repository fit (`F_R`), the six weighted dimensions, evidence, commands, and confidence `A/B/C`. Store each iteration in the run state and summarize it in the PR. Treat any dimension below `2/5`, failed required checks, correctness/security/data-loss risk, contract drift, or missing recovery evidence as blocking. Convert blocking and material findings into repair items, implement them on the same integration branch, run the relevant tests and review, update the PR, and score again. If a repair changes the agreed scope, create a follow-up spec and ticket before implementing it; otherwise use the implementation workflow's review-fix path.
6. **Close the loop only at a defined convergence point.** Stop when the final PR-stage assessment has no blocking or material findings, required checks are green, the reviewer explicitly reports no remaining actionable problem, and confidence is `A` or `B` with all `B` gaps documented. Use at most five scoring/repair iterations. If the limit is reached, or evidence remains confidence `C`, stop and report the unresolved items instead of looping indefinitely; ask the user how to proceed.
7. **Request review.** After convergence, mark the draft PR ready, add the final score/evidence summary as a PR comment or body update, and request review from `magician336` using the authenticated GitHub CLI (`gh pr edit <number> --add-reviewer magician336`, with an equivalent API fallback if needed). Report the PR URL, score, confidence, checks, and any documented residual risk. This skill never merges or closes the PR.

## Operating rules

- Use the repository's issue tracker and `gh` authentication documented in `docs/agents/issue-tracker.md`; do not invent ticket IDs or claim a publication that failed.
- Keep workflow artifacts under `.scratch/<run-slug>/` (issue slices, state, and score reports) unless the called skill specifies another location. Do not commit generated scratch artifacts unless the user asks.
- Preserve the user's uncommitted work, secrets, and local configuration. Never use `git reset --hard`, `git clean`, or an unreviewed force push.
- Re-run `codegraph sync` after code or contract changes before relying on a new architectural assessment.
- A repair iteration must include the changed files, the finding it addresses, the tests run, and the new score. Do not declare convergence from a score alone.
- If a delegated skill asks a question or requires approval, pause at that gate and resume with the user's answer. The loop may be resumed from the recorded state without repeating completed gates.

For the exact state machine, evidence record, stop conditions, and PR checklist, read [workflow-contract.md](references/workflow-contract.md).
