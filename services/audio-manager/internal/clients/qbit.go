package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// Seeding and deletion stay qbit-manage's: the category audio-source is never hardlinked, so it is
// tagged noHL and its tracker's share-limit group applies. The keep tag holds a torrent out of those
// groups only while this needs its file, which matters for a public one: that group deletes after 2 h.

const (
	qbitCategory = "audio-source"
	qbitKeepTag  = "keep"
	qbitSavePath = "/data/downloads"
)

var errQbitNotFound = errors.New("qBittorrent does not know it")

type Qbit struct {
	base   string
	client *http.Client
}

type QbitTorrent struct {
	Downloaded   int64   `json:"downloaded"`
	Progress     float64 `json:"progress"`
	SavePath     string  `json:"save_path"`
	DownloadPath string  `json:"download_path"`
}

type QbitFile struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	PieceRange []int  `json:"piece_range"`
}

func LoginQbit(ctx context.Context, base, user, password string, timeout time.Duration) (*Qbit, error) {
	jar, _ := cookiejar.New(nil)
	q := &Qbit{base: strings.TrimRight(base, "/") + "/api/v2", client: &http.Client{Jar: jar, Timeout: timeout}}
	form := url.Values{"username": {user}, "password": {password}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, q.base+"/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Referer", q.base)
	response, err := q.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("qBittorrent login: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	// 5.2 answers 204 with no body, older versions 200 "Ok."; a refusal is 200 "Fails.".
	if response.StatusCode != http.StatusNoContent && strings.TrimSpace(string(body)) != "Ok." {
		return nil, errors.New("qBittorrent refused the login")
	}
	return q, nil
}

// get decodes an answer, or errQbitNotFound for a torrent qBittorrent does not know (yet: an add
// takes a moment to register).
func (q *Qbit) get(ctx context.Context, path string, params url.Values, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, q.base+"/"+path+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	response, err := q.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return errQbitNotFound
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("qBittorrent %s: %s", path, response.Status)
	}
	raw, err := io.ReadAll(response.Body)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (q *Qbit) Post(ctx context.Context, path string, fields url.Values) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, q.base+"/"+path, strings.NewReader(fields.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := q.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)
	if response.StatusCode == http.StatusNotFound {
		return errQbitNotFound
	}
	if response.StatusCode >= 300 {
		return fmt.Errorf("qBittorrent %s: %s", path, response.Status)
	}
	return nil
}

// Torrent is nil for a torrent qBittorrent does not have.
func (q *Qbit) Torrent(ctx context.Context, hash string) (*QbitTorrent, error) {
	var found []QbitTorrent
	if err := q.get(ctx, "torrents/info", url.Values{"hashes": {hash}}, &found); err != nil && !errors.Is(err, errQbitNotFound) {
		return nil, err
	}
	if len(found) == 0 {
		return nil, nil
	}
	return &found[0], nil
}

func (q *Qbit) Files(ctx context.Context, hash string) ([]QbitFile, error) {
	var files []QbitFile
	err := q.get(ctx, "torrents/files", url.Values{"hash": {hash}}, &files)
	if errors.Is(err, errQbitNotFound) {
		return nil, nil
	}
	return files, err
}

func (q *Qbit) PieceStates(ctx context.Context, hash string) ([]int, error) {
	var states []int
	err := q.get(ctx, "torrents/pieceStates", url.Values{"hash": {hash}}, &states)
	if errors.Is(err, errQbitNotFound) {
		return nil, nil
	}
	return states, err
}

func (q *Qbit) PieceSize(ctx context.Context, hash string) (int64, error) {
	var properties struct {
		PieceSize int64 `json:"piece_size"`
	}
	err := q.get(ctx, "torrents/properties", url.Values{"hash": {hash}}, &properties)
	return properties.PieceSize, err
}

func (q *Qbit) EnsureCategory(ctx context.Context) error {
	categories := map[string]any{}
	if err := q.get(ctx, "torrents/categories", nil, &categories); err != nil && !errors.Is(err, errQbitNotFound) {
		return err
	}
	if _, ok := categories[qbitCategory]; ok {
		return nil
	}
	return q.Post(ctx, "torrents/createCategory", url.Values{"category": {qbitCategory}, "savePath": {qbitSavePath}})
}

func (q *Qbit) AddStopped(ctx context.Context, torrent []byte, name string) error {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	// stopped is 5.x's name, paused 4.x's; each ignores the other.
	for _, field := range [][2]string{{"category", qbitCategory}, {"tags", qbitCategory + "," + qbitKeepTag},
		{"savepath", qbitSavePath}, {"autoTMM", "false"}, {"stopped", "true"}, {"paused", "true"}} {
		form.WriteField(field[0], field[1])
	}
	part, err := form.CreateFormFile("torrents", name+".torrent")
	if err != nil {
		return err
	}
	part.Write(torrent)
	form.Close()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, q.base+"/torrents/add", &body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	response, err := q.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	answer, _ := io.ReadAll(response.Body)
	// Older versions answer "Ok.", 5.2 a JSON summary; only "Fails." is a refusal.
	if response.StatusCode >= 300 || strings.TrimSpace(string(answer)) == "Fails." {
		return errors.New("qBittorrent did not take the torrent")
	}
	return nil
}

func (q *Qbit) Start(ctx context.Context, hash string) error {
	return q.either(ctx, "torrents/start", "torrents/resume", hash)
}

func (q *Qbit) Stop(ctx context.Context, hash string) error {
	return q.either(ctx, "torrents/stop", "torrents/pause", hash)
}

// either tries 5.x's name first: it renamed resume and pause to start and stop.
func (q *Qbit) either(ctx context.Context, current, older, hash string) error {
	err := q.Post(ctx, current, url.Values{"hashes": {hash}})
	if errors.Is(err, errQbitNotFound) {
		return q.Post(ctx, older, url.Values{"hashes": {hash}})
	}
	return err
}

func (q *Qbit) Delete(ctx context.Context, hash string) error {
	return q.Post(ctx, "torrents/delete", url.Values{"hashes": {hash}, "deleteFiles": {"true"}})
}

// Release hands the torrent to qbit-manage's share limits.
func (q *Qbit) Release(ctx context.Context, hash string) error {
	return q.Post(ctx, "torrents/removeTags", url.Values{"hashes": {hash}, "tags": {qbitKeepTag}})
}
