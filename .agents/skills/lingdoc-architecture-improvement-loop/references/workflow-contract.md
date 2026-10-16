# LingDoc architecture improvement loop contract

This reference makes the loop resumable and reviewable. It is an execution contract, not a replacement for the delegated skills. The delegated skill remains authoritative for its own approval gates and output format.

## Run state

Create `.scratch/<run-slug>/state.md` before the first architectural scan. Keep the file append-only where practical. Record:

```text
run slug:
repository root:
base ref and SHA:
integration branch:
starting branch and status:
candidate report:
selected candidates and rationale:
current candidate:
spec/issue links:
ticket files or issue links:
implementation commits:
PR number and URL:
health iteration:
  H_L:
  F_R:
  confidence:
  blocking findings:
  material findings:
  commands and results:
  changed files:
  follow-up:
reviewer request result:
residual risks:
```

A new iteration appends a dated entry rather than overwriting the previous score. Never store tokens, passwords, or private keys in this state.

## Branch and worktree protocol

1. Run `git status --short`, `git branch --show-current`, and `git rev-parse --show-toplevel`.
2. If the checkout is clean, run `git fetch origin main` and create `codex/lingdoc-architecture-improvement-<slug>` from `origin/main`.
3. If it is dirty, leave it untouched and create a separate worktree from `origin/main`, or ask the user to choose a safe worktree. Do not stash, reset, clean, or overwrite user changes without explicit authorization.
4. Record the fetched base SHA and verify the branch points at that SHA before editing.

If fetching fails, do not pretend the branch is current. Report the network/authentication failure and either wait for the user or proceed only when the user explicitly accepts the recorded stale SHA.

## Candidate and approval gates

The architecture skill's report is the candidate source of truth. For every candidate, record:

- the concrete hotspot and the CodeGraph/file evidence;
- the suspected change amplification, cognitive load, or unknown-unknown risk;
- the proposed seam and expected leverage;
- dependencies, migration/recovery risk, and an exit condition.

The user selects the candidate set. A candidate is not selected merely because it is ranked first. Before `to-spec`, preserve the architecture skill's candidate evidence and approval gate; do not silently substitute `grill-me` or another design interview. Before `implement-spec`, the user must be able to inspect the spec and the vertical ticket slices. If a delegated skill publishes to GitHub, record the resulting URL and label; if it writes local slices, record their paths.

`grill-me` is outside this loop. Use it only when the user explicitly asks to discuss, challenge, or refine this skill's design.

Process candidates sequentially unless the user explicitly authorizes parallel work and the candidates have disjoint files and contracts. Sequential processing keeps one integration branch and makes health-score changes attributable.

## Health-score gate

At PR stage, the scoring skill must bind its result to the current head SHA, PR number, scope, commands, and risks. Preserve these values in the score report:

- `H_L`: LingDoc internal health;
- `F_R`: fit between LingDoc and the host repository, not a generic whole-repository grade;
- six dimensions and their weighted total;
- evidence level `A`, `B`, or `C`;
- pending or unverified claims.

The health protocol's dimension score below `2/5` is blocking even when the weighted total is high. A required CI failure, untested permission/recovery path, documentation-contract mismatch, or new correctness/security/data-loss risk is also blocking. A confidence `C` result cannot close the loop unless the user explicitly accepts the evidence limitation.

## Repair iterations and convergence

For each finding, write a repair item with severity, evidence, affected contract/module, acceptance test, and owner (the current integration branch). Use the implementation workflow to apply the repair, then:

1. refresh CodeGraph;
2. run focused tests for the finding;
3. run the repository checks required by the PR template and changed modules;
4. update the PR with the repair and evidence;
5. run the health score again and append the new iteration.

The loop converges only when all of the following hold after the last code change:

- no blocking or material findings remain;
- required CI/checks are green or a documented environment limitation is explicitly separated from a code failure;
- the score report contains reproducible commands and evidence;
- confidence is `A`, or `B` with each evidence gap documented and accepted by the workflow owner;
- the score reviewer states that no actionable problem remains.

Run no more than five score/repair iterations. If convergence is not reached, leave the PR open, record the unresolved findings, and ask the user for a decision. Do not lower severity or silently extend the loop.

## PR and reviewer checklist

Before requesting review:

- the PR follows `.github/pull_request_template.md`;
- the title uses a Conventional Commit prefix and includes a task ID when available;
- the description links specs/tickets and states user-visible behavior, contract impact, tests, risks, and screenshots for UI changes;
- the PR head SHA matches the final health-score report;
- no secrets or unrelated changes are present;
- the draft is marked ready only after convergence;
- `magician336` is requested as reviewer and the command result is recorded.

Requesting review is the final side effect. Do not merge, close, force-push, or change repository settings as part of this skill.
