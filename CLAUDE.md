# Working on lobster

Go CLI (module `lobster`). Providers live in `internal/provider`, the fallback chain in `cmd/fallback.go`, playback in `cmd/search.go` and `cmd/session.go`.

## Before every push

Run all of these and fix what they report:

```sh
gofmt -l cmd internal          # only files you touched must be clean
go vet ./...
GOOS=windows go vet ./...
go test -race -count=1 ./...
coderabbit review --agent      # local review, fix findings before pushing
```

CodeRabbit's CLI needs `coderabbit auth login` once. Auto reviews are off on the repo; trigger one on a PR with an `@coderabbitai review` comment after each push.

## Rules that keep review comments low

1. **One concern per PR.** A provider rewrite, new providers, retirements and a docs overhaul are four PRs, not one.
2. **Docs match code.** Before pushing, verify every claim you wrote in README.md, GUIDE.md and docs/ against the code it describes (fallback order, flags, config keys, defaults). Review the doc-only diff separately.
3. **One home for a shared rule.** Quality selection, season/episode parsing and fallback ordering exist once, with tests. Do not copy the logic into each provider.
4. **Edge-case pass for every new parser or ID format.** Add tests for empty, ambiguous, oversized, malformed and duplicated input, and for old IDs that must keep resolving.
5. **A test for every branch you add**, written red first. Behaviour that matters (privacy opt-in, torrent tier ordering, download queue excluding magnets) gets a test that fails when the behaviour is removed.
6. **Comments say what the code does now.** Update or delete a comment when the behaviour it describes changes.
7. **Privacy and opt-in defaults.** Anything that joins a torrent swarm must be gated on `torrent_fallback` or an explicit torrent `--base`, and must run only after every HTTP source has failed.

## Install and release

Build to a temp path, then `mv` over `~/.local/bin/lobster` so a running mpv is not disturbed. Releases are tags: pushing a `v*` tag runs the GoReleaser workflow.
