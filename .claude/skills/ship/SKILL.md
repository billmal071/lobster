---
name: ship
description: Use when carrying out any code change on lobster — from picking up a task through local CodeRabbit review, opening a PR, and monitoring it to merge. Covers worktrees, subagent delegation, and how to tell when a CodeRabbit review is genuinely finished.
---

# Shipping a change to lobster

The standing rules are in `CLAUDE.md` and apply throughout. This is the
procedure.

**The main window coordinates; subagents do the work.** Every implementation,
review, and investigation step below is a dispatch. The main window holds the
plan and the decisions, not file contents or diffs.

## 1. Set up an isolated workspace

```bash
git fetch origin
git worktree add -b feat/<name> .worktrees/<name> origin/main
```

Never work on `main`. Check `git worktree list` and `git status` first — another
session may be mid-merge in this checkout; if so, leave it alone.

## 2. Understand before building

For anything non-trivial, dispatch a read-only agent to map the area and report
back the specific functions, seams and constraints that matter. Two things this
repo punishes you for not knowing up front:

- **Interactive blocking.** `internal/ui.Select` execs `fzf` unconditionally;
  `ui.Input` execs it directly; `ui.SelectWithTimeout` reads raw stdin; the
  no-arg root command opens a Bubble Tea TUI. Anything an agent or script must
  drive has to avoid all four.
- **Provider IDs are not portable.** The resolver re-searches by title
  (`internal/resolver/probe.go`), so an ID alone does not identify a work. Carry
  title and year too.

## 3. Implement in small, reviewed steps

Write the failing test first, watch it fail **on an assertion**, then implement.
One logical change per commit.

Dispatch an implementer per task with a brief and a report-file path. Ask it to
return only: status, commit SHA, one-line test summary, concerns. Read the report
file only if you need detail.

Run the gate from `CLAUDE.md` before every commit.

## 4. Local CodeRabbit review — before pushing

The CLI is installed (`coderabbit`, authenticated). Review locally so obvious
findings never reach a public PR:

```bash
cd .worktrees/<name>
git fetch origin
coderabbit review --plain --type committed --base origin/main > /tmp/cr-local.md
```

Use `origin/main`, not `main`: your local `main` is only as fresh as your last
pull, so reviewing against it can compare the wrong range and hide or invent
findings.

`--type uncommitted` reviews the working tree; `--type all` does both. Use
`--config CLAUDE.md` to give it this repo's conventions.

Hand `/tmp/cr-local.md` to a subagent to triage — do not read it into the main
window. For each finding: reproduce it first, fix only what is real, and record
what you rejected and why.

## 5. Open the PR

```bash
git push -u origin feat/<name>
gh pr create --base main --title "<type>(<scope>): <summary>" --body-file /tmp/pr-body.md
```

No AI attribution anywhere in the title or body. A good body states what changed,
*why* the non-obvious decisions were made, what was verified, and what was **not**
verified — an honest limitations section is worth more than a feature list.

## 6. Monitoring the PR — the part that goes wrong

**A CodeRabbit review is finished only when the commit range in its walkthrough
comment ends at the current head.** Nothing else is reliable:

```bash
head=$(gh pr view N --json headRefOid --jq .headRefOid)
# Select the walkthrough comment specifically, then read its range. Scanning
# every bot comment and taking the last hash is unsound: any other bot comment
# carrying a 40-hex sha wins, and a stale walkthrough then reads as current.
rng=$(gh api repos/billmal071/lobster/issues/N/comments --paginate \
        --jq '.[] | select(.user.login=="coderabbitai[bot]")
                  | select(.body | contains("<!-- walkthrough_start -->"))
                  | .body' \
      | grep -oE 'and [0-9a-f]{40}' | tail -1 | cut -d' ' -f2)

if [ -z "$rng" ]; then
  echo "no walkthrough found — treat as NOT reviewed"   # fail closed
elif [ "$head" = "$rng" ]; then
  echo "reviewed current head"
else
  echo "STALE ($rng != $head) — do not merge"
fi
```

Fail closed on an empty result. An absent walkthrough means CodeRabbit has not
reported on this head yet — never the same thing as a pass.

Traps, each of which has caused a wrong call here:

- `gh pr view --json reviews` reports **stale passes**, and shows CodeRabbit's
  in-thread replies as full reviews against the new head.
- `gh pr checks` showing "CodeRabbit — pass" can mean *"Review rate limited"*.
- The bot login differs per API: `coderabbitai[bot]` for issue comments,
  `coderabbitai` in the GraphQL `reviewThreads` API. Filtering on the wrong one
  returns an empty list, which reads as "no findings".
- CodeRabbit posts **new** findings on re-review, so a PR that was clean an hour
  ago may not be. Re-check the whole gate on the current head, every time.

Unresolved threads:

```bash
gh api graphql -f query='{repository(owner:"billmal071",name:"lobster"){pullRequest(number:N){
  reviewThreads(last:100){nodes{isResolved path line comments(first:3){nodes{databaseId author{login} body}}}}}}}' \
  --jq '.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved==false)'
```

Reply **in-thread** — note the endpoint includes the PR number, and omitting it
404s:

```bash
gh api repos/billmal071/lobster/pulls/N/comments/<comment_id>/replies -f body='...'
```

The "Docstring Coverage" pre-merge check is a warning, not a blocker: it fires
because Go test functions have no doc comments, which is normal. Do not pad tests
to satisfy it.

## 7. Merge

Merge only when all four hold: walkthrough range ends at head, zero unresolved
threads, CI green (SKIPPED is fine — the weekly shai-hulud job normally is), and
`mergeable == MERGEABLE`. Right after another merge GitHub returns `UNKNOWN`
while it recomputes; re-poll rather than treating that as a blocker.

```bash
gh pr merge N --squash --delete-branch
```

Squash is this repo's convention — `main` is one commit per PR.

Then clean up:

```bash
git worktree remove .worktrees/<name>
git worktree prune
```

## For long waits

Reviews land 10–60 minutes after a push. Don't poll in a tight loop — either use
a scheduled check, or stop and report the state honestly so the owner can decide.
Never write "the PR is done" off a single poll; that mistake has been made here.
