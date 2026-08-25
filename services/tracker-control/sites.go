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
	"regexp"
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

// fetchDigitalCore. Their API rejects an API key on every user endpoint by name
// ("This action (GET:user) is not allowed for API key access") and a passkey is not accepted either,
// but the same 401 lists a login cookie as a valid credential, and /api/v1/auth/login answers
// "Bad credentials." to an empty body. So: log in, keep the cookie, read JSON.
func fetchDigitalCore(tracker string, config map[string]any) (profile, error) {
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

	read := func() (map[string]any, int, error) {
		request, _ := http.NewRequest("GET", site+"/api/v1/users/current", nil)
		request.Header.Set("User-Agent", userAgent)
		resp, err := client.Do(request)
		if err != nil {
			return nil, 0, err
		}
		text := body(resp)
		if resp.StatusCode != 200 {
			return nil, resp.StatusCode, nil
		}
		var parsed map[string]any
		if json.Unmarshal([]byte(text), &parsed) != nil {
			return nil, resp.StatusCode, fmt.Errorf("users/current was not an object: %.120s", text)
		}
		return parsed, resp.StatusCode, nil
	}

	account, status, err := read()
	if err != nil {
		return out, err
	}
	if account == nil {
		payload, _ := json.Marshal(map[string]string{"username": user, "password": password})
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
		// The password is accepted and the session is still not complete: this account has an
		// authenticator on it, and the code cannot come from a config file. What works instead is
		// logging in from a browser once and dropping that session into
		// appdata/tracker-stats/digitalcore-cookies.json, which is read before any login is tried.
		var answer struct {
			TwoFactorRequired bool `json:"twoFactorRequired"`
		}
		if json.Unmarshal([]byte(text), &answer) == nil && answer.TwoFactorRequired {
			return out, fmt.Errorf("password accepted but the account asks for a second factor, " +
				"so seed the cookie from a browser instead")
		}
		save()
		if account, status, err = read(); err != nil {
			return out, err
		}
		if account == nil {
			return out, fmt.Errorf("logged in but users/current still returned %d", status)
		}
	}
	save()

	// Their field names are unknown until a real session answers, so match on what a key means
	// rather than on a name guessed from nothing.
	found := map[string]bool{}
	walk(account, func(key string, value any) {
		lower := strings.ToLower(key)
		number, isNumber := asNumber(value)
		switch {
		case !isNumber:
			if text, ok := value.(string); ok && strings.Contains(lower, "class") && out.class == "" {
				out.class = text
			}
		case strings.Contains(lower, "upload") && !strings.Contains(lower, "multi") && !found["up"]:
			out.uploaded, found["up"] = number, true
		case strings.Contains(lower, "download") && !strings.Contains(lower, "multi") && !found["down"]:
			out.downloaded, found["down"] = number, true
		case lower == "ratio" && !found["ratio"]:
			out.ratio, found["ratio"] = number, true
		case (strings.Contains(lower, "bonus") || strings.Contains(lower, "points")) && !found["points"]:
			out.points, found["points"] = number, true
		}
	})
	if !found["up"] || !found["down"] {
		return out, fmt.Errorf("users/current held no byte counters, keys were: %s", topKeys(account))
	}
	if !found["ratio"] && out.downloaded > 0 {
		out.ratio = out.uploaded / out.downloaded
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

// walk visits every key in a decoded JSON document, however deeply it is nested.
func walk(value any, visit func(key string, value any)) {
	switch typed := value.(type) {
	case map[string]any:
		for key, inner := range typed {
			visit(key, inner)
			walk(inner, visit)
		}
	case []any:
		for _, inner := range typed {
			walk(inner, visit)
		}
	}
}

func asNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case string:
		return toFloat(typed)
	}
	return 0, false
}

func topKeys(document map[string]any) string {
	var names []string
	for key := range document {
		names = append(names, key)
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
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
