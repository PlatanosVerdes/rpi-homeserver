package main

import "net/http"

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexPage))
}

func iconHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(iconSVG))
}

// A rounded-square glyph of two staggered subtitle lines over a play triangle, in the same teal
// accent as the page itself, since none of Homepage's bundled icons fit a homegrown tool.
const iconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
  <rect width="64" height="64" rx="14" fill="#0f2f2b"/>
  <path d="M24 20 L44 32 L24 44 Z" fill="#2dd4bf" opacity="0.35"/>
  <rect x="14" y="38" width="24" height="6" rx="3" fill="#2dd4bf"/>
  <rect x="14" y="48" width="36" height="6" rx="3" fill="#2dd4bf"/>
</svg>`

const indexPage = `<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>SubDown</title>
<link rel="icon" href="/icon.svg">
<style>
  :root {
    --bg: #0b0d10;
    --surface: #14171c;
    --surface-2: #1b1f26;
    --border: rgba(255,255,255,0.08);
    --text: #e8eaed;
    --text-dim: #8b929c;
    --accent: #2dd4bf;
    --accent-dim: rgba(45,212,191,0.15);
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    background: var(--bg);
    color: var(--text);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    -webkit-font-smoothing: antialiased;
  }
  .wrap { max-width: 720px; margin: 0 auto; padding: 1.5rem 1.25rem 4rem; }
  header { position: sticky; top: 0; background: var(--bg); padding: 1.25rem 0 0.75rem; z-index: 10; }
  .top { display: flex; align-items: center; justify-content: space-between; gap: 0.75rem; margin-bottom: 0.9rem; }
  h1 { font-size: 1.35rem; margin: 0; font-weight: 650; letter-spacing: -0.01em; }
  a.all-btn {
    background: var(--accent); color: #06201c; text-decoration: none; font-weight: 650;
    font-size: 0.82rem; padding: 0.5rem 0.85rem; border-radius: 10px; white-space: nowrap;
  }
  a.all-btn:active { opacity: 0.8; }
  .poster {
    width: 40px; height: 58px; border-radius: 7px; object-fit: cover; flex: none;
    background: var(--surface-2);
  }
  .search {
    display: flex; align-items: center; gap: 0.6rem;
    background: var(--surface); border: 1px solid var(--border);
    border-radius: 12px; padding: 0.7rem 0.9rem;
  }
  .search svg { flex: none; opacity: 0.55; }
  .search input {
    flex: 1; background: transparent; border: none; outline: none;
    color: var(--text); font-size: 1rem; font-family: inherit;
  }
  .search input::placeholder { color: var(--text-dim); }
  .tabs { display: flex; gap: 0.4rem; margin-top: 0.75rem; }
  .tab {
    border: 1px solid var(--border); background: var(--surface); color: var(--text-dim);
    border-radius: 999px; padding: 0.4rem 0.9rem; font-size: 0.85rem; cursor: pointer;
    font-family: inherit; transition: background 0.15s, color 0.15s, border-color 0.15s;
  }
  .tab.active { background: var(--accent-dim); color: var(--accent); border-color: var(--accent); }
  #langs-btn { margin-left: auto; }
  #langs-btn .badge { color: var(--accent); font-weight: 650; }
  /* The language picker is collapsed by default: with embedded tracks in the list some films carry
     30 languages, so the panel is longer than everything above it and is not what you came for. */
  #langs { display: none; flex-wrap: wrap; gap: 0.35rem; margin-top: 0.6rem;
    background: var(--surface); border: 1px solid var(--border); border-radius: 12px; padding: 0.7rem; }
  #langs.open { display: flex; }
  .lang {
    display: inline-flex; align-items: center; gap: 0.3rem; cursor: pointer; font-family: inherit;
    background: var(--surface-2); color: var(--text-dim); border: 1px solid transparent;
    border-radius: 8px; padding: 0.3rem 0.5rem; font-size: 0.72rem; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.03em;
  }
  .lang.on { background: var(--accent-dim); color: var(--accent); border-color: var(--accent); }
  .lang .flag { font-size: 1.05rem; }
  .presets { display: flex; gap: 0.5rem; width: 100%; padding-top: 0.5rem; border-top: 1px solid var(--border); }
  .presets button {
    background: none; border: none; padding: 0; cursor: pointer; font-family: inherit;
    font-size: 0.78rem; color: var(--accent); text-decoration: underline;
  }
  .count { color: var(--text-dim); font-size: 0.8rem; margin: 0.9rem 0 0.6rem; }
  .card {
    background: var(--surface); border: 1px solid var(--border); border-radius: 14px;
    padding: 0.85rem 1rem; margin-bottom: 0.6rem;
  }
  .row { display: flex; align-items: center; justify-content: space-between; gap: 0.7rem; }
  .title { font-size: 0.98rem; font-weight: 500; flex: 1; min-width: 5rem; overflow: hidden; text-overflow: ellipsis; }
  /* Capped so that a title always keeps its half of the row: one chip per language is usually two
     or three, but "Todos los idiomas" on a release that ships 20 would otherwise squeeze the name
     out of its own card. */
  .chips { display: flex; gap: 0.4rem; flex-wrap: wrap; justify-content: flex-end; max-width: 60%; }
  .chip {
    display: inline-flex; align-items: center;
    background: var(--surface-2); color: var(--accent); text-decoration: none;
    padding: 0.4rem 0.75rem; border-radius: 8px; font-size: 0.82rem; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.03em; white-space: nowrap;
    border: 1px solid transparent; transition: border-color 0.15s, background 0.15s;
    font-family: inherit; cursor: pointer;
  }
  .chip:active { border-color: var(--accent); }
  .chip.open { background: var(--accent-dim); border-color: var(--accent); }
  /* How many tracks sit behind the chip, i.e. how many rows opening it will show. */
  .chip .n { margin-left: 0.35rem; font-size: 0.7rem; font-weight: 600; color: var(--text-dim); }
  /* The flag is the whole label on a language chip, and at the chip's own 0.82rem it read as a
     smudge rather than a country. line-height keeps the chip from growing with it. */
  .flag { font-size: 1.5rem; line-height: 1; }
  .variants {
    display: none; gap: 0.4rem; flex-wrap: wrap; justify-content: flex-end;
    margin-top: 0.6rem; padding-top: 0.6rem; border-top: 1px solid var(--border);
  }
  .variants.open { display: flex; }
  .chip.variant { text-transform: none; letter-spacing: 0; font-weight: 500; }
  .chip.variant .vlbl { margin-left: 0.35rem; }
  .chip.all { color: var(--text); background: var(--accent-dim); text-transform: none; letter-spacing: 0; }
  .series .row { cursor: pointer; }
  .chevron { color: var(--text-dim); transition: transform 0.2s; flex: none; }
  .series.open .chevron { transform: rotate(90deg); }
  .episodes { display: none; margin-top: 0.7rem; padding-top: 0.7rem; border-top: 1px solid var(--border); }
  .series.open .episodes { display: block; }
  .episode { padding: 0.45rem 0; }
  .episode + .episode { border-top: 1px solid var(--border); }
  .ep-row { display: flex; align-items: center; justify-content: space-between; gap: 0.7rem; }
  .ep-title { font-size: 0.88rem; color: var(--text-dim); flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; }
  .ep-num { color: var(--text); font-weight: 600; margin-right: 0.4rem; }
  .empty { color: var(--text-dim); text-align: center; padding: 3rem 1rem; font-size: 0.95rem; }
</style>
</head>
<body>
<div class="wrap">
  <header>
    <div class="top">
      <h1>SubDown</h1>
      <a class="all-btn" id="all-btn" href="/download-all">Descargar todo</a>
    </div>
    <div class="search">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line>
      </svg>
      <input id="q" type="search" placeholder="Buscar pelicula o serie..." autocomplete="off" autofocus>
    </div>
    <div class="tabs">
      <button class="tab active" data-filter="all">Todo</button>
      <button class="tab" data-filter="movies">Peliculas</button>
      <button class="tab" data-filter="series">Series</button>
      <button class="tab" id="langs-btn">Idiomas <span class="badge" id="langs-count"></span></button>
    </div>
    <div id="langs"></div>
  </header>
  <div class="count" id="count"></div>
  <div id="list"></div>
</div>
<script>
let data = { movies: [], series: [] };
let filter = 'all';

// Every release that ships its own subtitles ships 20 of them, so showing all of a library's
// languages at once buries the two anyone here reads. The picker starts on these and remembers
// whatever it is changed to; an empty selection means every language.
var DEFAULT_LANGS = ['eng', 'spa'];
var LANGS_KEY = 'subdown.langs';
var langs = new Set(loadLangs());

function loadLangs() {
  try {
    var raw = localStorage.getItem(LANGS_KEY);
    if (raw) return JSON.parse(raw);
  } catch (e) { /* private window, or a value someone hand-edited: fall back to the default */ }
  return DEFAULT_LANGS;
}

function saveLangs() {
  try { localStorage.setItem(LANGS_KEY, JSON.stringify(Array.from(langs))); } catch (e) {}
}

function norm(s) {
  return (s || '').toLowerCase().normalize('NFD').replace(/[\u0300-\u036f]/g, '');
}

function esc(s) {
  return (s || '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// Only languages whose flag is unambiguous get one; the rest fall back to their ISO code, which
// beats showing one country's flag for several of its languages.
var FLAGS = {
  eng: '\ud83c\uddec\ud83c\udde7', spa: '\ud83c\uddea\ud83c\uddf8', por: '\ud83c\uddf5\ud83c\uddf9', fra: '\ud83c\uddeb\ud83c\uddf7',
  ita: '\ud83c\uddee\ud83c\uddf9', deu: '\ud83c\udde9\ud83c\uddea', nld: '\ud83c\uddf3\ud83c\uddf1', zho: '\ud83c\udde8\ud83c\uddf3',
  rus: '\ud83c\uddf7\ud83c\uddfa', jpn: '\ud83c\uddef\ud83c\uddf5', kor: '\ud83c\uddf0\ud83c\uddf7', ara: '\ud83c\uddf8\ud83c\udde6',
  tha: '\ud83c\uddf9\ud83c\udded', ind: '\ud83c\uddee\ud83c\udde9', dan: '\ud83c\udde9\ud83c\uddf0', swe: '\ud83c\uddf8\ud83c\uddea',
  nor: '\ud83c\uddf3\ud83c\uddf4', fin: '\ud83c\uddeb\ud83c\uddee', pol: '\ud83c\uddf5\ud83c\uddf1', tur: '\ud83c\uddf9\ud83c\uddf7',
  ell: '\ud83c\uddec\ud83c\uddf7', heb: '\ud83c\uddee\ud83c\uddf1', hin: '\ud83c\uddee\ud83c\uddf3', ces: '\ud83c\udde8\ud83c\uddff',
  hun: '\ud83c\udded\ud83c\uddfa', ukr: '\ud83c\uddfa\ud83c\udde6', vie: '\ud83c\uddfb\ud83c\uddf3', lav: '\ud83c\uddf1\ud83c\uddfb',
  est: '\ud83c\uddea\ud83c\uddea', lit: '\ud83c\uddf1\ud83c\uddf9', bul: '\ud83c\udde7\ud83c\uddec', slk: '\ud83c\uddf8\ud83c\uddf0',
  slv: '\ud83c\uddf8\ud83c\uddee', isl: '\ud83c\uddee\ud83c\uddf8', ron: '\ud83c\uddf7\ud83c\uddf4', mkd: '\ud83c\uddf2\ud83c\uddf0',
  fas: '\ud83c\uddee\ud83c\uddf7', msa: '\ud83c\uddf2\ud83c\uddfe', tgl: '\ud83c\uddf5\ud83c\udded', aze: '\ud83c\udde6\ud83c\uddff',
  hrv: '\ud83c\udded\ud83c\uddf7', hye: '\ud83c\udde6\ud83c\uddf2', kat: '\ud83c\uddec\ud83c\uddea', kaz: '\ud83c\uddf0\ud83c\uddff',
  khm: '\ud83c\uddf0\ud83c\udded', kir: '\ud83c\uddf0\ud83c\uddec', sqi: '\ud83c\udde6\ud83c\uddf1', srp: '\ud83c\uddf7\ud83c\uddf8',
  mya: '\ud83c\uddf2\ud83c\uddf2'
};

function langLabel(lang) {
  // The flag carries its own span so it can be sized on its own: a.chip also draws the
  // "Descargar todos" button, and growing that one too would throw the header off.
  var flag = FLAGS[lang];
  return flag ? '<span class="flag">' + flag + '</span>' : lang.toUpperCase();
}

function chip(href, label, extra, stop) {
  var onclick = stop ? ' onclick="event.stopPropagation()"' : '';
  return '<a class="chip' + (extra ? ' ' + extra : '') + '" href="' + href + '"' + onclick + '>' + label + '</a>';
}

function poster(id) {
  return '<img class="poster" src="/image/' + id + '" loading="lazy" onerror="this.style.visibility=\'hidden\'" alt="">';
}

function keepLangs(subs) {
  if (langs.size === 0) return subs;
  return subs.filter(function (s) { return langs.has(s.lang); });
}

// The zip endpoints take the same selection, so a download holds what the page was showing
// instead of every language the file happens to carry.
function langsQuery(sep) {
  return langs.size === 0 ? '' : sep + 'langs=' + Array.from(langs).join(',');
}

function downloadHref(id, name, sub) {
  return '/download/' + id + '?index=' + sub.index +
    '&lang=' + encodeURIComponent(sub.lang) +
    '&label=' + encodeURIComponent(sub.label || '') +
    '&name=' + encodeURIComponent(name);
}

function byLanguage(subs) {
  var order = [], tracks = {};
  subs.forEach(function (s) {
    if (!tracks[s.lang]) { tracks[s.lang] = []; order.push(s.lang); }
    tracks[s.lang].push(s);
  });
  return order.map(function (lang) { return { lang: lang, tracks: tracks[lang] }; });
}

// One chip per language, whatever the file carries: a release with four English tracks (plain,
// forced, SDH, British) used to spread four chips across the row and push the title out of its own
// card. A language with more than one track opens them underneath instead, where there is room to
// name each one, and a language with just one still downloads on a single tap.
function subsMarkup(id, name, subs) {
  var groups = byLanguage(subs);
  var chips = groups.map(function (g) {
    if (g.tracks.length === 1) return chip(downloadHref(id, name, g.tracks[0]), langLabel(g.lang));
    return '<button class="chip" data-lang="' + g.lang + '">' + langLabel(g.lang) +
      '<span class="n">' + g.tracks.length + '</span></button>';
  }).join('');
  var variants = groups.filter(function (g) { return g.tracks.length > 1; }).map(function (g) {
    return '<div class="variants" data-lang="' + g.lang + '">' + g.tracks.map(function (s) {
      var label = langLabel(g.lang) + '<span class="vlbl">' + esc(s.label || 'normal') + '</span>';
      return chip(downloadHref(id, name, s), label, 'variant');
    }).join('') + '</div>';
  }).join('');
  return { chips: '<span class="chips">' + chips + '</span>', variants: variants };
}

function movieCard(m) {
  var subs = subsMarkup(m.id, m.title, keepLangs(m.subs));
  return '<div class="card subs">' +
    '<div class="row">' + poster(m.id) +
    '<span class="title">' + esc(m.title) + '</span>' + subs.chips + '</div>' +
    subs.variants +
    '</div>';
}

function seriesCard(s) {
  var epRows = s.episodes.map(function (ep) {
    var num = 'S' + String(ep.season).padStart(2, '0') + 'E' + String(ep.episode).padStart(2, '0');
    var subs = subsMarkup(ep.id, s.title + ' ' + num, ep.subs);
    return '<div class="episode subs">' +
      '<div class="ep-row">' +
      '<span class="ep-title"><span class="ep-num">' + num + '</span>' + esc(ep.title) + '</span>' +
      subs.chips + '</div>' +
      subs.variants +
      '</div>';
  }).join('');
  var allHref = '/download-all/' + s.id + '?name=' + encodeURIComponent(s.title) + langsQuery('&');
  return '<div class="card series" data-id="' + s.id + '">' +
    '<div class="row" onclick="toggleSeries(this)">' +
    poster(s.id) +
    '<span class="title">' + esc(s.title) + ' <span style="color:var(--text-dim);font-weight:400;">(' + s.episodes.length + ' ep.)</span></span>' +
    '<span class="chips">' + chip(allHref, 'Descargar todos', 'all', true) +
    '<svg class="chevron" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="9 18 15 12 9 6"></polyline></svg>' +
    '</span></div>' +
    '<div class="episodes">' + epRows + '</div>' +
    '</div>';
}

function toggleSeries(rowEl) {
  rowEl.parentElement.classList.toggle('open');
}

// A series keeps only the episodes that still have a track to offer, so a language nobody in the
// show speaks does not leave a card claiming episodes with no chips under it.
function seriesInLangs(s) {
  var episodes = s.episodes.map(function (ep) {
    return { id: ep.id, season: ep.season, episode: ep.episode, title: ep.title, subs: keepLangs(ep.subs) };
  }).filter(function (ep) { return ep.subs.length > 0; });
  return { id: s.id, title: s.title, episodes: episodes };
}

function renderLangs() {
  var counts = {};
  function tally(subs) { subs.forEach(function (s) { counts[s.lang] = (counts[s.lang] || 0) + 1; }); }
  data.movies.forEach(function (m) { tally(m.subs); });
  data.series.forEach(function (s) { s.episodes.forEach(function (ep) { tally(ep.subs); }); });

  // Commonest first, so the languages actually worth picking sit at the top of the panel.
  var codes = Object.keys(counts).sort(function (a, b) { return counts[b] - counts[a] || a.localeCompare(b); });
  var html = codes.map(function (code) {
    return '<button class="lang' + (langs.has(code) ? ' on' : '') + '" data-lang="' + code + '">' +
      (FLAGS[code] ? '<span class="flag">' + FLAGS[code] + '</span>' : '') + esc(code) +
      '</button>';
  }).join('');
  html += '<div class="presets">' +
    '<button data-preset="default">Solo ES + EN</button>' +
    '<button data-preset="all">Todos los idiomas</button>' +
    '</div>';
  document.getElementById('langs').innerHTML = html;
  document.getElementById('langs-count').textContent = langs.size === 0 ? 'todos' : String(langs.size);
}

function render() {
  var q = norm(document.getElementById('q').value);
  var showMovies = filter === 'all' || filter === 'movies';
  var showSeries = filter === 'all' || filter === 'series';

  var movies = showMovies ? data.movies.filter(function (m) { return norm(m.title).includes(q); }) : [];
  var series = showSeries ? data.series.filter(function (s) { return norm(s.title).includes(q); }) : [];
  var matched = movies.length + series.length;

  movies = movies.filter(function (m) { return keepLangs(m.subs).length > 0; });
  series = series.map(seriesInLangs).filter(function (s) { return s.episodes.length > 0; });

  var html = movies.map(movieCard).join('') + series.map(seriesCard).join('');
  document.getElementById('list').innerHTML = html || '<div class="empty">Nada por aqui.</div>';

  var shown = movies.length + series.length;
  var hidden = matched - shown;
  document.getElementById('count').textContent = shown + ' resultado(s)' +
    (hidden > 0 ? ' · ' + hidden + ' sin los idiomas elegidos' : '');
  document.getElementById('all-btn').href = '/download-all' + langsQuery('?');
}

document.getElementById('q').addEventListener('input', render);

// Delegated, because the list is rebuilt from scratch on every keystroke and filter change. One
// open language at a time per title: two lists of variants side by side is the row-full-of-chips
// mess this replaced.
document.getElementById('list').addEventListener('click', function (e) {
  var btn = e.target.closest('button.chip[data-lang]');
  if (!btn) return;
  var scope = btn.closest('.subs');
  var panel = scope.querySelector('.variants[data-lang="' + btn.dataset.lang + '"]');
  var opening = !panel.classList.contains('open');
  scope.querySelectorAll('.variants.open').forEach(function (p) { p.classList.remove('open'); });
  scope.querySelectorAll('.chip.open').forEach(function (b) { b.classList.remove('open'); });
  if (opening) {
    panel.classList.add('open');
    btn.classList.add('open');
  }
});

// Type anywhere and the search box takes it. autofocus only covers the moment the page loads, and
// the first thing you do here is always search, so a click on the field should never be required.
// Modifier combos are left alone so browser shortcuts still work.
document.addEventListener('keydown', function (e) {
  var q = document.getElementById('q');
  if (e.target === q || e.ctrlKey || e.metaKey || e.altKey) return;
  if (e.key === 'Escape') { q.value = ''; render(); q.blur(); return; }
  if (e.key.length !== 1) return;   // ignore Tab, arrows, F-keys and friends
  q.focus();
});
document.querySelectorAll('.tab[data-filter]').forEach(function (btn) {
  btn.addEventListener('click', function () {
    document.querySelectorAll('.tab[data-filter]').forEach(function (b) { b.classList.remove('active'); });
    btn.classList.add('active');
    filter = btn.dataset.filter;
    render();
  });
});

document.getElementById('langs-btn').addEventListener('click', function () {
  var panel = document.getElementById('langs');
  panel.classList.toggle('open');
  this.classList.toggle('active', panel.classList.contains('open'));
});

document.getElementById('langs').addEventListener('click', function (e) {
  var btn = e.target.closest('button');
  if (!btn) return;
  if (btn.dataset.preset === 'all') {
    langs.clear();
  } else if (btn.dataset.preset === 'default') {
    langs = new Set(DEFAULT_LANGS);
  } else if (btn.dataset.lang) {
    if (langs.has(btn.dataset.lang)) langs.delete(btn.dataset.lang);
    else langs.add(btn.dataset.lang);
  } else {
    return;
  }
  saveLangs();
  renderLangs();
  render();
});

fetch('/api/items')
  .then(function (r) { return r.json(); })
  .then(function (json) { data = json; renderLangs(); render(); })
  .catch(function () {
    document.getElementById('list').innerHTML = '<div class="empty">No se pudo cargar. &iquest;Esta Jellyfin arriba?</div>';
  });
</script>
</body>
</html>`
