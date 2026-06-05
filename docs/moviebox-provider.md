# MovieBox Provider Implementation

## Overview

MovieBox (`h5.aoneroom.com` / `moviebox.ph`) is a self-hosted streaming platform backed by Alibaba Cloud infrastructure. It exposes a clean JSON REST API with no Cloudflare or bot protection. Streams are delivered as direct m3u8/MP4 from the `macdn.aoneroom.com` CDN — no third-party embed hosts are involved, so no embed extraction layer is needed.

The V3 mobile API is recommended over the V1/V2 web API because it returns cleaner JSON with full season/episode granularity, and the HMAC signing is simple to implement.

---

## API Reference

**Base URL:** `https://api3.aoneroom.com` (also `api4`–`api6`; rotate on failure)  
**Path prefix:** `/wefeed-mobile-bff/subject-api/`

| Operation | Method | Endpoint | Key params |
|-----------|--------|----------|------------|
| Search | POST | `/search/v2` | `{"keyword": "...", "page": 1, "perPage": 20}` |
| Detail | GET | `/get` | `subjectId` |
| Seasons | GET | `/season-info` | `subjectId` |
| Play info | GET | `/play-info` | `subjectId`, `se` (season), `ep` (episode) |
| Subtitles | GET | `/get-ext-captions` | `subjectId` |

Trending/recent use:  
`GET /wefeed-mobile-bff/tab-operating?page=1&tabId=<id>&version=<ver>`

---

## Content ID System

Two-tier IDs returned from search:

- **`subjectId`** — 64-bit integer (e.g. `1857349212451623008`). Required for all API calls.
- **`detailPath`** — alphanumeric slug (e.g. `KHgp5s6Gcd2`). Used in display URLs only.

Store `subjectId` as the provider ID in `media.SearchResult.ID`.

---

## HMAC-MD5 Request Signing

Every V3 request requires two headers:

```
X-Client-Token: {ts},{md5(reverse(ts))}
x-tr-signature:  {ts}|2|{base64(hmac-md5(canonical, key))}
```

Where `ts` is the current Unix timestamp in milliseconds.

**Canonical string** (newline-separated, in this order):
```
{METHOD}
{Accept header value}
{Content-Type header value}
{body length as decimal string}
{ts}
{md5(request body)}
{sorted query string path}  // e.g. "/wefeed-mobile-bff/subject-api/get?subjectId=123"
```

**HMAC key** (base64-decoded before use):
```
76iRl07s0xSN9jqmEWAt79EBJZulIQIsV64FZr2O
```

Backup key: `Xqn2nnO41/L92o1iuXhSLHTbXvY4Z5ZZ62m8mSLA`

The response includes an `x-user` header containing a bearer token — capture it and send as `Authorization: Bearer <token>` on subsequent requests within the same session.

**Go signing sketch:**

```go
import (
    "crypto/hmac"
    "crypto/md5"
    "encoding/base64"
    "encoding/hex"
    "fmt"
    "strconv"
    "time"
)

func sign(method, path, body string, key []byte) (clientToken, signature string) {
    ts := strconv.FormatInt(time.Now().UnixMilli(), 10)

    // X-Client-Token
    rev := reverse(ts)
    h := md5.Sum([]byte(rev))
    clientToken = ts + "," + hex.EncodeToString(h[:])

    // x-tr-signature canonical string
    bodyMD5 := md5.Sum([]byte(body))
    canonical := fmt.Sprintf("GET\napplication/json\napplication/json\n%d\n%s\n%s\n%s",
        len(body), ts, hex.EncodeToString(bodyMD5[:]), path)

    mac := hmac.New(md5.New, key)
    mac.Write([]byte(canonical))
    sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
    signature = ts + "|2|" + sig
    return
}
```

---

## Provider Interface Mapping

| `Provider` method | MovieBox API call |
|-------------------|-------------------|
| `Search(query)` | `POST /search/v2` |
| `GetDetails(id)` | `GET /get?subjectId={id}` |
| `GetSeasons(id)` | `GET /season-info?subjectId={id}` |
| `GetEpisodes(id, seasonID)` | `GET /season-info?subjectId={id}` → filter by season number |
| `GetServers(id, episodeID)` | Not applicable — returns a synthetic single "MovieBox" server |
| `GetEmbedURL(serverID)` | Not applicable — MovieBox uses `Watch()` directly |
| `Trending(type)` | `GET /tab-operating?tabId=<movie|tv tabId>` |
| `Recent(type)` | Same tab-operating endpoint, different tabId |

MovieBox returns streams directly, so it should implement `StreamProvider` (the `Watch` extension interface) rather than the embed-based `Provider` flow:

```go
// Watch resolves a direct stream URL for a movie or TV episode.
// For movies, se and ep are 0.
func (m *MovieBox) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error)
```

Internally, `Watch` calls `GET /play-info?subjectId={mediaID}&se={se}&ep={ep}` and returns the highest-quality m3u8 URL from the `hls[]` array (fall back to `streams[0].url` MP4 if HLS is empty).

---

## Response Shapes (abbreviated)

**Search result item:**
```json
{
  "subjectId": 1857349212451623008,
  "detailPath": "KHgp5s6Gcd2",
  "name": "Project Hail Mary",
  "subjectType": 1,
  "releaseYear": 2025,
  "seasonNum": 0,
  "episodeNum": 0
}
```
`subjectType`: `1` = movie, `2` = TV series, `3` = animation.

**Play-info response:**
```json
{
  "hls": [
    {"id": "abc123", "url": "https://macdn.aoneroom.com/.../index.m3u8", "quality": "1080p"}
  ],
  "streams": [
    {"id": "def456", "url": "https://macdn.aoneroom.com/.../video.mp4", "quality": "720p"}
  ],
  "hasResource": true
}
```
If `hasResource` is `false`, no streams are available for that region/title.

---

## File Structure

```
internal/provider/
  moviebox.go        // MovieBox struct, Provider + StreamProvider impl
  moviebox_test.go   // Unit tests with recorded HTTP fixtures
  testdata/
    moviebox/
      search.json
      detail.json
      season_info.json
      play_info.json
```

---

## Notes

- The API host pool (`api3`–`api6.aoneroom.com`) should be tried in order on connection failure — add a `hosts []string` field to the struct and rotate.
- `hasResource: false` on `play-info` means geo-restriction. Surface this as a descriptive error rather than a generic "no stream found".
- Subtitles come from a separate `GET /get-ext-captions?subjectId={id}` call; fetch alongside play-info and attach to `media.Stream.Subtitles`.
- The `x-user` bearer token from the first response should be stored on the struct and reused for the session lifetime — it avoids re-authentication on every request.
