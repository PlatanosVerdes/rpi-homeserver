package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchUNIT3D(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user" || r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"Unauthenticated."}`)
			return
		}
		fmt.Fprint(w, `{"username":"someone","group":"Neófito","uploaded":"50 GiB","downloaded":"1.5 GiB",`+
			`"ratio":"33.33","buffer":"124 GiB","seeding":0,"leeching":0,"seedbonus":"1,234.50","hit_and_runs":2}`)
	}))
	defer site.Close()
	config := map[string]any{"site": site.URL + "/", "software": "unit3d"}

	t.Setenv("SOMESITE_APIKEY", "secret")
	got, err := fetchAccount("somesite", config)
	if err != nil {
		t.Fatal(err)
	}
	if got.uploaded != 50*(1<<30) || got.downloaded != 1.5*(1<<30) {
		t.Errorf("bytes: got %v up, %v down", got.uploaded, got.downloaded)
	}
	if got.ratio != 33.33 || got.points != 1234.5 || got.class != "Neófito" {
		t.Errorf("got ratio %v, points %v, class %q", got.ratio, got.points, got.class)
	}
	if !got.hasHitAndRuns || got.hitAndRuns != 2 {
		t.Errorf("hit and runs: got %v, %v", got.hitAndRuns, got.hasHitAndRuns)
	}

	t.Setenv("SOMESITE_APIKEY", "wrong")
	if _, err := fetchAccount("somesite", config); err == nil {
		t.Error("a refused key read as an account")
	}

	t.Setenv("SOMESITE_APIKEY", "")
	if _, err := fetchAccount("somesite", config); err == nil {
		t.Error("a missing key read as an account")
	}
}

func TestToBytesUnits(t *testing.T) {
	cases := map[string]float64{"50 GiB": 50 << 30, "50 GB": 50 << 30, "3 Go": 3 << 30, "2 MiB": 2 << 20, "7 B": 7}
	for text, want := range cases {
		if got, ok := toBytes(text); !ok || got != want {
			t.Errorf("%q: got %v, %v", text, got, ok)
		}
	}
	if _, ok := toBytes("12 ratio"); ok {
		t.Error("a non-size read as bytes")
	}
}

func TestFetchUNIT3DWithoutHitAndRuns(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"uploaded":"50 GiB","downloaded":"1 GiB","ratio":"50"}`)
	}))
	defer site.Close()
	t.Setenv("SOMESITE_APIKEY", "secret")
	got, err := fetchAccount("somesite", map[string]any{"site": site.URL, "software": "unit3d"})
	if err != nil {
		t.Fatal(err)
	}
	if got.hasHitAndRuns {
		t.Error("a site that does not report hit and runs read as having zero or more")
	}
}
