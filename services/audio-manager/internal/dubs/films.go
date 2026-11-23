package dubs

import (
	"os"
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

// readTracks replaces what Radarr says about each film's languages with what its file holds, reading
// only the files that changed since the last listing. A file ffprobe cannot read keeps Radarr's word.
func (d *Service) readTracks(films, previous []dubFilm) {
	known := map[string]*fileTracks{}
	for _, f := range previous {
		if f.File != nil {
			known[f.Path] = f.File
		}
	}
	for i := range films {
		film := &films[i]
		stat, err := os.Stat(d.hostPath(film.Path))
		if err != nil {
			continue
		}
		file := known[film.Path]
		if file == nil || file.Size != stat.Size() || file.ModTime != stat.ModTime().Unix() {
			info := d.probeFile(d.hostPath(film.Path))
			if info == nil {
				continue
			}
			file = &fileTracks{Size: stat.Size(), ModTime: stat.ModTime().Unix(), Length: info.length(), Audio: info.audio()}
		}
		film.File = file
		film.Missing = film.Missing[:0]
		for _, code := range d.langs.order {
			if pickDub(&probeInfo{Streams: file.Audio}, d.langs.byCode[code]) == nil {
				film.Missing = append(film.Missing, code)
			}
		}
	}
}
