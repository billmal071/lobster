package provider

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestNewKimCartoonNormalizesDeadAndURLBases(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "empty", base: "", want: kimCartoonDefaultBase},
		{name: "old host", base: "kimcartoon.li", want: kimCartoonDefaultBase},
		{name: "old www host", base: "www.kimcartoon.li", want: kimCartoonDefaultBase},
		{name: "current url", base: "https://kimcartoon.com.co/", want: kimCartoonDefaultBase},
		{name: "current www url", base: "https://www.kimcartoon.com.co/", want: kimCartoonDefaultBase},
		{name: "custom host", base: "mirror.example", want: "mirror.example"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewKimCartoon(tt.base).base
			if got != tt.want {
				t.Fatalf("base = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseKCSearchResultsCurrentSearchMarkup(t *testing.T) {
	doc := kimCartoonDoc(t, `
		<div class="postbody">
			<div class="bixbox">
				<div class="releases"><h1><span>Search 'rick and morty'</span></h1></div>
				<div class="listupd">
					<article class="bs styletwo">
						<div class="bsx">
							<a href="https://kimcartoon.com.co/cartoon/rick-and-morty-season-9-2026/" itemprop="url" title="Rick and Morty Season 9 (2026)" class="tip" rel="25081">
								<div class="ttzz">Rick and Morty Season 9 (2026)</div>
							</a>
						</div>
					</article>
					<article class="bs styletwo">
						<div class="bsx">
							<a href="https://kimcartoon.com.co/cartoon/rick-and-morty-season-8-2025/" itemprop="url" title="Rick and Morty Season 8 (2025)" class="tip" rel="23832">
								<div class="ttzz">Rick and Morty Season 8 (2025)</div>
							</a>
						</div>
					</article>
				</div>
			</div>
		</div>
		<div id="sidebar">
			<div class="serieslist pop">
				<a class="series" href="https://kimcartoon.com.co/cartoon/unrelated/">Unrelated Trending</a>
			</div>
		</div>
	`)

	results := parseKCSearchResults(doc)
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2: %+v", len(results), results)
	}
	if results[0].ID != "cartoon/rick-and-morty-season-9-2026" {
		t.Fatalf("first ID = %q", results[0].ID)
	}
	if results[0].Title != "Rick and Morty Season 9 (2026)" {
		t.Fatalf("first title = %q", results[0].Title)
	}
	if results[0].Year != "2026" {
		t.Fatalf("first year = %q", results[0].Year)
	}
}

func TestParseKCSearchResultsCurrentTrendingMarkup(t *testing.T) {
	doc := kimCartoonDoc(t, `
		<div id="wpop-items">
			<div class="serieslist pop wpop wpop-weekly">
				<ul>
					<li>
						<div class="imgseries">
							<a class="series" href="https://kimcartoon.com.co/cartoon/regular-show-the-lost-tapes-2026/" rel="25037">
								<img alt="Regular Show: The Lost Tapes (2026)">
							</a>
						</div>
						<div class="leftseries">
							<h4>
								<a class="series" href="https://kimcartoon.com.co/cartoon/regular-show-the-lost-tapes-2026/" rel="25037">Regular Show: The Lost Tapes (2026)</a>
							</h4>
						</div>
					</li>
				</ul>
			</div>
		</div>
	`)

	results := parseKCSearchResults(doc)
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1: %+v", len(results), results)
	}
	if results[0].ID != "cartoon/regular-show-the-lost-tapes-2026" {
		t.Fatalf("ID = %q", results[0].ID)
	}
	if results[0].Title != "Regular Show: The Lost Tapes (2026)" {
		t.Fatalf("title = %q", results[0].Title)
	}
}

func TestParseKCSearchResultsOldMarkupStillWorks(t *testing.T) {
	doc := kimCartoonDoc(t, `
		<article class="bs styletwo">
			<div class="bsx">
				<a href="https://kimcartoon.com.co/cartoon/south-park-season-28-2025/" itemprop="url" title="South Park Season 28 (2025)">
					<div class="ttzz">South Park Season 28 (2025)</div>
				</a>
			</div>
		</article>
	`)

	results := parseKCSearchResults(doc)
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].ID != "cartoon/south-park-season-28-2025" {
		t.Fatalf("ID = %q", results[0].ID)
	}
}

func kimCartoonDoc(t *testing.T, html string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parsing HTML: %v", err)
	}
	return doc
}
