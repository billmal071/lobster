package tbcpl

import "testing"

const sampleJSON = `{"categories":[
 {"id":"movies","name":"Movies & Shows","sites":[
   {"name":"1Shows","url":"https://www.1shows.org/","enabled":true,"status":"trusted"},
   {"name":"MeowTV","url":"https://meowtv.ru/","enabled":true}]},
 {"id":"livetv","name":"Live TV & Sports","sites":[
   {"name":"FreeTV","url":"https://example.com/list.m3u","enabled":true,"status":"trusted"},
   {"name":"Disabled","url":"https://nope.example/","enabled":false,"status":"trusted"}]}]}`

func TestParseAndFilters(t *testing.T) {
	c, err := Parse([]byte(sampleJSON))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(c.Sites) != 4 {
		t.Fatalf("got %d sites, want 4", len(c.Sites))
	}
	movies := c.ByCategory("movies")
	if len(movies) != 2 || movies[0].Name != "1Shows" || movies[0].Category != "movies" {
		t.Fatalf("ByCategory movies wrong: %+v", movies)
	}
	trusted := c.Trusted()
	// 1Shows + FreeTV are trusted+enabled; Disabled is trusted but disabled.
	if len(trusted) != 2 {
		t.Fatalf("Trusted got %d, want 2: %+v", len(trusted), trusted)
	}
}
