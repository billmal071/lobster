# Revive Dead Providers — Design

Date: 2026-09-01
Branch: feat/wire-idle-providers (builds on 824374f)

## Problem

Lobster's fallback chain has lost providers to domain deaths:

- **flixhq.to** — dropped in 824374f because its origin is down (Cloudflare 522
  from multiple vantages) and every attempt burned the full request timeout.
- **AllAnime** — API is behind a Cloudflare bot challenge and its watch path has
  been crypto-gated since Aug 2026. Not legitimately revivable.

A mirror hunt (2026-09-01) found **no live FlixHQ-engine deployment**: flixhq.to,
flixhq.click, flixhq.pe, flixhq.bz, sflix.to, myflixerz.to all return Cloudflare
522 (origin down), suggesting the engine family shares one dead backend cluster.
flixhq.ws (separate deployment) is alive.

Goal: providers come back **automatically** the moment any mirror resurfaces,
and cost near-zero while dead.

## Design

### 1. Health-gated FlixHQ revival

- Expand `knownDomains["flixhq"]` in `internal/provider/failover.go` to:
  `flixhq.to, flixhq.click, flixhq.pe, flixhq.bz, sflix.to, myflixerz.to`.
  The last two are same-engine deployments (identical `flw-item` markup and
  `/ajax/` endpoints); the existing scraper works on them unchanged. This reuses
  the existing scraper — no new scraper is authored.
- Re-add FlixHQ to `fallbackProviders` (cmd/fallback.go), gated: a new
  `provider.FirstHealthyDomain(name)` probes all candidate domains **in
  parallel** (HEAD, ~3s timeout each; wall clock ≈ one probe) and returns the
  first healthy domain by list preference. If none is healthy, FlixHQ is not
  added to the chain at all.
- Probe results are cached in-memory for the process lifetime (sync.Once per
  provider name) so the gate runs once per session, not per search.
- PR #36's TBCPL mirror feed extends the same candidate list dynamically via
  `MergeOverrides`; no coupling changes needed here.

### 2. AllAnime retirement

- Remove AllAnime from `fallbackProviders`; keep the code and tests.
- Comment at the removal site: Cloudflare challenge + crypto gate, AniPub
  (anipub.xyz → megaplay.buzz, verified alive) is the anime path.

### 3. IPv6-robust probing

This dev machine's broken IPv6 made live domains look dead (TLS over v6 never
completed while v4 worked). Health probes must not repeat that mistake:

- The probe HTTP client uses a dialer that attempts IPv4 and IPv6 concurrently
  and takes whichever connects first (Go's default Happy Eyeballs via
  `net.Dialer{DualStack}` semantics is acceptable; verify the current transport
  isn't pinning v6). If needed, probe falls back to an explicit `tcp4` dial when
  the default dial fails.

### 4. Testing

- `FirstHealthyDomain`: healthy stub server wins; hanging server (never
  responds) does not block past the timeout; all-dead list returns "" and the
  provider is omitted from `fallbackProviders`.
- Parallelism bound: total gate time ≈ single probe timeout, not sum.
- Extend `cmd/fallback_providers_test.go`: FlixHQ present when a domain is
  healthy, absent when none are; AllAnime absent unconditionally.
- All probes in tests hit `httptest` servers — no live-network tests.

## Out of scope

- Reviving AllAnime (bot-challenge bypass is off the table).
- New scrapers for unrelated sites.
- Persisting probe results across runs (session cache only).
