package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
)

type radarrMovie struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	ImdbID    string `json:"imdbId"`
	HasFile   bool   `json:"hasFile"`
	MovieFile *struct {
		Path      string `json:"path"`
		MediaInfo *struct {
			AudioLanguages string `json:"audioLanguages"`
		} `json:"mediaInfo"`
	} `json:"movieFile"`
}

// filmsFrom is every .mkv film Radarr holds and which of the languages it lacks.
func filmsFrom(movies []radarrMovie, langs *languages) []dubFilm {
	out := []dubFilm{}
	for _, movie := range movies {
		if !movie.HasFile || movie.ImdbID == "" || movie.MovieFile == nil || !strings.HasSuffix(movie.MovieFile.Path, ".mkv") {
			continue
		}
		audio := ""
		if movie.MovieFile.MediaInfo != nil {
			audio = movie.MovieFile.MediaInfo.AudioLanguages
		}
		var seen []string
		for _, word := range strings.Split(audio, "/") {
			if word = strings.TrimSpace(word); word != "" && !slices.Contains(seen, word) {
				seen = append(seen, word)
			}
		}
		audio = strings.Join(seen, "/")
		film := dubFilm{ID: movie.ID, Title: movie.Title, Year: movie.Year, IMDb: movie.ImdbID,
			Path: movie.MovieFile.Path, Audio: audio, Missing: []string{}}
		for _, code := range langs.order {
			if !langs.byCode[code].has(audio) {
				film.Missing = append(film.Missing, code)
			}
		}
		out = append(out, film)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out
}

// getJSON is a GET with an API key header, decoding the answer into out.
func getJSON(ctx context.Context, client *http.Client, url, apiKey string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("X-Api-Key", apiKey)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 200))
		return fmt.Errorf("%s: %s %s", redactURL(url), response.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("%s: %w", redactURL(url), err)
	}
	return nil
}

// redactURL drops the query, which may carry a key, from a URL quoted in an error.
func redactURL(url string) string {
	if i := strings.IndexByte(url, '?'); i >= 0 {
		return url[:i]
	}
	return url
}

func (d *dubService) radarrMovies(ctx context.Context) ([]radarrMovie, error) {
	var movies []radarrMovie
	err := getJSON(ctx, d.http, d.cfg.radarrURL+"/api/v3/movie", d.cfg.radarrKey, &movies)
	return movies, err
}

func (d *dubService) radarrMovie(ctx context.Context, id int64) (radarrMovie, error) {
	var movie radarrMovie
	err := getJSON(ctx, d.http, fmt.Sprintf("%s/api/v3/movie/%d", d.cfg.radarrURL, id), d.cfg.radarrKey, &movie)
	return movie, err
}
