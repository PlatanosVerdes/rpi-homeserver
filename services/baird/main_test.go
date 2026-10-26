package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestScrapeKeepsPageOrderAndDropsRepeats(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("B", 40)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<a href="acestream://` + b + `">1</a><a href="acestream://` + a + `">2</a>
			<a href="acestream://` + b + `">again</a><a href="acestream://short">no</a>`))
	}))
	defer srv.Close()
	ids, err := scrape(srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != strings.ToLower(b) || ids[1] != a {
		t.Fatalf("got %v", ids)
	}
}

func TestScrapeWithoutLinksIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>nothing here</html>"))
	}))
	defer srv.Close()
	if _, err := scrape(srv.Client(), srv.URL); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRenderWritesPagesThenPickedChannels(t *testing.T) {
	s := openStore(t.TempDir())
	s.choices.Pages = []page{{URL: "https://x", Name: "Sky Sports F1", Group: "FORMULA 1", IDs: []string{"id1", "id2"}}}
	s.catalogue = []entry{
		{Name: `News, "EN"`, Categories: []string{"informational"}, Items: []item{{Infohash: "h1"}, {Infohash: "h2"}}},
		{Name: "Not picked", Items: []item{{Infohash: "h3"}}},
	}
	s.setPicked([]string{`News, "EN"`}, true)
	s.ids = map[string]string{"h1": "c1", "h3": "c3"}
	if err := s.render(); err != nil {
		t.Fatal(err)
	}
	want := "#EXTM3U\n" +
		"#EXTINF:-1 group-title=\"FORMULA 1\",Sky Sports F1\nacestream://id1\n" +
		"#EXTINF:-1 group-title=\"FORMULA 1\",Sky Sports F1\nacestream://id2\n" +
		"#EXTINF:-1 group-title=\"INFORMATIONAL\",News  'EN'\nacestream://c1\n"
	if s.m3u != want {
		t.Fatalf("got\n%s\nwant\n%s", s.m3u, want)
	}
	if s.channels != 2 || s.sources != 3 {
		t.Fatalf("channels %d sources %d", s.channels, s.sources)
	}
	if b, _ := os.ReadFile(s.path("baird.m3u")); string(b) != want {
		t.Fatal("file differs from what is served")
	}
	if got := s.missingIDs(); len(got) != 1 || got[0] != "h2" {
		t.Fatalf("missing %v", got)
	}
}

func TestChoicesSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	s := openStore(dir)
	s.setPicked([]string{"A", "B"}, true)
	s.setPicked([]string{"A"}, false)
	if err := s.addPage(page{URL: "https://x", Name: "X"}); err != nil {
		t.Fatal(err)
	}
	again := openStore(dir)
	if len(again.choices.Picked) != 1 || again.choices.Picked[0] != "B" || len(again.choices.Pages) != 1 {
		t.Fatalf("got %+v", again.choices)
	}
}

func TestACategoryTakesNewChannelsAndKeepsExclusions(t *testing.T) {
	s := openStore(t.TempDir())
	live := []item{{Availability: 1}}
	s.catalogue = []entry{
		{Name: "A", Categories: []string{"sport"}, Items: live},
		{Name: "B", Categories: []string{"sport", "tv"}, Items: live},
		{Name: "C", Categories: []string{"movies"}, Items: live},
	}
	s.setCategories([]string{"sport"}, true)
	s.setPicked([]string{"B"}, false)
	s.setPicked([]string{"C"}, true)
	s.catalogue = append(s.catalogue, entry{Name: "D", Categories: []string{"sport"}, Items: live})

	var got []string
	for _, e := range s.pickedEntries() {
		got = append(got, e.Name)
	}
	if strings.Join(got, ",") != "A,C,D" {
		t.Fatalf("got %v", got)
	}
	s.setPicked([]string{"B"}, true)
	if !s.included(s.catalogue[1]) || len(s.choices.Excluded) != 0 {
		t.Fatalf("ticking B again should drop its exclusion: %+v", s.choices)
	}
}

func TestCatalogueTakesNumericNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"total":2,"results":[{"name":24,"items":[{"infohash":"h1","categories":["tv"]}]},{"name":"News","items":[{"infohash":"h2","categories":["TV","tv"," "],"countries":["ES"]}]}]}}`))
	}))
	defer srv.Close()
	c, err := (&engine{base: srv.URL, client: srv.Client()}).catalogue()
	if err != nil || len(c) != 2 || c[0].Name != "24" || c[0].Categories[0] != "tv" || len(c[1].Categories) != 1 || c[1].Countries[0] != "es" {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestCountriesNarrowTheCategories(t *testing.T) {
	s := openStore(t.TempDir())
	live := []item{{Availability: 1}}
	s.catalogue = []entry{
		{Name: "ES", Categories: []string{"sport"}, Countries: []string{"es"}, Items: live},
		{Name: "GB", Categories: []string{"sport"}, Countries: []string{"gb"}, Items: live},
		{Name: "None", Categories: []string{"sport"}, Items: live},
		{Name: "Movie ES", Categories: []string{"movies"}, Countries: []string{"es"}, Items: live},
	}
	s.setCategories([]string{"sport"}, true)
	s.setCountries([]string{"es", "-"}, true)
	var got []string
	for _, e := range s.pickedEntries() {
		got = append(got, e.Name)
	}
	if strings.Join(got, ",") != "ES,None" {
		t.Fatalf("got %v", got)
	}
}

func TestUpstreamPassesThroughWhatIsShown(t *testing.T) {
	m3u := `#EXTM3U url-tvg="https://guide"
#EXTINF:-1 tvg-id="D1" group-title="DAZN",DAZN 1 1080p *
acestream://a1
#EXTINF:-1 tvg-id="D1" group-title="DAZN",DAZN 1 1080p **
acestream://a2
#EXTINF:-1 group-title="KIDS",Cartoons
acestream://a3
`
	chans, header := parseList([]byte(m3u))
	s := openStore(t.TempDir())
	s.setUpstream(upstreamRead{channels: chans, header: header}, nil, nil, keeping{For: time.Hour, Max: 3})
	if len(s.listed) != 2 || len(s.listed[0].Sources) != 2 {
		t.Fatalf("got %+v", s.listed)
	}
	s.setListGroups([]string{"KIDS"}, false)
	s.render()
	want := "#EXTM3U url-tvg=\"https://guide\"\n" +
		"#EXTINF:-1 tvg-id=\"D1\" group-title=\"DAZN\",DAZN 1 1080p *\nacestream://a1\n" +
		"#EXTINF:-1 tvg-id=\"D1\" group-title=\"DAZN\",DAZN 1 1080p **\nacestream://a2\n"
	if s.m3u != want {
		t.Fatalf("got\n%s\nwant\n%s", s.m3u, want)
	}
	s.setListChannels([]string{"Cartoons"}, true)
	s.render()
	if !strings.Contains(s.m3u, "acestream://a3") {
		t.Fatal("a channel switched back on inside a hidden group is missing")
	}
}

func TestDroppedSourcesStayUntilTheyFailOrExpire(t *testing.T) {
	start, k := time.Now(), keeping{For: 72 * time.Hour, Max: 3}
	ch := func(ids ...string) listed {
		c := listed{Name: "C"}
		for _, id := range ids {
			c.Sources = append(c.Sources, upstream{ID: id})
		}
		return c
	}
	l := remember(nil, []listed{ch("a", "b", "c")}, start, k, nil)
	l = remember(l, []listed{ch("a")}, start.Add(time.Hour), k, map[string]string{"b": "failed"})
	if ids := idsOf(l); ids != "a,c" {
		t.Fatalf("after a failure: %s", ids)
	}
	l = remember(l, []listed{ch("a")}, start.Add(73*time.Hour), k, nil)
	if ids := idsOf(l); ids != "a" {
		t.Fatalf("after the window: %s", ids)
	}
}

func TestKeptSourcesAreCappedPlayedFirst(t *testing.T) {
	start, k := time.Now(), keeping{For: 72 * time.Hour, Max: 2}
	var l []listed
	seen := map[string]string{"old": "played"}
	for i, id := range []string{"old", "mid", "new", "hit"} {
		l = remember(l, []listed{{Name: "C", Sources: []upstream{{ID: "live"}, {ID: id}}}}, start.Add(time.Duration(i)*time.Minute), k, seen)
	}
	l = remember(l, []listed{{Name: "C", Sources: []upstream{{ID: "live"}}}}, start.Add(time.Hour), k, seen)
	if ids := idsOf(l); ids != "live,old,hit" {
		t.Fatalf("got %s, want the listed one, the one that played, then the newest", ids)
	}
}

func TestAChannelDroppedWholeStaysOnlyIfItPlayed(t *testing.T) {
	start, k := time.Now(), keeping{For: 72 * time.Hour, Max: 3}
	read := []listed{{Name: "Junk", Sources: []upstream{{ID: "j"}}}, {Name: "Good", Sources: []upstream{{ID: "g"}}}}
	l := remember(nil, read, start, k, nil)
	l = remember(l, nil, start.Add(time.Hour), k, map[string]string{"g": "played"})
	if len(l) != 1 || l[0].Name != "Good" {
		t.Fatalf("got %+v", l)
	}
}

func idsOf(l []listed) string {
	var ids []string
	for _, c := range l {
		for _, s := range c.Sources {
			ids = append(ids, s.ID)
		}
	}
	return strings.Join(ids, ",")
}

func TestAGatewayAnsweringNoChannelsCountsAsDown(t *testing.T) {
	good := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if good {
			w.Write([]byte("#EXTINF:-1 group-title=\"G\",One\nacestream://x\n"))
			return
		}
		w.Write([]byte("<html>gateway timeout, but 200</html>"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	file := dir + "/upstream.json"
	os.WriteFile(file, []byte(`[{"url":"`+srv.URL+`/a"}]`), 0o644)
	if r, err := readUpstream(srv.Client(), file, dir+"/cache"); err != nil || len(r.channels) != 1 {
		t.Fatalf("got %+v %v", r, err)
	}
	good = false
	if r, _ := readUpstream(srv.Client(), file, dir+"/cache"); len(r.channels) != 1 || r.channels[0].Name != "One" {
		t.Fatal("an empty answer replaced the last good copy")
	}
}

func TestGroupsThatAppearLaterStartHidden(t *testing.T) {
	s := openStore(t.TempDir())
	first := []listed{{Name: "A", Group: "DAZN", Sources: []upstream{{ID: "1"}}}}
	s.setUpstream(upstreamRead{channels: first}, nil, nil, keeping{For: time.Hour, Max: 3})
	if !s.shown(s.listed[0]) {
		t.Fatal("the first read must show everything")
	}
	later := append(first, listed{Name: "Spam", Group: "NEW STUFF", Sources: []upstream{{ID: "2"}}})
	s.setUpstream(upstreamRead{channels: later}, nil, nil, keeping{For: time.Hour, Max: 3})
	if s.shown(s.listed[1]) || len(s.choices.NewGroups) != 1 {
		t.Fatalf("a new group must start hidden: %+v", s.choices)
	}
	s.setListGroups([]string{"NEW STUFF"}, true)
	if !s.shown(s.listed[1]) || len(s.choices.NewGroups) != 0 {
		t.Fatal("switching it on shows it and clears the mark")
	}
}

func TestADeadChannelDoesNotComeInThroughItsCategory(t *testing.T) {
	s := openStore(t.TempDir())
	s.catalogue = []entry{
		{Name: "Live", Categories: []string{"sport"}, Items: []item{{Infohash: "h1", Availability: 1}}},
		{Name: "Dead", Categories: []string{"sport"}, Items: []item{{Infohash: "h2"}}},
	}
	s.setCategories([]string{"sport"}, true)
	if !s.included(s.catalogue[0]) || s.included(s.catalogue[1]) {
		t.Fatal("only the live one should come in")
	}
	s.setPicked([]string{"Dead"}, true)
	if !s.included(s.catalogue[1]) {
		t.Fatal("picked by hand comes in anyway")
	}
}

func TestAllCategoriesWithACountryTakesEverythingFromIt(t *testing.T) {
	s := openStore(t.TempDir())
	live := []item{{Availability: 1}}
	s.catalogue = []entry{
		{Name: "ES sport", Categories: []string{"sport"}, Countries: []string{"es"}, Items: live},
		{Name: "ES movies", Categories: []string{"movies"}, Countries: []string{"es"}, Items: live},
		{Name: "ES none", Countries: []string{"es"}, Items: live},
		{Name: "GB sport", Categories: []string{"sport"}, Countries: []string{"gb"}, Items: live},
	}
	s.setCategories([]string{allCategories}, true)
	s.setCountries([]string{"es"}, true)
	var got []string
	for _, e := range s.pickedEntries() {
		got = append(got, e.Name)
	}
	if strings.Join(got, ",") != "ES sport,ES movies,ES none" {
		t.Fatalf("got %v", got)
	}
}
