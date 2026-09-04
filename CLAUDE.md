# Working on lobster

Standing rules for this repo. The full ship procedure lives in the `ship` skill
(`.claude/skills/ship/SKILL.md`) — invoke it with `/ship` when starting a change.

## Authorship

Commits and PRs belong to the repo owner. **Never** add a `Co-Authored-By`
trailer, a "Generated with Claude Code" footer, or any other AI attribution — not
in commit messages, not in PR descriptions, not in issue bodies. Write the commit
message as the author would.

## Delegate the work

Do implementation, review, and investigation in **subagents**, not in the main
window. The main window coordinates: it holds the plan, dispatches, reads short
status lines, and decides. It should not hold file contents, diffs, or test
output.

- Give a subagent a written brief and a report-file path. It writes detail to the
  file and returns a status line, commits, and a one-line test summary.
- Never run two implementation subagents on the same branch at once.
- Hand diffs over as files (`git diff > /tmp/...`), never pasted into a prompt.

## Branching and worktrees

- Never commit to `main`. Branch first.
- Use a worktree per branch, under `.worktrees/<name>`:
  `git worktree add -b feat/thing .worktrees/thing origin/main`
  This repo already follows that layout; keep it.
- **Never force-push a PR branch.** It discards the CodeRabbit reviews sitting on
  that head and restarts the whole review cycle. To update a branch, `git merge
  origin/main` — never rebase.
- Another session may be working in this checkout. Before switching branches or
  resetting, check `git status` and `git worktree list`; if a merge is in
  progress that you did not start, leave it alone and use your own worktree.

## The gate before any commit

```bash
PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go build ./... && go vet ./... && go test ./...
```

CI runs ubuntu, windows and macos, so anything touching syscalls, paths or
process handling also needs:

```bash
CGO_ENABLED=0 GOOS=windows go build ./... && GOOS=windows go vet ./cmd/
CGO_ENABLED=0 GOOS=darwin  go build ./...
```

Never commit on a red suite.

## Tests

- **No live network calls.** A test that probes a real provider is slow and flaky
  in CI. Stub through the package-level seams (`agentProvider`, `agentSearch`,
  `agentResolveAndPlay`, `agentPlayerCheck`, `flixhqDomain`) or the `TestMain`
  pattern in `cmd/fallback_providers_test.go`. If a test suddenly takes seconds,
  it is probing something.
- Never launch a real media player or spawn a detached process in a test.
- Watch every new test **fail first**, on an assertion. A compile error is not a
  red — it proves a symbol is missing, not that the test detects the bug.
- Restore package-level vars and the global `cfg` with `t.Cleanup`.

## Verify findings before fixing them

Reviews on this repo have produced real bugs, wrong suggested fixes, and outright
false alarms. Reproduce a finding against the actual code before changing
anything, and say in the reply what you verified. If a suggested fix is wrong,
push back with the reason rather than implementing it.

## Content boundaries

Do not author brand-new scrapers for pirate streaming sites, and do not
reverse-engineer any site's private API, decryption, or crypto gating. Wiring
existing or documented public APIs is fine.
