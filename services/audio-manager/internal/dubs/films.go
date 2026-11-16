package dubs

import (
	"slices"
	"sort"
	"strings"

	"audio-manager/internal/clients"
)

// filmsFrom is every .mkv film Radarr holds and which of the languages it lacks.
func filmsFrom(movies []clients.Movie, langs *languages) []dubFilm {
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
