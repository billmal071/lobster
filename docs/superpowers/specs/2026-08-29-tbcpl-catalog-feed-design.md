# TBCPL Catalog Feed — Design

**Date:** 2026-08-29
**Status:** Approved (design)

## Summary

Integrate [TBCPL](https://tbcpl.lol) ("The Best Couch Potato List") into lobster as a
**dynamic domain / fallback feed** for three of its categories: **Movies & Shows**,
**Anime**, and **Live TV & Sports**.

TBCPL is not a media API. It is a curated directory of ~248 external streaming sites,
published as static JSON:

- Global list: `https://tbcpl.lol/links.json` (~20 KB, 109 sites)
- Regional overlays: `https://tbcpl.lol/Region-Links/links.<COUNTRY>.json` (15 countries,
  adding 139 more unique sites)

Schema: `{ "categories": [ { "id", "name", "sites": [ { "name", "url", "logo",
"enabled", "status" } ] } ] }`. `status` is one of `"trusted"`, `"new"`, or absent.

The integration treats TBCPL as the **source of candidate domains** and reuses lobster's
existing extractors and resolver to do the actual work. **No bespoke per-site scrapers are
authored** (in keeping with the project's standing preference).

## Goals

- Keep lobster's existing providers' mirror domains fresh from a live source.
- Add a best-effort path to play additional trusted movie/anime sites that use the common
  TMDB-id embed convention.
- Feed TBCPL's live-TV playlists into lobster's existing IPTV path.

## Non-goals

- Writing new per-site scrapers for arbitrary TBCPL sites.
- Playing sites that do not run software lobster already supports and do not fit a known
  embed template.
- Handling non-playlist iframe sports sites (deferred).

## Architecture

Two distinct plug points, because the categories behave differently:

1. **Movies & Anime** — providers are keyed to specific site *software* (flixhq, 1shows/
   vidzee, allanime, kimcartoon, soap2day…) with a per-provider `knownDomains` map plus
   config `DomainOverrides`, resolved through `provider.ResolveDomain`. A random TBCPL
   entry is only auto-playable if it runs software lobster already scrapes (mirror-match),
   or if it fits the common TMDB-id embed convention (generic embed).
2. **Live TV & Sports** — `provider.LiveTV` consumes *any* m3u/playlist URL via its
   `sources []string`. TBCPL live-TV playlist entries append directly — no per-site code.

### Component 1: `internal/tbcpl` — catalog client (new package)

- Fetches `links.json` plus optional region overlay(s).
- Parses into `[]Site{ Name, URL, Category, Status, Enabled, Regions }`.
- Disk cache at `~/.config/lobster/tbcpl-cache.json` with a **12h TTL**.
- On cache-miss **and** network failure, falls back to a small **embedded snapshot** baked
  at build time, so lobster works offline.
- Filter helpers: `ByCategory(id)`, `Trusted()`, `Enabled()`.

### Component 2: Mirror-match mapper (movies/anime, safe path)

- Maps a site's host root to a provider lobster already scrapes, reusing the keys in
  `provider.knownDomains` and the `cmd.newProvider()` selector
  (`flixhq`→FlixHQ, `1shows`/`1flex`/`1tube`→TBCPL(1shows), `allanime`→AllAnime,
  `kimcartoon`→KimCartoon, `soap2day`→Soap2Day, …).
- Matched TBCPL domains are merged into `DomainOverrides[provider]` at runtime and picked
  up by `ResolveDomain`.
- Unmatched entries fall through to Component 3.

### Component 3: Generic TMDB-embed provider `internal/provider/tbcplembed.go` (new)

- Modeled on `VidNest` / `VaPlayer`: `Search` / `GetSeasons` / `GetEpisodes` use TMDB's
  keyless multi-search via the package's existing shared helpers.
- For each **trusted, unmatched** movie/anime site, builds candidate embed URLs from a
  small template set keyed by host + TMDB id:
  - movie: `{origin}/embed/movie/{tmdb}`, `{origin}/movie/{tmdb}`, `{origin}/e/{tmdb}`
  - tv: same shapes with `/{season}/{episode}` appended
- Fetches the candidate page, sniffs `<iframe src>`, then hands off to the existing
  `extract.ResolveForURL(embed, origin)` dispatcher
  (megacloud / vidzee / byse / netu / vidwish).
- Implements `StreamProvider`. The existing `resolver` races candidates and health-demotes
  failures.
- Best-effort: sites that fit no template are **logged as skipped** (no silent caps).

### Component 4: Live TV & Sports feed

- From TBCPL's `livetv` category, entries that are m3u/playlist URLs are appended to
  `LiveTV.sources` alongside iptv-org and user playlists.
- Non-playlist iframe sports sites are logged and skipped this pass.

### Component 5: Wiring & config

- `cmd.fallbackProviders()` gains the generic TMDB-embed provider (trusted sites) and
  applies mirror-match domain injection before constructing providers.
- `provider.NewLiveTV` sources get TBCPL live-tv playlists merged in.
- New config keys:
  - `tbcpl_feed` (bool, default **on**)
  - `tbcpl_region` (string; adds one region overlay; default global-only)
  - `tbcpl_include_untrusted` (bool, default **off**; keeps the generic-embed race small
    and fast)

## Data flow

```
tbcpl.lol/links.json (+region)  ->  internal/tbcpl catalog (cache/snapshot)
        |                                   |                      |
   movies/anime                        movies/anime            livetv
   mirror-match  --> DomainOverrides    generic embed -->       playlists -->
        |            (existing            (TMDB id ->            LiveTV.sources
        v             providers)           iframe sniff ->             |
   resolver race <------------------------ extract.ResolveForURL)      v
        |                                                        LiveTV browse/play
        v
   playable stream
```

## Error handling

- Catalog fetch failure → use cache; cache stale/missing → embedded snapshot; log the
  degradation.
- Mirror-match with no known provider → skip silently (expected, common).
- Generic embed: no template match / no iframe / extractor failure → return error for that
  candidate; resolver demotes it via health store. Skips logged.
- Live-tv non-playlist entry → logged and skipped.

## Testing

- Catalog parse from a fixture JSON.
- Cache TTL behavior + offline embedded-snapshot fallback.
- Mirror-match host→provider mapping table.
- Generic embed: template build + `<iframe>` sniff via `httptest`, then extractor dispatch.
- Live-tv m3u append.
- Resolver integration with a fake TBCPL-sourced source.

## Honest scope note

Live TV/sports and mirror-matching deliver reliable value. The generic-embed path is
genuinely best-effort — it plays the subset of trusted TMDB-id embed sites and quietly
demotes the rest. It adds no bespoke per-site scrapers.
