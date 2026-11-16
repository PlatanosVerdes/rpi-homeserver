package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

// The languages a dub can be added in, from scripts/sync/languages.json, which add-audio.py reads too.

type language struct {
	Code      string   `json:"-"`
	Name      string   `json:"name"`
	Tag       string   `json:"tag"`
	Locale    string   `json:"locale"`
	Aliases   []string `json:"aliases"`
	Words     []string `json:"words"`
	Rejected  string   `json:"rejected"`
	Preferred string   `json:"preferred"`

	names         map[string]bool // aliases and words, lowercase: how Radarr names a track
	aliases       map[string]bool // how a file tags the track
	hint          *regexp.Regexp  // a release title naming the language
	rejectedTitle *regexp.Regexp  // a release of a variant that is not this one; nil when none
	rejectedTrack *regexp.Regexp  // a track of another variant, or a commentary
	preferred     *regexp.Regexp  // nil when none
}

type languages struct {
	order  []string
	byCode map[string]*language
}

const commentary = "commentary|comentario"

// Python's \b knows letters beyond ASCII and RE2's does not, so "català" needs its own boundaries.
const wordEdge = `[^\pL\pN_]`

func loadLanguages(file string) (*languages, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Languages json.RawMessage `json:"languages"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	out := &languages{byCode: map[string]*language{}}
	err = forEachKey(doc.Languages, func(code string, dec *json.Decoder) error {
		lang := &language{Code: code}
		if err := dec.Decode(lang); err != nil {
			return fmt.Errorf("%s: %w", code, err)
		}
		if err := lang.compile(); err != nil {
			return fmt.Errorf("%s: %w", code, err)
		}
		out.order = append(out.order, code)
		out.byCode[code] = lang
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if len(out.order) == 0 {
		return nil, fmt.Errorf("%s lists no language", file)
	}
	return out, nil
}

func (l *language) compile() error {
	l.names, l.aliases = map[string]bool{}, map[string]bool{}
	for _, alias := range l.Aliases {
		l.aliases[strings.ToLower(alias)] = true
		l.names[strings.ToLower(alias)] = true
	}
	words := slices.Clone(l.Words)
	slices.Sort(words)
	quoted := make([]string, len(words))
	for i, word := range words {
		l.names[strings.ToLower(word)] = true
		quoted[i] = regexp.QuoteMeta(word)
	}
	var err error
	if l.hint, err = regexp.Compile(`(?i)(?:^|` + wordEdge + `)(?:` + strings.Join(quoted, "|") + `)(?:` + wordEdge + `|$)`); err != nil {
		return err
	}
	track := commentary
	if l.Rejected != "" {
		if l.rejectedTitle, err = regexp.Compile("(?i)" + l.Rejected); err != nil {
			return err
		}
		track = l.Rejected + "|" + commentary
	}
	if l.rejectedTrack, err = regexp.Compile("(?i)" + track); err != nil {
		return err
	}
	if l.Preferred != "" {
		if l.preferred, err = regexp.Compile("(?i)" + l.Preferred); err != nil {
			return err
		}
	}
	return nil
}

func (ls *languages) names() map[string]string {
	out := make(map[string]string, len(ls.order))
	for _, code := range ls.order {
		out[code] = ls.byCode[code].Name
	}
	return out
}

// has says whether Radarr's audio languages ("English / Spanish", "eng/spa") include this one.
func (l *language) has(audioLanguages string) bool {
	for _, word := range strings.FieldsFunc(audioLanguages, func(r rune) bool { return r == '/' || r == ',' }) {
		if l.names[strings.ToLower(strings.TrimSpace(word))] {
			return true
		}
	}
	return false
}

// forEachKey walks a JSON object in the file's own order, which a map would lose.
func forEachKey(raw []byte, each func(key string, dec *json.Decoder) error) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if token, err := dec.Token(); err != nil || token != json.Delim('{') {
		return fmt.Errorf("not a JSON object")
	}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		if err := each(token.(string), dec); err != nil {
			return err
		}
	}
	return nil
}
