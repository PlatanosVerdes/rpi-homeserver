package dubs

import (
	"audio-manager/internal/clients"
	"audio-manager/internal/script"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A probe downloads slowly and is watched until its pieces are in, then stopped: left running, a fast
// swarm took 300 MB in the ten seconds between two checks.
const probeRate = 2 * mb

// minVideoShare is how much of a torrent its .mkv must be to hold the film rather than a sample.
const minVideoShare = 0.5

// pass is one tick of the worker: it logs into qBittorrent when first needed and sends what happened
// to Telegram at the end.
type pass struct {
	d      *Service
	ctx    context.Context
	q      *clients.Qbit
	events []string
}

func (p *pass) qbit() (*clients.Qbit, error) {
	if p.q == nil {
		q, err := clients.LoginQbit(p.ctx, p.d.cfg.QbitURL, p.d.cfg.QbitUser, p.d.cfg.QbitPassword, dubHTTPTimeout)
		if err != nil {
			return nil, err
		}
		p.q = q
	}
	return p.q, nil
}

func (p *pass) say(job *jobState, line string) {
	line = job.Title + ": " + line
	log.Printf("audio-manager: %s", line)
	p.events = append(p.events, line)
}

// take turns one of the page's requests into a job, or ends one; the request is gone either way.
func (p *pass) take(request dubRequest) error {
	d := p.d
	key := dubKey(request.MovieID, request.Lang)
	d.mu.Lock()
	var job *jobState
	if existing, ok := d.state.Jobs[key]; ok {
		job = existing.clone()
	}
	film, listed := d.film(request.MovieID, request.Lang)
	d.mu.Unlock()

	switch {
	case request.Cancel:
		if job != nil && !job.finished() {
			if err := p.dropTorrent(job); err != nil {
				return err
			}
			job.set("cancelled", "cancelled from the page")
		} else {
			job = nil
		}
	case (job == nil || job.finished()) && listed:
		now := time.Now().Unix()
		job = &jobState{MovieID: film.ID, Lang: request.Lang, Title: fmt.Sprintf("%s (%d)", film.Title, film.Year),
			Name: film.Title, IMDb: film.IMDb, Requested: now, Tried: []string{}, Status: "queued", Updated: now}
	default:
		job = nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if job != nil {
		d.state.Jobs[key] = job
	}
	if i := slices.Index(d.state.Requests, request); i >= 0 {
		d.state.Requests = slices.Delete(d.state.Requests, i, i+1)
	}
	d.saveState()
	return nil
}

type jobProgress struct {
	status, stage string
	tried         int
}

func progressOf(job *jobState) jobProgress {
	p := jobProgress{status: job.Status, tried: len(job.Tried)}
	if job.Current != nil {
		p.stage = job.Current.Stage
	}
	return p
}

// advance takes one step at a time until the job waits on something outside this pass.
func (p *pass) advance(job *jobState) {
	key := dubKey(job.MovieID, job.Lang)
	for !job.finished() {
		before := progressOf(job)
		var charged int64
		var err error
		switch job.Status {
		case "queued":
			err = p.search(job)
		case "trying":
			err = p.tryNext(job)
		case "probing":
			charged, err = p.checkProbe(job)
		case "downloading":
			charged, err = p.checkDownload(job)
		}
		if err != nil {
			log.Printf("audio-manager: %s: %v", job.Title, err)
			job.Reason = "will try again: " + err.Error()
		}
		p.d.commit(key, job, charged)
		if err != nil || progressOf(job) == before {
			return
		}
	}
}

func (p *pass) search(job *jobState) error {
	ctx, cancel := context.WithTimeout(p.ctx, filmSearchTimeout)
	defer cancel()
	p.d.jobSearching.Store(true)
	search, err := p.d.searchFilm(ctx, searchJob{id: job.MovieID, imdb: job.IMDb, title: job.Name}, nil)
	p.d.jobSearching.Store(false)
	if err != nil {
		return err
	}
	p.d.searches.put(job.MovieID, search)
	found, err := p.d.rank(search, job.Lang, job.Tried)
	if err != nil {
		return err
	}
	if err := p.downloadedFirst(found); err != nil {
		return err
	}
	job.Candidates = found
	if len(found) == 0 {
		job.set("failed", "no release to try on any indexer")
		p.say(job, job.Reason)
		return nil
	}
	job.set("trying", fmt.Sprintf("%d releases to probe", len(found)))
	return nil
}

// downloadedFirst moves the releases a previous job already downloaded to the front, so a dub that
// was fetched but refused gets another try without fetching anything.
func (p *pass) downloadedFirst(found []candidate) error {
	if len(found) == 0 {
		return nil
	}
	q, err := p.qbit()
	if err != nil {
		return err
	}
	sources, err := q.Sources(p.ctx)
	if err != nil {
		return err
	}
	var complete []string
	for _, t := range sources {
		if t.Progress >= 1 {
			complete = append(complete, releaseKey(t.Name))
		}
	}
	// A site may add words to the torrent's own name in its listing ("... h264.mkv Spanish").
	downloaded := func(c candidate) bool {
		title := releaseKey(c.Title)
		return slices.ContainsFunc(complete, func(name string) bool { return name != "" && strings.HasPrefix(title, name) })
	}
	slices.SortStableFunc(found, func(a, b candidate) int {
		switch da, db := downloaded(a), downloaded(b); {
		case da == db:
			return 0
		case da:
			return -1
		}
		return 1
	})
	return nil
}

func releaseKey(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".mkv")
}

// largestMKV is the index of the biggest .mkv, or -1 when it is not most of the torrent: a release
// packed in RAR files shows only its sample as .mkv.
func largestMKV(files []clients.QbitFile) int {
	video := -1
	var total int64
	for i, f := range files {
		total += f.Size
		if strings.HasSuffix(strings.ToLower(f.Name), ".mkv") && (video < 0 || f.Size > files[video].Size) {
			video = i
		}
	}
	if video < 0 || float64(files[video].Size) < minVideoShare*float64(total) {
		return -1
	}
	return video
}

// reuse hands a source an earlier job downloaded to add-audio again, if it is complete and has the dub.
func (p *pass) reuse(job *jobState, next candidate, q *clients.Qbit, existing *clients.QbitTorrent) error {
	if !existing.IsSource() || existing.Progress < 1 {
		p.rejected(job, next, 0, "already in qBittorrent")
		return nil
	}
	files, err := q.Files(p.ctx, existing.Hash)
	if err != nil {
		return err
	}
	video := largestMKV(files)
	if video < 0 {
		p.rejected(job, next, 0, "no .mkv holding the film")
		return nil
	}
	path := filepath.Join(p.d.hostPath(existing.SavePath), filepath.FromSlash(files[video].Name))
	lang := p.d.langs.byCode[job.Lang]
	info := p.d.probeHead(path, min(files[video].Size, probeBudget))
	dub := pickDub(info, lang)
	if dub == nil {
		p.rejected(job, next, 0, "downloaded already, without the language")
		return nil
	}
	if film := p.d.listedFilm(job.MovieID); film.File != nil && !sameCut(info.length(), film.File.Length) {
		p.rejected(job, next, 0, "downloaded already, another cut")
		return nil
	}
	if !job.Unconfirmed && !confirmed(dub, next, lang) {
		job.Doubtful = append(job.Doubtful, next)
		p.rejected(job, next, 0, "downloaded already, "+lang.Name+" not confirmed, kept for last")
		return nil
	}
	if err := q.Keep(p.ctx, existing.Hash); err != nil {
		return err
	}
	p.probed(job, next, 0, "downloaded already, has "+lang.Name)
	job.Current = &torrentRef{candidate: next, Hash: existing.Hash, File: video, Stage: "downloading",
		Since: time.Now().Unix(), Dub: dub.describe(), Reused: true}
	job.set("downloading", next.Title+", downloaded already")
	return nil
}

func (p *pass) tryNext(job *jobState) error {
	if (len(job.Tried) >= maxProbes || len(job.Candidates) == 0) && len(job.Doubtful) > 0 && !job.Unconfirmed {
		job.Candidates, job.Doubtful, job.Unconfirmed = job.Doubtful, nil, true
		p.say(job, fmt.Sprintf("nothing confirmed; trying the %d releases whose track may be another variant", len(job.Candidates)))
	}
	if (len(job.Tried) >= maxProbes && !job.Unconfirmed) || len(job.Candidates) == 0 {
		job.set("failed", fmt.Sprintf("none of the %d releases probed carried it", len(job.Tried)))
		p.say(job, job.Reason)
		return nil
	}
	next := job.Candidates[0]
	job.Candidates = job.Candidates[1:]
	job.Tried = append(job.Tried, next.GUID)
	p.d.mu.Lock()
	charged := p.d.state.ChargedBytes
	p.d.mu.Unlock()
	if charged+next.charged() > p.d.cfg.Budget {
		p.rejected(job, next, 0, "over budget")
		return nil
	}
	torrent, err := p.d.prowlarr().DownloadTorrent(p.ctx, next.URL)
	if err != nil {
		return err
	}
	hash, err := infohash(torrent)
	if err != nil {
		p.rejected(job, next, 0, err.Error())
		return nil
	}
	q, err := p.qbit()
	if err != nil {
		return err
	}
	if existing, err := q.Torrent(p.ctx, hash); err != nil {
		return err
	} else if existing != nil {
		existing.Hash = hash
		return p.reuse(job, next, q, existing)
	}
	if err := q.EnsureCategory(p.ctx); err != nil {
		return err
	}
	if err := q.AddStopped(p.ctx, torrent, job.Title); err != nil {
		return err
	}
	var files []clients.QbitFile
	for range 10 {
		if files, err = q.Files(p.ctx, hash); err != nil {
			return err
		}
		if len(files) > 0 {
			break
		}
		time.Sleep(time.Second)
	}
	video := largestMKV(files)
	if video < 0 {
		if err := q.Delete(p.ctx, hash); err != nil {
			return err
		}
		p.rejected(job, next, 0, "no .mkv holding the film (packed, or only a sample)")
		return nil
	}
	var others []string
	for i := range files {
		if i != video {
			others = append(others, strconv.Itoa(i))
		}
	}
	if len(others) > 0 {
		if err := q.Post(p.ctx, "torrents/filePrio", url.Values{"hash": {hash}, "id": {strings.Join(others, "|")}, "priority": {"0"}}); err != nil {
			return err
		}
	}
	if err := q.Post(p.ctx, "torrents/toggleSequentialDownload", url.Values{"hashes": {hash}}); err != nil {
		return err
	}
	if err := q.Post(p.ctx, "torrents/setDownloadLimit", url.Values{"hashes": {hash}, "limit": {strconv.Itoa(probeRate)}}); err != nil {
		return err
	}
	startsAt := 1.0
	if next.Private {
		rules, err := loadTrackerRules(p.d.cfg.RulesFile)
		if err != nil {
			return err
		}
		startsAt = rules.startsAt(next.Indexer)
	}
	job.Current = &torrentRef{candidate: next, Hash: hash, File: video, Stage: "probing", Since: time.Now().Unix(),
		DeleteBelow: startsAt * float64(next.Size)}
	job.set("probing", next.Title)
	return nil
}

// checkProbe watches a probe until the head of its .mkv shows the track list, then keeps it for a
// download if one track is in the language, or drops it. It returns the bytes the download charges.
func (p *pass) checkProbe(job *jobState) (int64, error) {
	current := job.Current
	q, err := p.qbit()
	if err != nil {
		return 0, err
	}
	torrent, err := q.Torrent(p.ctx, current.Hash)
	if err != nil {
		return 0, err
	}
	if torrent == nil {
		job.Current = nil
		p.rejected(job, current.candidate, 0, "left qBittorrent")
		return 0, nil
	}
	files, err := q.Files(p.ctx, current.Hash)
	if err != nil {
		return 0, err
	}
	if current.File >= len(files) || len(files[current.File].PieceRange) == 0 {
		return 0, fmt.Errorf("qBittorrent lists no file %d in %s", current.File, current.Title)
	}
	file := files[current.File]
	var offset int64
	for _, f := range files[:current.File] {
		offset += f.Size
	}
	pieceSize, err := q.PieceSize(p.ctx, current.Hash)
	if err != nil {
		return 0, err
	}
	base := torrent.DownloadPath
	if base == "" {
		base = torrent.SavePath
	}
	path := filepath.Join(p.d.hostPath(base), filepath.FromSlash(file.Name))
	budget := float64(probeBudget)
	if current.Private {
		budget = min(budget, current.DeleteBelow/probeMargin)
	}

	if err := q.Start(p.ctx, current.Hash); err != nil {
		return 0, err
	}
	var info *probeInfo
	var read int64
	watchErr := func() error {
		deadline := time.Now().Add(p.d.watchFor)
		for time.Now().Before(deadline) && !p.d.cancelWaiting(dubKey(job.MovieID, job.Lang)) {
			states, err := q.PieceStates(p.ctx, current.Hash)
			if err != nil {
				return err
			}
			ready := min(readyBytes(states, file.PieceRange[0], offset, pieceSize), file.Size, probeBudget)
			if ready >= min(probeHead, file.Size) && ready > read {
				info, read = p.d.probeHead(path, ready), ready
				if info != nil && len(info.audio()) > 0 {
					return nil
				}
			}
			now, err := q.Torrent(p.ctx, current.Hash)
			if err != nil {
				return err
			}
			if now != nil && float64(now.Downloaded) >= budget {
				return nil
			}
			time.Sleep(p.d.pollEvery)
		}
		return nil
	}()
	stopErr := q.Stop(p.ctx, current.Hash)
	if err := errors.Join(watchErr, stopErr); err != nil {
		return 0, err
	}

	var downloaded int64
	if now, err := q.Torrent(p.ctx, current.Hash); err != nil {
		return 0, err
	} else if now != nil {
		downloaded = now.Downloaded
	}
	if info == nil || len(info.audio()) == 0 {
		switch {
		case float64(downloaded) >= budget:
			if err := p.dropTorrent(job); err != nil {
				return 0, err
			}
			p.rejected(job, current.candidate, downloaded, fmt.Sprintf("no track list in %d MB", downloaded/mb))
		case time.Now().Unix()-current.Since > probeHours*3600:
			if err := p.dropTorrent(job); err != nil {
				return 0, err
			}
			p.rejected(job, current.candidate, downloaded, fmt.Sprintf("no data after %d h", probeHours))
		}
		return 0, nil
	}
	lang := p.d.langs.byCode[job.Lang]
	dub := pickDub(info, lang)
	if dub == nil {
		var found []string
		for _, s := range info.audio() {
			code := s.language()
			if code == "" {
				code = "?"
			}
			if !slices.Contains(found, code) {
				found = append(found, code)
			}
		}
		if err := p.dropTorrent(job); err != nil {
			return 0, err
		}
		p.rejected(job, current.candidate, downloaded, fmt.Sprintf("no %s: %s", lang.Name, strings.Join(found, ", ")))
		return 0, nil
	}
	if film := p.d.listedFilm(job.MovieID); film.File != nil && !sameCut(info.length(), film.File.Length) {
		if err := p.dropTorrent(job); err != nil {
			return 0, err
		}
		p.rejected(job, current.candidate, downloaded, fmt.Sprintf("another cut: %.0f min against the film's %.0f",
			info.length()/60, film.File.Length/60))
		return 0, nil
	}
	if !job.Unconfirmed && !confirmed(dub, current.candidate, lang) {
		if err := p.dropTorrent(job); err != nil {
			return 0, err
		}
		job.Doubtful = append(job.Doubtful, current.candidate)
		p.rejected(job, current.candidate, downloaded, fmt.Sprintf("%s not confirmed: %s, kept for last", lang.Name, dub.describe()))
		return 0, nil
	}
	for _, step := range []func() error{
		func() error {
			return q.Post(p.ctx, "torrents/toggleSequentialDownload", url.Values{"hashes": {current.Hash}})
		},
		func() error {
			return q.Post(p.ctx, "torrents/setDownloadLimit", url.Values{"hashes": {current.Hash}, "limit": {"0"}})
		},
		func() error { return q.Start(p.ctx, current.Hash) },
	} {
		if err := step(); err != nil {
			return 0, err
		}
	}
	p.probed(job, current.candidate, downloaded, "has "+lang.Name)
	current.Stage, current.Since, current.Dub = "downloading", time.Now().Unix(), dub.describe()
	job.Downloads++
	job.set("downloading", fmt.Sprintf("%s, %.1f GB", current.Title, float64(current.Size)/gb))
	p.say(job, fmt.Sprintf("downloading %s (%.1f GB): %s", current.Title, float64(current.Size)/gb, current.Dub))
	return current.charged(), nil
}

// probed records what a probe of a release found, on the job and in the log.
func (p *pass) probed(job *jobState, release candidate, downloaded int64, outcome string) {
	job.Probes = append(job.Probes, probeRecord{Title: release.Title, Indexer: release.Indexer, Private: release.Private,
		MB: downloaded / mb, Outcome: outcome, Time: time.Now().Unix()})
	log.Printf("audio-manager: %s: probe %s (%s): %s, %d MB", job.Title, release.Title, release.Indexer, outcome, downloaded/mb)
}

// rejected records a release that will not do, and moves the job on to the next.
func (p *pass) rejected(job *jobState, release candidate, downloaded int64, outcome string) {
	p.probed(job, release, downloaded, outcome)
	job.set("trying", release.Title+": "+outcome)
}

// checkDownload hands a finished download to add-audio. It returns how far the counted download
// came out from what checkProbe charged for it, once the torrent is settled.
func (p *pass) checkDownload(job *jobState) (int64, error) {
	current := job.Current
	q, err := p.qbit()
	if err != nil {
		return 0, err
	}
	torrent, err := q.Torrent(p.ctx, current.Hash)
	if err != nil {
		return 0, err
	}
	if torrent == nil {
		job.Current = nil
		p.nextOrFail(job, "the download left qBittorrent")
		return 0, nil
	}
	settled := current.settle(torrent.Downloaded)
	if torrent.Progress < 1 {
		if time.Now().Unix()-current.Since > stalledHours*3600 {
			if err := p.dropTorrent(job); err != nil {
				return 0, err
			}
			p.nextOrFail(job, fmt.Sprintf("%s: %.0f%% after %d h", current.Title, torrent.Progress*100, stalledHours))
			return settled, nil
		}
		return 0, nil
	}
	movie, err := p.d.radarr().Movie(p.ctx, job.MovieID)
	if err != nil {
		return 0, err
	}
	target := ""
	if movie.MovieFile != nil {
		target = p.d.hostPath(movie.MovieFile.Path)
	}
	files, err := q.Files(p.ctx, current.Hash)
	if err != nil {
		return 0, err
	}
	if current.File >= len(files) {
		return 0, fmt.Errorf("qBittorrent lists no file %d in %s", current.File, current.Title)
	}
	source := filepath.Join(p.d.hostPath(torrent.SavePath), filepath.FromSlash(files[current.File].Name))
	if _, err := os.Stat(target); !strings.HasSuffix(target, ".mkv") || err != nil {
		if err := p.dropTorrent(job); err != nil {
			return 0, err
		}
		job.set("failed", "the library file is gone or no longer .mkv")
		p.say(job, job.Reason)
		return settled, nil
	}
	if load, err := p.d.loadAverage(); err == nil && p.d.cfg.MaxLoad > 0 && load > p.d.cfg.MaxLoad {
		job.Reason = fmt.Sprintf("downloaded; add-audio waits for the raspi to calm down (load %.1f)", load)
		return 0, nil
	}
	last, code := p.d.runAddAudio(target, source, job.Lang)
	if strings.HasPrefix(last, "busy:") {
		log.Printf("audio-manager: %s: %s, next pass", job.Title, last)
		return 0, nil
	}
	if err := p.dropTorrent(job); err != nil {
		return 0, err
	}
	if code == 0 || strings.Contains(last, "already has a") {
		job.set("done", last)
		if job.Unconfirmed {
			job.Reason += "; " + p.d.langs.byCode[job.Lang].Name + " not confirmed by its track title, check it"
		}
		p.say(job, "done: "+job.Reason)
		poke(p.d.refresh)
	} else {
		p.nextOrFail(job, "add-audio refused it: "+last)
	}
	return settled, nil
}

func (p *pass) nextOrFail(job *jobState, reason string) {
	if job.Downloads >= maxDownloads {
		job.set("failed", reason)
		p.say(job, "gave up: "+reason)
		return
	}
	job.set("trying", reason)
}

// dropTorrent deletes a probe; a full download goes to qbit-manage, which owes its site the seeding.
func (p *pass) dropTorrent(job *jobState) error {
	current := job.Current
	if current == nil {
		return nil
	}
	q, err := p.qbit()
	if err != nil {
		return err
	}
	torrent, err := q.Torrent(p.ctx, current.Hash)
	if err != nil {
		return err
	}
	if torrent != nil {
		safe := !current.Private || float64(torrent.Downloaded)*probeMargin < current.DeleteBelow
		if current.Stage == "probing" && safe {
			err = q.Delete(p.ctx, current.Hash)
		} else {
			err = q.Release(p.ctx, current.Hash)
		}
		if err != nil {
			return err
		}
	}
	job.Current = nil
	return nil
}

func (p *pass) notify() {
	token, chat := p.d.cfg.TelegramToken, p.d.cfg.TelegramChat
	if len(p.events) == 0 || token == "" || chat == "" {
		return
	}
	text := "🎧 add audio\n" + strings.Join(p.events, "\n")
	response, err := p.d.http.PostForm("https://api.telegram.org/bot"+token+"/sendMessage", url.Values{"chat_id": {chat}, "text": {text}})
	if err != nil {
		log.Printf("audio-manager: telegram failed: %v", redactToken(err, token))
		return
	}
	response.Body.Close()
}

func redactToken(err error, token string) string {
	return strings.ReplaceAll(err.Error(), token, "…")
}

// hostPath maps where Radarr and qBittorrent see a file (/data, DATA_ROOT on the host) to this container.
func (d *Service) hostPath(containerPath string) string {
	if containerPath == "/data" || strings.HasPrefix(containerPath, "/data/") {
		return filepath.Join(d.cfg.DataRoot, filepath.FromSlash(strings.TrimPrefix(containerPath, "/data")))
	}
	return containerPath
}

// readyBytes is how many bytes from the start of a file are downloaded, given where it starts in the torrent.
func readyBytes(states []int, firstPiece int, offset, pieceSize int64) int64 {
	done := 0
	for firstPiece+done < len(states) && states[firstPiece+done] == 2 {
		done++
	}
	return max(0, int64(firstPiece+done)*pieceSize-offset)
}

// infohash is the SHA-1 of the bencoded info dict, which is how qBittorrent names the torrent.
func infohash(torrent []byte) (string, error) {
	if len(torrent) == 0 || torrent[0] != 'd' {
		return "", errors.New("not a torrent file")
	}
	i := 1
	for i < len(torrent) && torrent[i] != 'e' {
		keyEnd, err := bencodeEnd(torrent, i)
		if err != nil {
			return "", err
		}
		colon := bytes.IndexByte(torrent[i:], ':')
		key := torrent[i+colon+1 : keyEnd]
		valueEnd, err := bencodeEnd(torrent, keyEnd)
		if err != nil {
			return "", err
		}
		if string(key) == "info" {
			sum := sha1.Sum(torrent[keyEnd:valueEnd])
			return hex.EncodeToString(sum[:]), nil
		}
		i = valueEnd
	}
	return "", errors.New("torrent has no info dict")
}

func bencodeEnd(data []byte, i int) (int, error) {
	if i >= len(data) {
		return 0, errors.New("truncated torrent")
	}
	switch data[i] {
	case 'i':
		end := bytes.IndexByte(data[i:], 'e')
		if end < 0 {
			return 0, errors.New("truncated torrent")
		}
		return i + end + 1, nil
	case 'l', 'd':
		i++
		for i < len(data) && data[i] != 'e' {
			next, err := bencodeEnd(data, i)
			if err != nil {
				return 0, err
			}
			i = next
		}
		if i >= len(data) {
			return 0, errors.New("truncated torrent")
		}
		return i + 1, nil
	}
	colon := bytes.IndexByte(data[i:], ':')
	if colon < 0 {
		return 0, errors.New("not a torrent file")
	}
	length, err := strconv.Atoi(string(data[i : i+colon]))
	if err != nil || length < 0 || i+colon+1+length > len(data) {
		return 0, errors.New("not a torrent file")
	}
	return i + colon + 1 + length, nil
}

// probeInfo is ffprobe's view of a file.
type probeInfo struct {
	Streams []probeStream `json:"streams"`
	Format  struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// length is the file's duration in seconds, 0 when the header does not say.
func (info *probeInfo) length() float64 {
	if info == nil {
		return 0
	}
	seconds, _ := strconv.ParseFloat(info.Format.Duration, 64)
	return seconds
}

type probeStream struct {
	Index     int    `json:"index"`
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	Channels  int    `json:"channels"`
	Tags      struct {
		Language string `json:"language"`
		Title    string `json:"title"`
	} `json:"tags"`
}

func (info *probeInfo) audio() []probeStream {
	var out []probeStream
	for _, s := range info.Streams {
		if s.CodecType == "audio" {
			out = append(out, s)
		}
	}
	return out
}

func (s probeStream) language() string { return strings.ToLower(s.Tags.Language) }

func (s probeStream) describe() string {
	lang := s.language()
	if lang == "" {
		lang = "?"
	}
	return fmt.Sprintf("#%d %s %dch %s %q", s.Index, s.CodecName, s.Channels, lang, s.Tags.Title)
}

// pickDub is add-audio.py's choice of track: the language, not another variant of it nor a
// commentary; the preferred title first, then AC3, then the most channels.
func pickDub(info *probeInfo, lang *language) *probeStream {
	var best *probeStream
	var bestKey [3]int
	for _, s := range info.audio() {
		if !lang.aliases[s.language()] || lang.rejectedTrack.MatchString(s.Tags.Title) {
			continue
		}
		key := [3]int{0, 0, s.Channels}
		if lang.preferred != nil && lang.preferred.MatchString(s.Tags.Title) {
			key[0] = 1
		}
		if s.CodecName == "ac3" {
			key[1] = 1
		}
		if best == nil || slices.Compare(key[:], bestKey[:]) > 0 {
			chosen := s
			best, bestKey = &chosen, key
		}
	}
	return best
}

// ffprobeHead is ffprobe's view of the first bytes of a partial download, or nil.
func (d *Service) ffprobeHead(path string, size int64) *probeInfo {
	source, err := os.Open(path)
	if err != nil {
		log.Printf("audio-manager: probe of %s failed: %v", path, err)
		return nil
	}
	defer source.Close()
	head, err := os.CreateTemp("", "probe-*.mkv")
	if err != nil {
		log.Printf("audio-manager: probe of %s failed: %v", path, err)
		return nil
	}
	defer os.Remove(head.Name())
	_, err = io.CopyN(head, source, size)
	head.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		log.Printf("audio-manager: probe of %s failed: %v", path, err)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-print_format", "json", "-show_format", "-show_streams", head.Name()).Output()
	if err != nil {
		return nil
	}
	var info probeInfo
	if json.Unmarshal(out, &info) != nil {
		return nil
	}
	return &info
}

// addAudioScript runs add-audio.py and returns the last line it wrote and its exit code.
func (d *Service) addAudioScript(target, source, lang string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), addAudioTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.cfg.Python, d.cfg.AddAudio, target, source, "--lang", lang)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return "add-audio timed out", 1
	}
	output := strings.TrimSpace(stdout.String() + stderr.String())
	log.Printf("audio-manager: add-audio: %s", output)
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		return err.Error(), 1
	}
	// The result is stdout's last line, a refusal stderr's; stderr also carries notes printed on the way.
	outcome := strings.TrimSpace(stdout.String())
	if code != 0 || outcome == "" {
		outcome = strings.TrimSpace(stderr.String())
	}
	if outcome == "" {
		return fmt.Sprintf("exit %d", code), code
	}
	return script.LastLine(outcome), code
}

// ffprobeFile reads the track list of a whole file, which ffprobe takes from its head.
func ffprobeFile(path string) *probeInfo {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path).Output()
	if err != nil {
		log.Printf("audio-manager: ffprobe %s: %v", path, err)
		return nil
	}
	var info probeInfo
	if json.Unmarshal(out, &info) != nil {
		return nil
	}
	return &info
}

// hostLoad is the 5-minute load average, which a container reads as the host's.
func hostLoad() (float64, error) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0, fmt.Errorf("/proc/loadavg: %q", raw)
	}
	return strconv.ParseFloat(fields[1], 64)
}

// speedRatios are the frame-rate changes between releases add-audio can undo: PAL speed-up and the like.
var speedRatios = []float64{1, 23.976 / 25, 25 / 23.976, 24.0 / 25, 25.0 / 24, 23.976 / 24, 24 / 23.976}

// maxLengthGap is how far a source may run from the film, at any of those speeds, before it is taken
// for another cut: longer credits stay well under it, an extended edition does not.
const maxLengthGap = 5 * 60

// sameCut says the source could be the film's cut, and is true when either length is unknown.
func sameCut(source, film float64) bool {
	if source <= 0 || film <= 0 {
		return true
	}
	return slices.ContainsFunc(speedRatios, func(r float64) bool { return math.Abs(source-film*r) <= maxLengthGap })
}

// confirmed says the dub is surely this language and not a variant it rejects (Latino for Castellano):
// its track title or the release title says so, or the release comes from a site in that language.
func confirmed(dub *probeStream, release candidate, lang *language) bool {
	if lang.rejectedTitle == nil || release.OwnSite {
		return true
	}
	return lang.preferred != nil && (lang.preferred.MatchString(dub.Tags.Title) || lang.preferred.MatchString(release.Title))
}
