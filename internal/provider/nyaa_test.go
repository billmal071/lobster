package provider

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lobster/internal/media"
)

// nyaaFixture is a trimmed copy of the live feed for q=One+Punch+Man
// (March 2026): real titles, hashes, sizes and seed counts, with the fields
// the provider does not read removed. It carries two series (season 3 under
// several naming conventions, and Erai-raws' year-tagged form that has no
// season marker), two batch packs and one season pack.
const nyaaFixture = `<rss xmlns:atom="http://www.w3.org/2005/Atom" xmlns:nyaa="https://nyaa.si/xmlns/nyaa" version="2.0">
<channel>
<title>Nyaa - &#34;One Punch Man&#34; - Torrent File RSS</title>
<link>https://nyaa.si/</link>
<atom:link href="https://nyaa.si/?page=rss" rel="self" type="application/rss+xml" />
<item>
<title>[SubsPlease] One-Punch Man S3 - 12 (1080p) [B7B8D40B].mkv</title>
<link>https://nyaa.si/download/2058733.torrent</link>
<nyaa:seeders>153</nyaa:seeders>
<nyaa:leechers>1</nyaa:leechers>
<nyaa:infoHash>fc13cf7e7b8483de7cd1dbe63fef16afa49e6322</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>1.4 GiB</nyaa:size>
</item>
<item>
<title>[SubsPlease] One-Punch Man S3 - 12 (720p) [50FE205C].mkv</title>
<link>https://nyaa.si/download/2058732.torrent</link>
<nyaa:seeders>20</nyaa:seeders>
<nyaa:leechers>0</nyaa:leechers>
<nyaa:infoHash>e61f34e11efe64a43535c64c959fd40f6d5b8180</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>711.8 MiB</nyaa:size>
</item>
<item>
<title>[SubsPlease] One-Punch Man S3 - 12 (480p) [ADF7399D].mkv</title>
<link>https://nyaa.si/download/2058731.torrent</link>
<nyaa:seeders>10</nyaa:seeders>
<nyaa:leechers>0</nyaa:leechers>
<nyaa:infoHash>cd0b32db8f73a40a89ef64d4e47ac5f483e04595</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>369.7 MiB</nyaa:size>
</item>
<item>
<title>[Erai-raws] One Punch Man (2025) - 12 [1080p CR WEBRip HEVC AAC][MultiSub][FB20C752]</title>
<link>https://nyaa.si/download/2059182.torrent</link>
<nyaa:seeders>24</nyaa:seeders>
<nyaa:leechers>0</nyaa:leechers>
<nyaa:infoHash>703c7535611e706984e640fa1c54d4c2b7727428</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>811.8 MiB</nyaa:size>
</item>
<item>
<title>[Anime Time] One Punch Man - S03E12 [1080p][HEVC 10bit x265][AAC][Dual-Audio] [Multi Sub] [Weekly]</title>
<link>https://nyaa.si/download/2076860.torrent</link>
<nyaa:seeders>10</nyaa:seeders>
<nyaa:leechers>0</nyaa:leechers>
<nyaa:infoHash>fa3b4bd8f19dd16b8bce8031ea43d136d88c77d8</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>503.7 MiB</nyaa:size>
</item>
<item>
<title>One Punch Man S03E12 Ultimate Lifeform 1080p CR WEB-DL DUAL AAC2.0 H 264-VARYG (Dual-Audio, Multi-Subs)</title>
<link>https://nyaa.si/download/2076771.torrent</link>
<nyaa:seeders>77</nyaa:seeders>
<nyaa:leechers>0</nyaa:leechers>
<nyaa:infoHash>e80b214052cea7c74e6253359d17428b1c0e71a8</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>1.4 GiB</nyaa:size>
</item>
<item>
<title>One.Punch.Man.S03E11.1080p.CR.WEBRip.10bits.x265-Rapta</title>
<link>https://nyaa.si/download/2056750.torrent</link>
<nyaa:seeders>9</nyaa:seeders>
<nyaa:leechers>0</nyaa:leechers>
<nyaa:infoHash>2aaea3e4bc5b95b2d3cfb603cef045289d612e86</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>266.5 MiB</nyaa:size>
</item>
<item>
<title>One-Punch Man Season 3 - 11 | S03 [English Dub][1080p]</title>
<link>https://nyaa.si/download/2074332.torrent</link>
<nyaa:seeders>8</nyaa:seeders>
<nyaa:leechers>0</nyaa:leechers>
<nyaa:infoHash>8fb2d2f0700ed986a91193d425b1c0cc8fa55011</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>1.4 GiB</nyaa:size>
</item>
<item>
<title>[SubsPlease] One-Punch Man S3 (00-12) (1080p) [Batch]</title>
<link>https://nyaa.si/download/2084806.torrent</link>
<nyaa:seeders>54</nyaa:seeders>
<nyaa:leechers>4</nyaa:leechers>
<nyaa:infoHash>0a26902b0a65213684d661d2573932b14725820e</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>17.8 GiB</nyaa:size>
</item>
<item>
<title>[Ironclad] One-Punch Man - S03 [WEB.1080p.AV1] | One-Punch Man (Dual-Audio, Multi-Subs)</title>
<link>https://nyaa.si/download/2077114.torrent</link>
<nyaa:seeders>119</nyaa:seeders>
<nyaa:leechers>4</nyaa:leechers>
<nyaa:infoHash>43cf8ba507f5914cf79c2aaf87f2bc45695dd298</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>4.5 GiB</nyaa:size>
</item>
<item>
<title>[Anime Time] One Punch Man - Season 3 (Episodes 1-12) [1080p][HEVC 10bit x265][AAC][Dual-Audio] [Multi Sub] [Batch]</title>
<link>https://nyaa.si/download/2076881.torrent</link>
<nyaa:seeders>55</nyaa:seeders>
<nyaa:leechers>3</nyaa:leechers>
<nyaa:infoHash>c71d7e4c3fd5e63b7bf6edcc26be7a055229ceda</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>6.3 GiB</nyaa:size>
</item>
<item>
<title>[ASW] One-Punch Man S3 - 12 [1080p HEVC x265 10Bit][AAC]</title>
<link>https://nyaa.si/download/2058759.torrent</link>
<nyaa:seeders>26</nyaa:seeders>
<nyaa:leechers>0</nyaa:leechers>
<nyaa:infoHash>44c130b70426081682c1e5f841153b080bc16fc3</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>495.8 MiB</nyaa:size>
</item>
<item>
<title>[Judas] One-Punch Man - S03E11 [1080p][HEVC x265 10bit][Eng-Subs] (Weekly)</title>
<link>https://nyaa.si/download/2056810.torrent</link>
<nyaa:seeders>19</nyaa:seeders>
<nyaa:leechers>0</nyaa:leechers>
<nyaa:infoHash>c8ab347ea8389d3c520a1cd766f6e8d31b174e99</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>427.5 MiB</nyaa:size>
</item>
</channel>
</rss>
`

// nyaaFrierenFixture is trimmed from the live feed for q=Frieren (November
// 2026). The same show is spelt "Journey's End" and "Journeys End" by
// different groups. The Feibanyama 2160p item's seed count is set to 0 (it
// was 143 when fetched) so the dead-torrent rule has something to drop.
const nyaaFrierenFixture = `<rss xmlns:atom="http://www.w3.org/2005/Atom" xmlns:nyaa="https://nyaa.si/xmlns/nyaa" version="2.0">
<channel>
<title>Nyaa - &#34;Frieren&#34; - Torrent File RSS</title>
<link>https://nyaa.si/</link>
<item>
<title>[Feibanyama] Frieren Beyond Journeys End S02E10 [IQIYI WebRip 2160p HEVC OPUS Multi-Subs] (Sousou no Frieren)</title>
<link>https://nyaa.si/download/2089001.torrent</link>
<nyaa:seeders>0</nyaa:seeders>
<nyaa:leechers>2</nyaa:leechers>
<nyaa:infoHash>1db7f40f7151d2e1a4f9c3968b1b9780378c25f1</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>1.8 GiB</nyaa:size>
</item>
<item>
<title>Frieren Beyond Journeys End S02E10 A Beautiful Sight 1080p CR WEB-DL DUAL AAC2.0 H 264-VARYG (Sousou no Frieren, Dual-Audio, Multi-Subs)</title>
<link>https://nyaa.si/download/2089002.torrent</link>
<nyaa:seeders>112</nyaa:seeders>
<nyaa:leechers>3</nyaa:leechers>
<nyaa:infoHash>4bd7e6ae6e8a7f9e63edfbb995b384b7dc7a3e64</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>1.4 GiB</nyaa:size>
</item>
<item>
<title>[Yameii] Frieren: Beyond Journey's End - S02E10 [English Dub] [CR WEB-DL 1080p H264 AAC] [60B4DD4F] (Sousou no Frieren Season 2 | S2)</title>
<link>https://nyaa.si/download/2089003.torrent</link>
<nyaa:seeders>65</nyaa:seeders>
<nyaa:leechers>1</nyaa:leechers>
<nyaa:infoHash>c9e31f3219a66031df16ad319862aeb9fdacee55</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>1.4 GiB</nyaa:size>
</item>
<item>
<title>[EMBER] Frieren: Beyond Journey's End S02E09 [1080p] [Dual Audio HEVC WEBRip DDP] (Sousou no Frieren Season 2)</title>
<link>https://nyaa.si/download/2089004.torrent</link>
<nyaa:seeders>33</nyaa:seeders>
<nyaa:leechers>0</nyaa:leechers>
<nyaa:infoHash>7b76f36ae960162d07f74fbd28de5c44b04fda22</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>485.4 MiB</nyaa:size>
</item>
<item>
<title>[Anime Time] Frieren Beyond Journeys End (Sousou no Frieren) - S02E09 [1080p][HEVC 10bit x265][AAC][Dual-Audio] [Multi Sub] [Weekly]</title>
<link>https://nyaa.si/download/2089005.torrent</link>
<nyaa:seeders>17</nyaa:seeders>
<nyaa:leechers>0</nyaa:leechers>
<nyaa:infoHash>ae5519107ecec0b299ca40c67a8c7313330ff78c</nyaa:infoHash>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>566.7 MiB</nyaa:size>
</item>
</channel>
</rss>
`

const (
	nyaaS3ID      = "nyaa/One-Punch+Man?s=3"
	nyaaEraiID    = "nyaa/One+Punch+Man?y=2025"
	nyaaFrierenID = "nyaa/Frieren+Beyond+Journeys+End?s=2"
)

// nyaaStub serves the One Punch Man fixture for every query and counts requests.
func nyaaStub(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	return nyaaStubWith(t, nyaaFixture)
}

func nyaaStubWith(t *testing.T, fixture string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("page") != "rss" || r.URL.Query().Get("c") != nyaaCategory {
			t.Errorf("unexpected feed URL: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(fixture))
	}))
	old := nyaaBase
	nyaaBase = srv.URL
	t.Cleanup(func() { nyaaBase = old; srv.Close() })
	return srv, &calls
}

// Every title here is copied from the live English-translated feed.
func TestNyaaParseTitle(t *testing.T) {
	cases := []struct {
		title  string
		group  string
		series string
		season int
		ep     int
		height int
		year   string
		batch  bool
	}{
		{"Show (2019-2020) - 12", "", "Show", 0, 12, 0, "", false},
		{"Show (01-12)", "", "", 0, 0, 0, "", true},
		{"Show - 05 (2019-2020) [1080p]", "", "Show", 0, 5, 1080, "", false},
		{"Show (2019-2020) - 05", "", "Show", 0, 5, 0, "", false},
		{"[Group] Show (01-12) [1080p]", "Group", "", 0, 0, 1080, "", true},
		{"One Piece (1100-1104)", "", "", 0, 0, 0, "", true},
		{"[SubsPlease] One-Punch Man S3 - 12 (1080p) [B7B8D40B].mkv", "SubsPlease", "One-Punch Man", 3, 12, 1080, "", false},
		{"[Erai-raws] One Punch Man (2025) - 12 [1080p CR WEBRip HEVC AAC][MultiSub][FB20C752]", "Erai-raws", "One Punch Man", 0, 12, 1080, "2025", false},
		{"[Erai-raws] One Punch Man (2025) - 12 (REPACK) [720p CR WEB-DL AVC AAC][MultiSub][91E83E57]", "Erai-raws", "One Punch Man", 0, 12, 720, "2025", false},
		{"[Anime Time] One Punch Man - S03E12 [1080p][HEVC 10bit x265][AAC][Dual-Audio] [Multi Sub] [Weekly]", "Anime Time", "One Punch Man", 3, 12, 1080, "", false},
		{"[ToonsHub] One-Punch Man S03E11 1080p CR WEB-DL DUAL AAC2.0 H.264 (Dual-Audio, Multi-Subs)", "ToonsHub", "One-Punch Man", 3, 11, 1080, "", false},
		{"One Punch Man S03E12 Ultimate Lifeform 1080p CR WEB-DL DUAL AAC2.0 H 264-VARYG (Dual-Audio, Multi-Subs)", "VARYG", "One Punch Man", 3, 12, 1080, "", false},
		{"One.Punch.Man.S03E11.1080p.CR.WEBRip.10bits.x265-Rapta", "Rapta", "One Punch Man", 3, 11, 1080, "", false},
		{"One-Punch Man Season 3 - 11 | S03 [English Dub][1080p]", "", "One-Punch Man", 3, 11, 1080, "", false},
		{"[New-raws] One Punch Man S3 - 12 END [1080p] [Bstation].mkv", "New-raws", "One Punch Man", 3, 12, 1080, "", false},
		{"[Raze] One Punch Man S3 - 12 x265 10bit 1080p 143.8561fps.mkv", "Raze", "One Punch Man", 3, 12, 1080, "", false},
		{"[Erai-raws] Kusuriya no Hitorigoto 3rd Season - 06 [1080p CR WEBRip HEVC AAC][MultiSub][739AA102]", "Erai-raws", "Kusuriya no Hitorigoto", 3, 6, 1080, "", false},
		{"[Judas] Hotaru no Yomeiri (Firefly Wedding) - S01E05 [1080p][HEVC x265 10bit][Multi-Subs] (Weekly)", "Judas", "Hotaru no Yomeiri", 1, 5, 1080, "", false},
		{"[LeoSubs] The Great Ruler - Episode 98 - 4K HEVC 10Bit - 60FPS - ENG", "LeoSubs", "The Great Ruler", 0, 98, 2160, "", false},
		{"[Piyoko] Onegai AiPri - 31 [WEB AMZN 1080p h264 AC3 2.0]", "Piyoko", "Onegai AiPri", 0, 31, 1080, "", false},
		{"[ASW] Dark Summoner to Dekiteiru - Omake Shorts - 01 [1080p HEVC x265 10Bit][AAC]", "ASW", "Dark Summoner to Dekiteiru - Omake Shorts", 0, 1, 1080, "", false},
		{"[Feibanyama] Black Clover S02E01 [BILIBILI WebRip 4320p H265 Veryslow AAC Multi-Subs]", "Feibanyama", "Black Clover", 2, 1, 4320, "", false},
		{"[HnY] Beyblade X 138 - The Kingdom's Way.mkv", "HnY", "Beyblade X", 0, 138, 0, "", false},
		{"Welsh And Shedar  01", "", "Welsh And Shedar", 0, 1, 0, "", false},
		// A trailing year is a film's release date, not episode 2016.
		{"[Group] Some Film 2016 [1080p][AAC]", "Group", "", 0, 0, 1080, "", false},
		{"Beyblade X 2024 - The Kingdom's Way", "", "Beyblade X", 0, 2024, 0, "", false},
		{"[Group] Show 01 - Happy New Year 2026 [1080p]", "Group", "Show", 0, 1, 1080, "", false},
		{"[Group] Show - 05 (2019) [1080p]", "Group", "Show", 0, 5, 1080, "", false},
		{"[SubsPlease] 86 - 12 (1080p) [ABCD1234].mkv", "SubsPlease", "86", 0, 12, 1080, "", false},
		{"[Group] Show 01-12 [1080p]", "Group", "", 0, 0, 1080, "", true},
		{"[Group] Show - 05 [WEB 2019-2020] [1080p]", "Group", "Show", 0, 5, 1080, "", false},
		{"[Group] Show - 05 [WEB 1998-2003] [1080p]", "Group", "Show", 0, 5, 1080, "", false},
		{"Sayonara.Lara.2026.S01E11.MULTI.Audio.Sub.1080p.BDRip.CUSTOM.x264.PCM-KuroNeko VOSTFR Goodbye Lara", "", "Sayonara Lara", 1, 11, 1080, "2026", false},
		{"[AnoZu] One Piece S23E03 1080p CR WEB-DL Dual-Audio DDP 2.0 H.264", "AnoZu", "One Piece", 23, 3, 1080, "", false},
		// "EP<n>" with no separator.
		{"[ToonsHub] One Piece EP1158 1080p CR WEB-DL DUAL AAC2.0 H.264 (Dual-Audio, Multi-Subs)", "ToonsHub", "One Piece", 0, 1158, 1080, "", false},
		// Absolute numbering dressed as SxxEyyyy: the season is not a season.
		{"One Piece S31E1156 The Long-sought Elbaph The Big Reunion Banquet 1080p CR WEB-DL DUAL AAC2.0 H.264-VARYG (Dual-Audio, Multi-Subs)", "VARYG", "One Piece", 0, 1156, 1080, "", false},
		{"One Piece S01E1178 Protect History and the Future-Robin and Gaban Go on the Attack 1080p BILI WEB-DL AAC2.0 H.264-VARYG (Multi-Subs)", "VARYG", "One Piece", 0, 1178, 1080, "", false},
		// Specials (season 0) are not episode 1 of the series.
		{"One Piece S00E01 One Piece Heroines 1080p CR WEB-DL DUAL AAC2.0 H.264-VARYG (Dual-Audio, Multi-Subs)", "VARYG", "", 0, 0, 1080, "", false},
		// Packs: skipped, and the reason must be Batch rather than a missed
		// episode so a live run can tell the two apart.
		{"[SubsPlease] One-Punch Man S3 (00-12) (1080p) [Batch]", "SubsPlease", "", 0, 0, 1080, "", true},
		{"[Anime Time] One Punch Man - Season 3 (Episodes 1-12) [1080p][HEVC 10bit x265][AAC][Dual-Audio] [Multi Sub] [Batch]", "Anime Time", "", 0, 0, 1080, "", true},
		{"[New-raws] One Punch Man S3 - 01~12 [1080p] [Bstation].mkv", "New-raws", "", 0, 0, 1080, "", true},
		{"[No0bSubs] Frieren - Beyond Journey's End S1+S2 01-38 + Specials + Soundtracks (1080p AV1 SUPER MULTI Audio)", "No0bSubs", "", 0, 0, 1080, "", true},
		{"[DubZoku] Jujutsu Kaisen S03E09-E12 v2 (WEB 1080p HEVC DDP) [Dual-Audio]", "DubZoku", "", 0, 0, 1080, "", true},
		{"[MicroHatSubs] One Piece 1100-1104 (BD 1080p AV1 Opus)", "MicroHatSubs", "", 0, 0, 1080, "", true},
		// Season packs with no episode number: not a batch by name, but
		// nothing to play either.
		{"[Ironclad] One-Punch Man - S03 [WEB.1080p.AV1] | One-Punch Man (Dual-Audio, Multi-Subs)", "Ironclad", "", 0, 0, 1080, "", false},
		{"One Punch Man S03 1080p CR WEB-DL DUAL AAC2.0 H 264-VARYG (Dual-Audio, Multi-Subs)", "VARYG", "", 0, 0, 1080, "", false},
		{"[KOTEX] Sousou no Frieren I-II Subs+Fonts for ReinForce [BD].zip", "KOTEX", "", 0, 0, 0, "", false},
	}
	for _, c := range cases {
		got := parseNyaaTitle(c.title)
		if got.Batch != c.batch {
			t.Errorf("%q: batch = %v, want %v", c.title, got.Batch, c.batch)
		}
		if got.Group != c.group {
			t.Errorf("%q: group = %q, want %q", c.title, got.Group, c.group)
		}
		if got.Height != c.height {
			t.Errorf("%q: height = %d, want %d", c.title, got.Height, c.height)
		}
		if c.batch {
			continue
		}
		if got.Episode != c.ep {
			t.Errorf("%q: episode = %d, want %d", c.title, got.Episode, c.ep)
		}
		if c.ep == 0 {
			continue
		}
		if got.Series != c.series {
			t.Errorf("%q: series = %q, want %q", c.title, got.Series, c.series)
		}
		if got.Season != c.season {
			t.Errorf("%q: season = %d, want %d", c.title, got.Season, c.season)
		}
		if got.Year != c.year {
			t.Errorf("%q: year = %q, want %q", c.title, got.Year, c.year)
		}
	}
}

// "One-Punch Man" and "One Punch Man" are the same show; season 3 and the
// unmarked run are not.
func TestNyaaKeyMergesPunctuationButNotSeasons(t *testing.T) {
	if nyaaKey("One-Punch Man", 3) != nyaaKey("One Punch Man", 3) {
		t.Error("hyphenation split one series in two")
	}
	if nyaaKey("One Punch Man", 1) != nyaaKey("One Punch Man", 0) {
		t.Error("season 1 and no season marker must be the same series")
	}
	if nyaaKey("One Punch Man", 3) == nyaaKey("One Punch Man", 0) {
		t.Error("a sequel must not merge into the first season")
	}
	// Apostrophes are elided, not spaced: "Journey's" and "Journeys" are one word.
	for _, spelt := range []string{"Frieren: Beyond Journey's End", "Frieren Beyond Journey’s End", "Frieren Beyond Journey`s End"} {
		if nyaaKey(spelt, 2) != nyaaKey("Frieren Beyond Journeys End", 2) {
			t.Errorf("%q keys as %q, splitting the series", spelt, nyaaKey(spelt, 2))
		}
	}
}

func TestNyaaEpisodeRef(t *testing.T) {
	for _, c := range []struct {
		in      string
		season  int
		episode int
		ok      bool
	}{
		{"12", 0, 12, true},
		{" 7 ", 0, 7, true},
		{"", 0, 0, false},
		{"nyaa/Frieren+Beyond+Journeys+End?s=2:1:2", 1, 2, true},
		{"nyaa/One+Piece:23:3", 23, 3, true},
		{"nyaa/Frieren%3A+Beyond+Journey%27s+End:2:10", 2, 10, true},
		{"abc", 0, 0, false},
		{"0", 0, 0, false},
		{"nyaa/One+Piece:0:3", 0, 0, false},
		{"nyaa/One+Piece:x:3", 0, 0, false},
	} {
		season, episode, err := nyaaEpisodeRef(c.in)
		if (err == nil) != c.ok || season != c.season || episode != c.episode {
			t.Errorf("nyaaEpisodeRef(%q) = %d, %d, %v; want %d, %d, ok=%v", c.in, season, episode, err, c.season, c.episode, c.ok)
		}
	}
}

// The fallback resolver sends "<showID>:<season>:<episode>" and the show ID
// of whichever search result matched, which for a sequel may be the unmarked
// series: season 3 of "nyaa/One-Punch+Man" lives at "?s=3".
func TestNyaaWatchAcceptsFallbackEpisodeForm(t *testing.T) {
	nyaaStub(t)
	n := NewNyaa()
	const s3ep12 = "magnet:?xt=urn:btih:fc13cf7e7b8483de7cd1dbe63fef16afa49e6322&"
	for _, c := range []struct{ id, episode string }{
		{nyaaS3ID, "12"},
		{nyaaS3ID, nyaaS3ID + ":1:12"},
		{nyaaS3ID, nyaaS3ID + ":3:12"},
		{"nyaa/One-Punch+Man", "nyaa/One-Punch+Man:3:12"},
	} {
		st, err := n.Watch(c.id, c.episode, "Default", "1080")
		if err != nil {
			t.Errorf("Watch(%q, %q): %v", c.id, c.episode, err)
			continue
		}
		if !strings.HasPrefix(st.URL, s3ep12) {
			t.Errorf("Watch(%q, %q) picked %s", c.id, c.episode, st.URL)
		}
	}
	// Season 1 of the unmarked series is the Erai-raws bucket, not season 3.
	st, err := n.Watch(nyaaEraiID, nyaaEraiID+":1:12", "Default", "1080")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.URL, "magnet:?xt=urn:btih:703c7535611e706984e640fa1c54d4c2b7727428&") {
		t.Errorf("season 1 of the unmarked series picked %s", st.URL)
	}
	if _, err := n.Watch(nyaaEraiID, nyaaEraiID+":2:12", "Default", "1080"); err == nil {
		t.Error("season 2 does not exist in the feed; Watch must fail")
	}
}

// Dead torrents are dropped while a live one exists, so "best" cannot pick
// an unseeded 2160p over a seeded 1080p; and the two spellings of the title
// land in one series.
func TestNyaaSkipsUnseededAndMergesSpellings(t *testing.T) {
	nyaaStubWith(t, nyaaFrierenFixture)
	n := NewNyaa()
	res, err := n.Search("Frieren")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != nyaaFrierenID || res[0].Episodes != 2 {
		t.Fatalf("results = %+v, want one series %s with 2 episodes", res, nyaaFrierenID)
	}
	servers, err := n.GetServers(nyaaFrierenID, "10")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"1080p VARYG (1.4 GiB, 112 seeds)",
		"1080p Yameii (1.4 GiB, 65 seeds)",
	}
	if len(servers) != len(want) {
		t.Fatalf("servers = %+v, want %v", servers, want)
	}
	for i, s := range servers {
		if s.Name != want[i] {
			t.Errorf("server %d = %q, want %q", i, s.Name, want[i])
		}
	}
	for _, q := range []string{"best", "", "2160"} {
		st, err := n.Watch(nyaaFrierenID, "10", "", q)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(st.URL, "magnet:?xt=urn:btih:4bd7e6ae6e8a7f9e63edfbb995b384b7dc7a3e64&") {
			t.Errorf("quality %q picked %s, want the seeded 1080p", q, st.URL)
		}
	}
}

func TestNyaaIDRoundTrip(t *testing.T) {
	for _, c := range []struct {
		base   string
		season int
		year   string
		id     string
	}{
		{"One-Punch Man", 3, "", nyaaS3ID},
		{"One Punch Man", 0, "2025", nyaaEraiID},
		{"One Punch Man", 0, "", "nyaa/One+Punch+Man"},
		{"Ranma", 2, "2024", "nyaa/Ranma?s=2&y=2024"},
		{"Frieren: Beyond Journey's End", 2, "", "nyaa/Frieren%3A+Beyond+Journey%27s+End?s=2"},
	} {
		if got := nyaaID(c.base, c.season, c.year); got != c.id {
			t.Errorf("nyaaID(%q, %d, %q) = %q, want %q", c.base, c.season, c.year, got, c.id)
		}
		base, season, year, err := parseNyaaID(c.id)
		if err != nil || base != c.base || season != c.season || year != c.year {
			t.Errorf("parseNyaaID(%q) = %q, %d, %q, %v", c.id, base, season, year, err)
		}
	}
	for _, bad := range []string{"", "nyaa/", "yts/123", "One Punch Man"} {
		if _, _, _, err := parseNyaaID(bad); err == nil {
			t.Errorf("parseNyaaID(%q) accepted", bad)
		}
	}
}

// Same-title adaptations from different years are different series: their
// episodes must not be merged, or playback can pick the other adaptation's
// torrent. Undated releases join a title's sole dated series — the common
// case of one group tagging the year and another not — but are not dealt to
// either of two.
func TestNyaaGroupKeepsAdaptationsApartByYear(t *testing.T) {
	rel := func(series, year string, ep int, hash string) nyaaRelease {
		return nyaaRelease{Series: series, Year: year, Episode: ep, Hash: hash, Seeders: 1}
	}
	series, _ := groupNyaa([]nyaaRelease{
		rel("Ranma", "1989", 1, "a"),
		rel("Ranma", "2024", 1, "b"),
		rel("Ranma", "", 2, "c"),
		rel("Frieren", "2023", 1, "d"),
		rel("Frieren", "", 2, "e"),
	})
	got := map[string][]string{}
	for _, s := range series {
		var hashes []string
		for _, r := range s.Releases {
			hashes = append(hashes, r.Hash)
		}
		got[nyaaID(s.Base, s.Season, s.Year)] = hashes
	}
	want := map[string][]string{
		"nyaa/Ranma?y=1989":   {"a"},
		"nyaa/Ranma?y=2024":   {"b"},
		"nyaa/Ranma":          {"c"},
		"nyaa/Frieren?y=2023": {"d", "e"},
	}
	if len(got) != len(want) {
		t.Fatalf("series = %v, want %v", got, want)
	}
	for id, hashes := range want {
		if strings.Join(got[id], ",") != strings.Join(hashes, ",") {
			t.Errorf("%s = %v, want %v", id, got[id], hashes)
		}
	}
}

// An ID that names an adaptation by year fetches only that one; an undated
// ID — an older ref — still resolves to a dated series when it is the only
// one of its title, and a movie request (no episode) is refused.
func TestNyaaWatchHonoursYearAndRefusesMovies(t *testing.T) {
	nyaaStub(t)
	n := NewNyaa()
	const erai = "magnet:?xt=urn:btih:703c7535611e706984e640fa1c54d4c2b7727428&"
	for _, id := range []string{nyaaEraiID, "nyaa/One+Punch+Man"} {
		st, err := n.Watch(id, "12", "Default", "1080")
		if err != nil {
			t.Fatalf("Watch(%q): %v", id, err)
		}
		if !strings.HasPrefix(st.URL, erai) {
			t.Errorf("Watch(%q) picked %s", id, st.URL)
		}
	}
	if _, err := n.Watch("nyaa/One+Punch+Man?y=1999", "12", "Default", "1080"); err == nil {
		t.Error("an adaptation the feed does not carry was served")
	}
	if _, err := n.Watch(nyaaS3ID, "", "", ""); err == nil || !strings.Contains(err.Error(), "TV only") {
		t.Errorf("movie request err = %v, want a TV-only refusal", err)
	}
	// A fallback episode ID naming season 2 against the season-3 series must
	// not return a season-3 magnet.
	if _, err := n.Watch(nyaaS3ID, nyaaS3ID+":2:12", "Default", "1080"); err == nil {
		t.Error("season 2 against a season-3 ID was served")
	}
}

func TestNyaaSearchGroupsTorrentsIntoSeries(t *testing.T) {
	nyaaStub(t)
	res, err := NewNyaa().Search("One Punch Man")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d series, want 2: %+v", len(res), res)
	}
	// The season-3 bucket has nine torrents against Erai-raws' one, so it
	// is listed first.
	s3 := res[0]
	if s3.ID != nyaaS3ID || s3.Title != "One-Punch Man Season 3" || s3.Type != media.TV {
		t.Errorf("season 3 result: %+v", s3)
	}
	if s3.Episodes != 2 || s3.Seasons != 1 {
		t.Errorf("season 3 counts: episodes %d seasons %d", s3.Episodes, s3.Seasons)
	}
	erai := res[1]
	if erai.ID != nyaaEraiID || erai.Title != "One Punch Man" || erai.Year != "2025" {
		t.Errorf("year-tagged result: %+v", erai)
	}
}

func TestNyaaGroupSkipsBatchesAndSeasonPacks(t *testing.T) {
	nyaaStub(t)
	n := NewNyaa()
	items, err := n.fetch(nyaaFeedURL("One Punch Man", ""))
	if err != nil {
		t.Fatal(err)
	}
	_, skipped := groupNyaa(items)
	if skipped != 3 {
		t.Errorf("skipped %d, want 3 (two batches, one season pack)", skipped)
	}
}

func TestNyaaGetEpisodesDistinctAscending(t *testing.T) {
	nyaaStub(t)
	n := NewNyaa()
	seasons, err := n.GetSeasons(nyaaS3ID)
	if err != nil || len(seasons) != 1 || seasons[0].Number != 3 || seasons[0].ID != "3" {
		t.Fatalf("GetSeasons = %+v, %v", seasons, err)
	}
	eps, err := n.GetEpisodes(nyaaS3ID, "3")
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 || eps[0].Number != 11 || eps[1].Number != 12 {
		t.Fatalf("episodes = %+v, want 11 then 12", eps)
	}
	if eps[1].ID != "12" || eps[1].Title != "Episode 12" {
		t.Errorf("episode shape: %+v", eps[1])
	}
}

// Highest resolution first, best-seeded within a resolution: the default pick
// is the one most likely to start.
func TestNyaaGetServersOrdering(t *testing.T) {
	nyaaStub(t)
	servers, err := NewNyaa().GetServers(nyaaS3ID, "12")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"1080p SubsPlease (1.4 GiB, 153 seeds)",
		"1080p VARYG (1.4 GiB, 77 seeds)",
		"1080p ASW (495.8 MiB, 26 seeds)",
		"1080p Anime Time (503.7 MiB, 10 seeds)",
		"720p SubsPlease (711.8 MiB, 20 seeds)",
		"480p SubsPlease (369.7 MiB, 10 seeds)",
	}
	if len(servers) != len(want) {
		t.Fatalf("got %d servers: %+v", len(servers), servers)
	}
	for i, s := range servers {
		if s.Name != want[i] || s.ID != string(rune('0'+i)) {
			t.Errorf("server %d = %+v, want %q", i, s, want[i])
		}
	}
	if _, err := NewNyaa().GetServers(nyaaS3ID, "99"); err == nil {
		t.Error("episode 99 is not in the feed; GetServers must fail, not return nothing")
	}
	if _, err := NewNyaa().GetServers(nyaaS3ID, ""); err == nil {
		t.Error("empty episode ID accepted")
	}
}

func TestNyaaWatchPicksQualityThenFallsBack(t *testing.T) {
	nyaaStub(t)
	n := NewNyaa()
	for _, c := range []struct{ quality, hash string }{
		{"720", "e61f34e11efe64a43535c64c959fd40f6d5b8180"},
		{"480p", "cd0b32db8f73a40a89ef64d4e47ac5f483e04595"},
		{"1080", "fc13cf7e7b8483de7cd1dbe63fef16afa49e6322"},
		{"best", "fc13cf7e7b8483de7cd1dbe63fef16afa49e6322"},
		{"", "fc13cf7e7b8483de7cd1dbe63fef16afa49e6322"},
		// 2160p is not stocked: fall back to the highest rather than fail.
		{"2160", "fc13cf7e7b8483de7cd1dbe63fef16afa49e6322"},
	} {
		st, err := n.Watch(nyaaS3ID, "12", "", c.quality)
		if err != nil {
			t.Fatalf("quality %q: %v", c.quality, err)
		}
		if !strings.HasPrefix(st.URL, "magnet:?xt=urn:btih:"+c.hash+"&") {
			t.Errorf("quality %q picked %s", c.quality, st.URL)
		}
	}
	st, _ := n.Watch(nyaaS3ID, "12", "", "720")
	if st.Quality != "720p" {
		t.Errorf("Quality = %q, want 720p", st.Quality)
	}
}

// With no release at the requested height, the closest one below it is
// chosen before anything above it; nothing below falls back to the best.
func TestNyaaPickTorrentClosestLower(t *testing.T) {
	rels := []nyaaRelease{{Height: 2160, Hash: "uhd"}, {Height: 720, Hash: "hd"}, {Height: 480, Hash: "sd"}}
	if got := nyaaPickTorrent(rels, "", "1080"); got.Hash != "hd" {
		t.Errorf("1080 picked %s, want hd (720p, closest lower)", got.Hash)
	}
	if got := nyaaPickTorrent(rels, "", "600p"); got.Hash != "sd" {
		t.Errorf("600p picked %s, want sd", got.Hash)
	}
	if got := nyaaPickTorrent(rels, "", "360"); got.Hash != "uhd" {
		t.Errorf("360 picked %s, want uhd (nothing below, best)", got.Hash)
	}
	if got := nyaaPickTorrent(rels, "1", "2160"); got.Hash != "hd" {
		t.Errorf("explicit index picked %s, want hd", got.Hash)
	}
}

func TestNyaaWatchExplicitServerWins(t *testing.T) {
	nyaaStub(t)
	n := NewNyaa()
	// Asking for 1080 as well proves the explicit choice beats the quality
	// preference rather than merging with it.
	st, err := n.Watch(nyaaS3ID, "12", "480p SubsPlease (369.7 MiB, 10 seeds)", "1080")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.URL, "magnet:?xt=urn:btih:cd0b32db8f73a40a89ef64d4e47ac5f483e04595&") {
		t.Errorf("explicit server ignored: %s", st.URL)
	}
	// A server index, as GetServers hands them out, is accepted too.
	st, err = n.Watch(nyaaS3ID, "12", "2", "best")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.URL, "magnet:?xt=urn:btih:44c130b70426081682c1e5f841153b080bc16fc3&") {
		t.Errorf("server index ignored: %s", st.URL)
	}
}

func TestNyaaMagnetShape(t *testing.T) {
	m := nyaaMagnet("fc13cf7e7b8483de7cd1dbe63fef16afa49e6322", "[SubsPlease] One-Punch Man S3 - 12 (1080p) [B7B8D40B].mkv")
	if !strings.HasPrefix(m, "magnet:?xt=urn:btih:fc13cf7e7b8483de7cd1dbe63fef16afa49e6322&dn=%5BSubsPlease%5D+One-Punch+Man") {
		t.Errorf("bad magnet prefix: %s", m)
	}
	for _, tr := range nyaaTrackers {
		if !strings.Contains(m, "&tr="+strings.NewReplacer(":", "%3A", "/", "%2F").Replace(tr)) {
			t.Errorf("magnet lacks tracker %s: %s", tr, m)
		}
	}
	if strings.Count(m, "&tr=") != len(nyaaTrackers) {
		t.Errorf("magnet has %d trackers, want %d", strings.Count(m, "&tr="), len(nyaaTrackers))
	}
}

// One play is Search, GetEpisodes, GetServers, Watch: the feed for a series
// must be fetched once, not once per step.
func TestNyaaCachesFeedAcrossCalls(t *testing.T) {
	_, calls := nyaaStub(t)
	n := NewNyaa()
	if _, err := n.GetEpisodes(nyaaS3ID, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := n.GetServers(nyaaS3ID, "12"); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Watch(nyaaS3ID, "12", "", "best"); err != nil {
		t.Fatal(err)
	}
	if _, err := n.GetDetails(nyaaS3ID); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("made %d requests for one series, want 1", got)
	}
	// A different provider instance has its own cache; an expired entry is
	// refetched so a new upload is not hidden for the life of the process.
	old := nyaaCacheTTL
	nyaaCacheTTL = 0
	defer func() { nyaaCacheTTL = old }()
	time.Sleep(time.Millisecond)
	if _, err := n.GetEpisodes(nyaaS3ID, "1"); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expired cache entry was not refetched: %d requests", got)
	}
}

func TestNyaaRecentAndTrendingUseCategoryFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Has("q") {
			t.Errorf("browse must not send a query: %s", r.URL)
		}
		if r.URL.Path == "/" && q.Get("s") == "seeders" && q.Get("o") != "desc" {
			t.Errorf("seeder sort without desc order: %s", r.URL)
		}
		_, _ = w.Write([]byte(nyaaFixture))
	}))
	defer srv.Close()
	old := nyaaBase
	nyaaBase = srv.URL
	defer func() { nyaaBase = old }()

	n := NewNyaa()
	recent, err := n.Recent(media.TV)
	if err != nil || len(recent) != 2 {
		t.Fatalf("Recent = %+v, %v", recent, err)
	}
	trending, err := n.Trending(media.TV)
	if err != nil || len(trending) != 2 {
		t.Fatalf("Trending = %+v, %v", trending, err)
	}
	if _, err := n.GetEmbedURL("0"); err == nil {
		t.Error("GetEmbedURL must refuse; a magnet is not an embed")
	}
}

func TestNyaaRejectsBadIDsAndUpstreamErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	old := nyaaBase
	nyaaBase = srv.URL
	defer func() { nyaaBase = old }()

	n := NewNyaa()
	if _, err := n.Search("x"); err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("upstream 429 not surfaced: %v", err)
	}
	if _, err := n.GetEpisodes("yts/123", "1"); err == nil {
		t.Error("foreign ID accepted")
	}
	if _, err := n.Watch("nyaa/One+Punch+Man", "abc", "", ""); err == nil {
		t.Error("non-numeric episode ID accepted")
	}
}

var _ StreamProvider = (*Nyaa)(nil)

func nyaaFeedItem(title, hash string) string {
	return "<item><title>" + title + "</title><link>https://nyaa.si/download/1.torrent</link>" +
		"<nyaa:seeders>5</nyaa:seeders><nyaa:leechers>0</nyaa:leechers><nyaa:infoHash>" + hash +
		"</nyaa:infoHash><nyaa:category>Anime - English-translated</nyaa:category><nyaa:size>1 GiB</nyaa:size></item>"
}

func TestNyaaUndatedIDWithSeveralDatedAdaptationsIsAmbiguous(t *testing.T) {
	feed := func(items ...string) string {
		return `<rss xmlns:nyaa="https://nyaa.si/xmlns/nyaa" version="2.0"><channel>` + strings.Join(items, "") + `</channel></rss>`
	}
	both := feed(
		nyaaFeedItem("[A] Ranma (1989) - 01 [1080p]", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		nyaaFeedItem("[B] Ranma (2024) - 01 [1080p]", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
	)
	nyaaStubWith(t, both)
	_, err := NewNyaa().GetEpisodes("nyaa/Ranma", "1")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v, want an ambiguity error", err)
	}

	nyaaStubWith(t, feed(nyaaFeedItem("[A] Ranma (2024) - 01 [1080p]", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")))
	if _, err := NewNyaa().GetEpisodes("nyaa/Ranma", "1"); err != nil {
		t.Fatalf("a single dated adaptation must still resolve: %v", err)
	}
}

func TestNyaaBrowseRefusesMovies(t *testing.T) {
	_, calls := nyaaStub(t)
	n := NewNyaa()
	if got, err := n.Trending(media.Movie); err == nil || len(got) != 0 {
		t.Fatalf("Trending(Movie) = %+v, %v; want no results", got, err)
	}
	if got, err := n.Recent(media.Movie); err == nil || len(got) != 0 {
		t.Fatalf("Recent(Movie) = %+v, %v; want no results", got, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("a movie browse fetched the feed %d time(s)", calls.Load())
	}
}

func TestNyaaFeedOverTheSizeLimitIsReportedAsSuch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<rss><channel>"))
		chunk := []byte(strings.Repeat("<!-- padding -->", 1024))
		for sent := 0; sent <= nyaaMaxFeedBytes; sent += len(chunk) {
			_, _ = w.Write(chunk)
		}
	}))
	defer srv.Close()
	old := nyaaBase
	nyaaBase = srv.URL
	defer func() { nyaaBase = old }()

	_, err := NewNyaa().Search("anything")
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("err = %v, want a size-limit error", err)
	}
}

func nyaaFeedURLFor(q string) string {
	return nyaaBase + "/?page=rss&c=" + nyaaCategory + "&q=" + q
}

func TestNyaaCacheStaysWithinItsBound(t *testing.T) {
	nyaaStub(t)
	n := NewNyaa()
	for i := 0; i < nyaaCacheMax+20; i++ {
		if _, err := n.fetch(nyaaFeedURLFor(strconv.Itoa(i))); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(n.cache); got > nyaaCacheMax {
		t.Errorf("cache holds %d feeds, want at most %d", got, nyaaCacheMax)
	}
}

func TestNyaaCacheEvictsExpiredFeedsOnInsert(t *testing.T) {
	nyaaStub(t)
	n := NewNyaa()
	old := nyaaCacheTTL
	nyaaCacheTTL = time.Millisecond
	defer func() { nyaaCacheTTL = old }()
	for i := 0; i < 3; i++ {
		if _, err := n.fetch(nyaaFeedURLFor(strconv.Itoa(i))); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := n.fetch(nyaaFeedURLFor("last")); err != nil {
		t.Fatal(err)
	}
	if got := len(n.cache); got != 1 {
		t.Errorf("cache holds %d feeds, want only the fresh one", got)
	}
}
