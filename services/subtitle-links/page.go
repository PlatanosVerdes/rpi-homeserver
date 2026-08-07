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
  .count { color: var(--text-dim); font-size: 0.8rem; margin: 0.9rem 0 0.6rem; }
  .card {
    background: var(--surface); border: 1px solid var(--border); border-radius: 14px;
    padding: 0.85rem 1rem; margin-bottom: 0.6rem;
  }
  .row { display: flex; align-items: center; justify-content: space-between; gap: 0.7rem; }
  .title { font-size: 0.98rem; font-weight: 500; flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; }
  .chips { display: flex; gap: 0.4rem; flex-wrap: wrap; justify-content: flex-end; }
  a.chip {
    background: var(--surface-2); color: var(--accent); text-decoration: none;
    padding: 0.4rem 0.75rem; border-radius: 8px; font-size: 0.82rem; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.03em; white-space: nowrap;
    border: 1px solid transparent; transition: border-color 0.15s;
  }
  a.chip:active { border-color: var(--accent); }
  a.chip.all { color: var(--text); background: var(--accent-dim); text-transform: none; letter-spacing: 0; }
  .series .row { cursor: pointer; }
  .chevron { color: var(--text-dim); transition: transform 0.2s; flex: none; }
  .series.open .chevron { transform: rotate(90deg); }
  .episodes { display: none; margin-top: 0.7rem; padding-top: 0.7rem; border-top: 1px solid var(--border); }
  .series.open .episodes { display: block; }
  .episode { display: flex; align-items: center; justify-content: space-between; gap: 0.7rem; padding: 0.45rem 0; }
  .episode + .episode { border-top: 1px solid var(--border); }
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
      <a class="all-btn" href="/download-all">Descargar todo</a>
    </div>
    <div class="search">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="7"></circle><line x1="21" y1="21" x2="16.65" y2="16.65"></line>
      </svg>
      <input id="q" type="search" placeholder="Buscar pelicula o serie..." autocomplete="off">
    </div>
    <div class="tabs">
      <button class="tab active" data-filter="all">Todo</button>
      <button class="tab" data-filter="movies">Peliculas</button>
      <button class="tab" data-filter="series">Series</button>
    </div>
  </header>
  <div class="count" id="count"></div>
  <div id="list"></div>
</div>
<script>
let data = { movies: [], series: [] };
let filter = 'all';

function norm(s) {
  return (s || '').toLowerCase().normalize('NFD').replace(/[\u0300-\u036f]/g, '');
}

var FLAGS = {
  spa: '\ud83c\uddea\ud83c\uddf8', eng: '\ud83c\uddec\ud83c\udde7', fre: '\ud83c\uddeb\ud83c\uddf7', fra: '\ud83c\uddeb\ud83c\uddf7',
  ger: '\ud83c\udde9\ud83c\uddea', deu: '\ud83c\udde9\ud83c\uddea', ita: '\ud83c\uddee\ud83c\uddf9', por: '\ud83c\uddf5\ud83c\uddf9',
  ara: '\ud83c\uddf8\ud83c\udde6', jpn: '\ud83c\uddef\ud83c\uddf5', kor: '\ud83c\uddf0\ud83c\uddf7', chi: '\ud83c\udde8\ud83c\uddf3',
  zho: '\ud83c\udde8\ud83c\uddf3', rus: '\ud83c\uddf7\ud83c\uddfa', dut: '\ud83c\uddf3\ud83c\uddf1', nld: '\ud83c\uddf3\ud83c\uddf1',
  swe: '\ud83c\uddf8\ud83c\uddea', nor: '\ud83c\uddf3\ud83c\uddf4', dan: '\ud83c\udde9\ud83c\uddf0', fin: '\ud83c\uddeb\ud83c\uddee',
  pol: '\ud83c\uddf5\ud83c\uddf1', tur: '\ud83c\uddf9\ud83c\uddf7', gre: '\ud83c\uddec\ud83c\uddf7', ell: '\ud83c\uddec\ud83c\uddf7',
  heb: '\ud83c\uddee\ud83c\uddf1', hin: '\ud83c\uddee\ud83c\uddf3', cze: '\ud83c\udde8\ud83c\uddff', ces: '\ud83c\udde8\ud83c\uddff',
  hun: '\ud83c\udded\ud83c\uddfa', ukr: '\ud83c\uddfa\ud83c\udde6', vie: '\ud83c\uddfb\ud83c\uddf3', tha: '\ud83c\uddf9\ud83c\udded',
  ind: '\ud83c\uddee\ud83c\udde9'
};

function flag(lang) {
  return FLAGS[lang] || '\ud83c\udf10';
}

function langLabel(lang) {
  return flag(lang) + ' ' + lang.toUpperCase();
}

function chip(href, label, extra, stop) {
  var onclick = stop ? ' onclick="event.stopPropagation()"' : '';
  return '<a class="chip' + (extra ? ' ' + extra : '') + '" href="' + href + '"' + onclick + '>' + label + '</a>';
}

function poster(id) {
  return '<img class="poster" src="/image/' + id + '" loading="lazy" onerror="this.style.visibility=\'hidden\'" alt="">';
}

function movieCard(m) {
  var chips = m.subs.map(function (s) {
    return chip('/download/' + m.id + '?index=' + s.index + '&lang=' + s.lang + '&name=' + encodeURIComponent(m.title), langLabel(s.lang));
  }).join('');
  return '<div class="card"><div class="row">' +
    poster(m.id) +
    '<span class="title">' + m.title + '</span>' +
    '<span class="chips">' + chips + '</span>' +
    '</div></div>';
}

function seriesCard(s) {
  var epRows = s.episodes.map(function (ep) {
    var chips = ep.subs.map(function (sub) {
      var label = 'S' + String(ep.season).padStart(2, '0') + 'E' + String(ep.episode).padStart(2, '0') + ' ' + sub.lang;
      return chip('/download/' + ep.id + '?index=' + sub.index + '&lang=' + sub.lang + '&name=' + encodeURIComponent(s.title + ' ' + label), langLabel(sub.lang));
    }).join('');
    return '<div class="episode">' +
      '<span class="ep-title"><span class="ep-num">S' + String(ep.season).padStart(2, '0') + 'E' + String(ep.episode).padStart(2, '0') + '</span>' + ep.title + '</span>' +
      '<span class="chips">' + chips + '</span>' +
      '</div>';
  }).join('');
  var allHref = '/download-all/' + s.id + '?name=' + encodeURIComponent(s.title);
  return '<div class="card series" data-id="' + s.id + '">' +
    '<div class="row" onclick="toggleSeries(this)">' +
    poster(s.id) +
    '<span class="title">' + s.title + ' <span style="color:var(--text-dim);font-weight:400;">(' + s.episodes.length + ' ep.)</span></span>' +
    '<span class="chips">' + chip(allHref, 'Descargar todos', 'all', true) +
    '<svg class="chevron" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="9 18 15 12 9 6"></polyline></svg>' +
    '</span></div>' +
    '<div class="episodes">' + epRows + '</div>' +
    '</div>';
}

function toggleSeries(rowEl) {
  rowEl.parentElement.classList.toggle('open');
}

function render() {
  var q = norm(document.getElementById('q').value);
  var showMovies = filter === 'all' || filter === 'movies';
  var showSeries = filter === 'all' || filter === 'series';

  var movies = showMovies ? data.movies.filter(function (m) { return norm(m.title).includes(q); }) : [];
  var series = showSeries ? data.series.filter(function (s) { return norm(s.title).includes(q); }) : [];

  var html = movies.map(movieCard).join('') + series.map(seriesCard).join('');
  document.getElementById('list').innerHTML = html || '<div class="empty">Nada por aqui.</div>';
  document.getElementById('count').textContent = (movies.length + series.length) + ' resultado(s)';
}

document.getElementById('q').addEventListener('input', render);
document.querySelectorAll('.tab').forEach(function (btn) {
  btn.addEventListener('click', function () {
    document.querySelectorAll('.tab').forEach(function (b) { b.classList.remove('active'); });
    btn.classList.add('active');
    filter = btn.dataset.filter;
    render();
  });
});

fetch('/api/items')
  .then(function (r) { return r.json(); })
  .then(function (json) { data = json; render(); })
  .catch(function () {
    document.getElementById('list').innerHTML = '<div class="empty">No se pudo cargar. &iquest;Esta Jellyfin arriba?</div>';
  });
</script>
</body>
</html>`
