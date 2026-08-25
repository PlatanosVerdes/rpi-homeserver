package main

// One function per site, on purpose. Every tracker here logs in differently and puts its numbers
// somewhere else, and a generic descriptor engine would hide that behind a config format nobody can
// debug. What is shared lives in stats.go: the cookie jar, the markup flattener and the unit parser.
//
// Every failure returns an error that names what was actually seen, because these were written from
// the login forms and one pasted profile page rather than from a session: the first run with a real
// password is what confirms each shape, and a silent empty result would waste that run.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var csrfMeta = regexp.MustCompile(`name="csrf-token"\s+content="([^"]+)"`)

// credentials prefers the environment, because Prowlarr only holds a login for the sites whose
// indexer definition needs one. C411 and DigitalCore are API-key definitions and retrotoon is a
// generic Torznab entry, so for those three there is nothing in Prowlarr to reuse.
func credentials(tracker string, config map[string]any) (string, string, error) {
	prefix := strings.ToUpper(tracker)
	user := env(prefix+"_USER", "")
	password := env(prefix+"_PASSWORD", "")
	if user != "" && password != "" {
		return user, password, nil
	}
	fromProwlarr, prowlarrPassword, _, err := prowlarrCredentials(
		withDefault(str(config, "prowlarr_indexer"), tracker))
	if err == nil && fromProwlarr != "" && prowlarrPassword != "" {
		return fromProwlarr, prowlarrPassword, nil
	}
	missing := prefix + "_USER"
	if user != "" {
		missing = prefix + "_PASSWORD"
	}
	return "", "", fmt.Errorf("no login for this site: set %s in .env, or give prowlarr one", missing)
}

func body(resp *http.Response) string {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	return string(raw)
}

// fetchDigitalCore. Their API refuses an API key on user endpoints by name and refuses a passkey
// too, but /api/v1/auth/login takes a username and password as JSON and answers with the whole
// account, and the session cookie it sets then reads /api/v1/users/<id>. Note the plural and the id:
// /api/v1/user, /users/current and /account are all 404 to a session, which is why the shape had to
// be found rather than assumed.
//
// The id is kept beside the cookie so a run with a live session costs one GET. Logging in every half
// hour for a number that moves in gigabytes is how an account gets noticed.
//
// Field names are mapped one by one on purpose. Matching them by meaning looked tidier and was
// wrong: this payload carries uploaded_real and downloaded_real next to uploaded and downloaded,
// where the pairs differ by a factor of thirty because of freeleech and upload multipliers, plus a
// doljuploader flag. Anything scanning for a key containing "upload" picks one of those at random,
// since Go walks a map in no fixed order.
func fetchDigitalCore(tracker string, config map[string]any) (profile, error) {
	var out profile
	site := strings.TrimRight(str(config, "site"), "/")
	base, err := url.Parse(site)
	if err != nil {
		return out, err
	}
	client, save, err := siteClient(tracker, base)
	if err != nil {
		return out, err
	}
	idPath := filepath.Join(stateDir, tracker+"-id")

	account := func(path string) map[string]any {
		request, err := http.NewRequest("GET", site+path, nil)
		if err != nil {
			return nil
		}
		request.Header.Set("User-Agent", userAgent)
		resp, err := client.Do(request)
		if err != nil {
			return nil
		}
		text := body(resp)
		if resp.StatusCode != 200 {
			return nil
		}
		var parsed map[string]any
		if json.Unmarshal([]byte(text), &parsed) != nil {
			return nil
		}
		if _, ok := parsed["uploaded"]; !ok {
			return nil
		}
		return parsed
	}

	var user map[string]any
	if raw, err := os.ReadFile(idPath); err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			user = account("/api/v1/users/" + url.PathEscape(id))
		}
	}

	if user == nil {
		name, password, err := credentials(tracker, config)
		if err != nil {
			return out, err
		}
		payload, _ := json.Marshal(map[string]string{"username": name, "password": password})
		request, _ := http.NewRequest("POST", site+"/api/v1/auth/login", strings.NewReader(string(payload)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", userAgent)
		resp, err := client.Do(request)
		if err != nil {
			return out, err
		}
		text := body(resp)
		if resp.StatusCode != 200 && resp.StatusCode != 201 {
			return out, fmt.Errorf("login returned %d: %s", resp.StatusCode, apiMessage(text))
		}
		var answer struct {
			User              map[string]any `json:"user"`
			TwoFactorRequired bool           `json:"twoFactorRequired"`
		}
		if json.Unmarshal([]byte(text), &answer) != nil {
			return out, fmt.Errorf("login answered something that is not an object: %.140s", text)
		}
		// The password is right and the session is still not usable: a code cannot come from a
		// config file, so the way in is a browser session dropped into the cookie file.
		if answer.TwoFactorRequired {
			return out, fmt.Errorf("password accepted but the account asks for a second factor, " +
				"so seed the cookie from a browser instead")
		}
		if answer.User == nil {
			return out, fmt.Errorf("login held no user object, keys were: %s", topKeys(text))
		}
		save()
		user = answer.User
		if id, ok := numberField(user, "id"); ok {
			os.MkdirAll(stateDir, 0o755)
			os.WriteFile(idPath, []byte(fmt.Sprintf("%.0f", id)), 0o600)
		}
	}
	save()

	uploaded, hasUp := numberField(user, "uploaded")
	downloaded, hasDown := numberField(user, "downloaded")
	if !hasUp || !hasDown {
		return out, fmt.Errorf("the account held no uploaded/downloaded pair")
	}
	out.uploaded, out.downloaded = uploaded, downloaded
	if downloaded > 0 {
		out.ratio = uploaded / downloaded
	}
	if points, ok := numberField(user, "bonuspoang"); ok {
		out.points = points
	}
	if class, ok := numberField(user, "class"); ok {
		out.class = fmt.Sprintf("%.0f", class)
	}
	// warneduntil only comes back on a fresh login, and "no" is the normal answer to warned.
	if until, ok := user["warneduntil"].(string); ok && !strings.HasPrefix(until, "0000") {
		out.warnedUntil = until
	}
	return out, nil
}

// fetchC411. Their API keys are scoped to Torznab, torrent upload and upload drafts, so account
// figures are not reachable with one. The login is /api/v1/../api/auth/login taking JSON with the
// csrf token from the page's <meta name="csrf-token"> as a header: every /api/** path answers a bare
// 401 to a stranger, which reads like the route does not exist, but with the token and a real body
// it answers in French about the credentials themselves. Posting the form to /login instead sets no
// cookie and returns the page again, which is a login that failed while looking like one that
// worked.
//
// The figures then sit in the header of every page behind the session, value above label, in French
// units.
func fetchC411(tracker string, config map[string]any) (profile, error) {
	var out profile
	user, password, err := credentials(tracker, config)
	if err != nil {
		return out, err
	}
	site := strings.TrimRight(str(config, "site"), "/")
	base, err := url.Parse(site)
	if err != nil {
		return out, err
	}
	client, save, err := siteClient(tracker, base)
	if err != nil {
		return out, err
	}

	page := func(path string) (string, int, error) {
		request, _ := http.NewRequest("GET", site+path, nil)
		request.Header.Set("User-Agent", userAgent)
		request.Header.Set("Accept-Language", "fr-FR,fr;q=0.9")
		resp, err := client.Do(request)
		if err != nil {
			return "", 0, err
		}
		return body(resp), resp.StatusCode, nil
	}

	text, status, err := page("/user/integrations")
	if err != nil {
		return out, err
	}
	if status != 200 || !strings.Contains(text, "Ratio") {
		login, _, err := page("/login")
		if err != nil {
			return out, err
		}
		token := ""
		if match := csrfMeta.FindStringSubmatch(login); match != nil {
			token = match[1]
		}
		payload, _ := json.Marshal(map[string]string{"username": user, "password": password})
		request, _ := http.NewRequest("POST", site+"/api/auth/login", strings.NewReader(string(payload)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", userAgent)
		request.Header.Set("Referer", site+"/login")
		request.Header.Set("Origin", site)
		if token != "" {
			request.Header.Set("csrf-token", token)
		}
		resp, err := client.Do(request)
		if err != nil {
			return out, err
		}
		answer := body(resp)
		if resp.StatusCode >= 400 {
			// Their message is the useful part and it is in French: "Nom d'utilisateur ou mot de
			// passe invalide" means exactly that, not a broken request.
			return out, fmt.Errorf("login returned %d: %s", resp.StatusCode, apiMessage(answer))
		}
		save()
		if text, status, err = page("/user/integrations"); err != nil {
			return out, err
		}
		if status != 200 {
			return out, fmt.Errorf("logged in but the account page returned %d", status)
		}
	}
	save()

	// The profile block prints the value above its label, the opposite of TorrentLeech, and the
	// units are French: 52.8 Go rather than 52.8 GB.
	lines := flatten(text)
	uploaded, hasUp := toBytes(valueBefore(lines, "Envoyé"))
	downloaded, hasDown := toBytes(valueBefore(lines, "Téléchargé"))
	ratio, hasRatio := toFloat(valueBefore(lines, "Ratio"))
	if !hasUp || !hasDown {
		// The header carries the same three figures as ↑52.8 Go | 0.83 | ↓63.3 Go
		if match := regexp.MustCompile(
			`↑\s*([\d.,]+\s*[KMGT]?o)\s*\|?\s*([\d.,]+)\s*\|?\s*↓\s*([\d.,]+\s*[KMGT]?o)`,
		).FindStringSubmatch(strings.Join(lines, " ")); match != nil {
			uploaded, hasUp = toBytes(match[1])
			ratio, hasRatio = toFloat(match[2])
			downloaded, hasDown = toBytes(match[3])
		}
	}
	if !hasUp || !hasDown {
		return out, fmt.Errorf("logged in but found no Envoyé/Téléchargé figures on the page")
	}
	out.uploaded, out.downloaded = uploaded, downloaded
	if hasRatio {
		out.ratio = ratio
	} else if downloaded > 0 {
		out.ratio = uploaded / downloaded
	}
	return out, nil
}

// topKeys names what a payload did contain, so a shape that changed says so instead of going quiet.
func topKeys(raw string) string {
	var document map[string]any
	if json.Unmarshal([]byte(raw), &document) != nil {
		return "(not an object)"
	}
	var names []string
	for key := range document {
		names = append(names, key)
	}
	if len(names) == 0 {
		return "(none)"
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// numberField reads one named number, whether the site sends it as a number or as a string.
func numberField(document map[string]any, key string) (float64, bool) {
	switch typed := document[key].(type) {
	case float64:
		return typed, true
	case string:
		return toFloat(typed)
	}
	return 0, false
}

// apiMessage digs the human sentence out of a JSON error envelope, because that sentence is what
// says whether a login was refused or a request was malformed.
func apiMessage(raw string) string {
	var envelope map[string]any
	if json.Unmarshal([]byte(raw), &envelope) == nil {
		for _, key := range []string{"message", "error", "statusMessage"} {
			if text, ok := envelope[key].(string); ok && text != "" {
				return text
			}
		}
	}
	if len(raw) > 140 {
		return raw[:140]
	}
	return raw
}
