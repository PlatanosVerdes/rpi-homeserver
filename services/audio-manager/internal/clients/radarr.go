package clients

import (
	"context"
	"fmt"
	"net/http"
)

type Radarr struct {
	Base, Key string
	HTTP      *http.Client
}

type Movie struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	Year      int        `json:"year"`
	ImdbID    string     `json:"imdbId"`
	HasFile   bool       `json:"hasFile"`
	MovieFile *MovieFile `json:"movieFile"`
}

type MovieFile struct {
	Path      string     `json:"path"`
	MediaInfo *MediaInfo `json:"mediaInfo"`
}

type MediaInfo struct {
	AudioLanguages string `json:"audioLanguages"`
}

func (r Radarr) Movies(ctx context.Context) ([]Movie, error) {
	var movies []Movie
	err := getJSON(ctx, r.HTTP, r.Base+"/api/v3/movie", r.Key, &movies)
	return movies, err
}

func (r Radarr) Movie(ctx context.Context, id int64) (Movie, error) {
	var movie Movie
	err := getJSON(ctx, r.HTTP, fmt.Sprintf("%s/api/v3/movie/%d", r.Base, id), r.Key, &movie)
	return movie, err
}
