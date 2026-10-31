package main

import (
	_ "embed"
	"net/http"
	"strings"
)

// The same page in two skins: the early-2000s one at / is the default, the modern one at /modern.
var retroHTML = strings.NewReplacer(
	"</style>", "</style>\n<style>"+retroCSS+"</style>",
	`<a href="/modern" aria-current="true">Modern</a><a href="/">Retro</a>`, `<a href="/modern">Modern</a><a href="/" aria-current="true">Retro</a>`,
).Replace(pageHTML)

func (a *app) index(w http.ResponseWriter, r *http.Request) {
	page := retroHTML
	switch r.URL.Path {
	case "/":
	case "/modern":
		page = pageHTML
	case "/retro":
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(page))
}

func icon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(iconSVG))
}

// iOS ignores SVG icons on the home screen, so it gets this PNG of icon.svg, its background squared
// because iOS rounds the corners itself.
//
//go:generate sh -c "echo 'svg > rect:first-of-type { rx: 0; ry: 0 }' | rsvg-convert --width 180 --height 180 --stylesheet /dev/stdin icon.svg -o apple-touch-icon.png"
//go:embed apple-touch-icon.png
var touchIcon []byte

func appleTouchIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(touchIcon)
}

// Baird's spinning Nipkow disc, the first television that showed a face.
//
//go:embed icon.svg
var iconSVG string

// An explorer: the left column is everything that can go in, with a switch and a count each; the
// right shows what the one picked holds, so what is inside a group is one click away.
const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Baird</title>
<link rel="icon" href="/icon.svg">
<link rel="apple-touch-icon" href="/apple-touch-icon.png">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Barlow:wght@400;500;600&family=Barlow+Semi+Condensed:wght@500;600;700&display=swap" rel="stylesheet">
<style>
  :root {
    --paper:#e8ece6; --panel:#f4f6f2; --ink:#17262a; --muted:#5b6b68; --line:#c3ccc5;
    --on:#1f5c55; --on-ink:#eef4f1; --on-soft:#d3e3dc; --warn:#a63d2b;
    --body:"Barlow", system-ui, sans-serif; --head:"Barlow Semi Condensed", "Barlow", system-ui, sans-serif;
  }
  @media (prefers-color-scheme: dark) {
    :root { --paper:#121816; --panel:#1a2220; --ink:#dde6e1; --muted:#8c9a95; --line:#2c3734;
            --on:#5fb3a3; --on-ink:#0e1513; --on-soft:#1e3631; --warn:#e07a62; }
  }
  * { box-sizing:border-box; }
  body { margin:0; background:var(--paper); color:var(--ink); font:16px/1.5 var(--body); font-variant-numeric:tabular-nums; }
  .wrap { max-width:80rem; margin:0 auto; padding:1.5rem 1rem 4rem; }
  button, input, select { font:inherit; color:inherit; }
  :focus-visible { outline:2px solid var(--on); outline-offset:2px; }
  .hidden { display:none !important; }

  header { display:flex; flex-wrap:wrap; gap:1rem 2rem; align-items:end; justify-content:space-between;
           padding-bottom:1.1rem; border-bottom:2px solid var(--ink); margin-bottom:1.3rem; }
  h1 { font:700 2.4rem/1 var(--head); margin:0; display:flex; gap:.6rem; align-items:center; }
  h1 img { width:2rem; height:2rem; }
  #summary { margin:.45rem 0 0; color:var(--muted); overflow-wrap:anywhere; }
  body { overflow-x:hidden; }
  .look { display:flex; align-items:center; gap:.5rem; font-size:.85rem; color:var(--muted); }
  .look nav { display:inline-flex; border:1px solid var(--line); border-radius:.35rem; overflow:hidden; }
  .look a { padding:.25rem .7rem; color:var(--ink); text-decoration:none; background:var(--panel); }
  .look a[aria-current] { background:var(--ink); color:var(--paper); }

  .layout { display:grid; grid-template-columns:19rem minmax(0, 1fr); gap:2rem; align-items:start; }
  /* A phone has room for one column: the list of everything first, then what was picked, with a way back. */
  #back { display:none; }
  @media (max-width:52rem) {
    .layout { grid-template-columns:minmax(0, 1fr); }
    .layout > nav { position:static !important; max-height:none !important; }
    .layout:not(.detail) > main, .layout.detail > nav { display:none; }
    #back { display:block; margin:0 0 .6rem; padding:.4rem 0; }
    header { align-items:start; gap:.6rem; padding-bottom:.8rem; margin-bottom:.9rem; }
    h1 { font-size:1.8rem; }
    h1 img { width:1.5rem; height:1.5rem; }
    #summary { font-size:.88rem; line-height:1.4; }
    .wrap { padding-top:1rem; }
    #bar .btn { width:100%; }
  }

  .layout > nav { position:sticky; top:1rem; max-height:calc(100vh - 2rem); overflow-y:auto; padding-right:.3rem; }
  #nq { width:100%; }
  #q { width:100%; margin-top:1rem; }
  input[type=url], input[type=text], input[type=search] { background:var(--panel); border:1px solid var(--line);
         border-radius:.35rem; padding:.5rem .65rem; min-width:0; }
  .views { display:flex; gap:.3rem; margin:.6rem 0 0; }
  .navsec { margin-top:1.3rem; }
  .navhead { display:flex; justify-content:space-between; align-items:baseline; gap:.5rem; padding:0 .4rem .3rem;
             border-bottom:1px solid var(--line); margin-bottom:.25rem; }
  .navhead b { font:600 1.05rem var(--head); }
  .navhead span { color:var(--muted); font-size:.8rem; }
  .item { display:flex; align-items:center; gap:.5rem; padding:.18rem .3rem; border-radius:.35rem; }
  .item:hover { background:var(--panel); }
  .item.sel { background:var(--on-soft); }
  .item .pick { flex:1; min-width:0; text-align:left; background:none; border:0; padding:.2rem 0; cursor:pointer;
                display:flex; justify-content:space-between; gap:.5rem; }
  .item .pick span:first-child { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .item .pick small { color:var(--muted); flex:none; }
  .item .new { color:var(--on); font-weight:600; font-size:.75rem; }
  .viewbtn { flex:1; background:var(--panel); border:1px solid var(--line); border-radius:.35rem; padding:.35rem .5rem; cursor:pointer; font-size:.9rem; }
  .viewbtn.sel { background:var(--ink); color:var(--paper); border-color:var(--ink); }
  .more { background:none; border:0; color:var(--on); cursor:pointer; padding:.3rem .4rem; font-weight:500; font-size:.9rem; }

  .switch { width:2.3rem; height:1.35rem; border-radius:1rem; border:0; background:var(--line); position:relative;
            cursor:pointer; flex:none; transition:background .15s; }
  .switch::after { content:""; position:absolute; top:.18rem; left:.18rem; width:1rem; height:1rem; border-radius:50%;
                   background:var(--panel); transition:transform .15s; }
  .switch[aria-checked="true"] { background:var(--on); }
  .switch[aria-checked="true"]::after { transform:translateX(.95rem); }
  .switch[aria-checked="mixed"] { background:var(--on-soft); }

  main h2 { font:600 1.8rem/1.1 var(--head); margin:0; }
  .kind { color:var(--muted); font-size:.9rem; }
  p.kind { margin:.3rem 0 0; max-width:42rem; }
  .label .kind { font:400 .85rem var(--body); }
  .facts { color:var(--muted); margin:.4rem 0 0; }
  .actions { display:flex; flex-wrap:wrap; gap:.5rem; align-items:center; margin:.9rem 0 .4rem; }
  .btn { background:var(--on); color:var(--on-ink); border:0; border-radius:.35rem; padding:.45rem .95rem; font-weight:600; cursor:pointer; }
  .btn:disabled { opacity:.45; cursor:default; }
  .btn.quiet { background:transparent; color:var(--ink); border:1px solid var(--line); font-weight:500; }
  .label { font:600 1rem var(--head); margin:.9rem 0 .4rem; }
  .chips { display:flex; flex-wrap:wrap; gap:.35rem; }
  .chip { background:var(--panel); border:1px solid var(--line); border-radius:2rem; padding:.25rem .75rem; font-size:.86rem; cursor:pointer; }
  .chip small { color:var(--muted); margin-left:.3rem; }
  .chip[aria-pressed="true"] { background:var(--on); border-color:var(--on); color:var(--on-ink); }
  .chip[aria-pressed="true"] small { color:var(--on-ink); opacity:.8; }
  .note { color:var(--muted); font-size:.85rem; margin:.3rem 0 0; }

  #filters { margin-top:.9rem; }
  #bar { position:sticky; top:0; z-index:1; background:var(--paper); display:flex; flex-wrap:wrap; gap:.6rem 1rem;
         align-items:center; justify-content:space-between; padding:.7rem 0; margin-top:.6rem; border-bottom:1px solid var(--line); }
  #bar p { margin:0; }
  #list { margin-top:0; }
  .row { display:flex; gap:.8rem; align-items:center; padding:.5rem .2rem; border-bottom:1px solid var(--line); }
  .row img, .row .mono { width:34px; height:34px; object-fit:contain; border-radius:.3rem; flex:none; background:var(--panel); }
  .row .mono { display:grid; place-items:center; font:700 .8rem var(--head); color:var(--muted); border:1px solid var(--line); }
  .row .meta { flex:1; min-width:0; }
  .row .name { font-weight:600; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .row .sub { color:var(--muted); font-size:.8rem; display:flex; gap:.9rem; flex-wrap:wrap; }
  .row .state { font-weight:600; color:var(--ink); }
  .row.out .name { color:var(--muted); }
  .row.preview { background:var(--on-soft); }
  .row.page .id { font-family:ui-monospace, monospace; font-size:.85rem; }
  #empty { color:var(--muted); padding:2rem 0; text-align:center; }

  dialog { border:1px solid var(--line); border-radius:.6rem; background:var(--panel); color:var(--ink); padding:1.4rem; width:min(32rem, calc(100vw - 2rem)); }
  dialog::backdrop { background:rgba(10,20,18,.45); }
  dialog h3 { font:600 1.35rem var(--head); margin:0 0 .3rem; }
  dialog p { color:var(--muted); margin:0 0 1rem; font-size:.92rem; }
  dialog label { display:block; font-weight:500; font-size:.9rem; margin:.8rem 0 .3rem; }
  dialog input { width:100%; }
  dialog .buttons { display:flex; justify-content:flex-end; gap:.5rem; margin-top:1.3rem; }
  #formerr { color:var(--warn); font-size:.9rem; margin:.8rem 0 0; }
  #toast { position:fixed; left:50%; bottom:1.2rem; transform:translateX(-50%); background:var(--ink); color:var(--paper);
           border-radius:.4rem; padding:.6rem 1rem; font-size:.9rem; }
  @media (prefers-reduced-motion: reduce) { * { transition:none !important; } }
</style>
</head>
<body>
<div class="wrap">
<header>
  <div>
    <h1><img src="/icon.svg" alt="">Baird</h1>
    <p id="summary">Loading…</p>
  </div>
  <div class="look">Look <nav aria-label="Look"><a href="/modern" aria-current="true">Modern</a><a href="/">Retro</a></nav></div>
</header>

<div class="layout">
<nav aria-label="Everything that can go in">
  <input id="nq" type="search" placeholder="Find a group, category or page" autocomplete="off" spellcheck="false" aria-label="Find a group, category or page">
  <div class="views">
    <button class="viewbtn" data-view="in"></button>
    <button class="viewbtn" data-view="out"></button>
    <button class="viewbtn" data-view="all"></button>
  </div>
  <div class="navsec">
    <div class="navhead"><b>Your lists</b><span id="listscount"></span></div>
    <div id="navlists"></div>
  </div>
  <div class="navsec">
    <div class="navhead"><b>Engine catalogue</b><span id="catcount"></span></div>
    <div id="navcats"></div>
    <button class="more" id="morecats"></button>
  </div>
  <div class="navsec">
    <div class="navhead"><b>Web pages</b><button class="more" id="openadd">Add</button></div>
    <div id="navpages"></div>
  </div>
</nav>

<main>
  <button class="more" id="back">‹ Everything</button>
  <div id="head"></div>
  <div id="filters"><div id="fchips"></div><input id="q" type="search" autocomplete="off" spellcheck="false"></div>
  <div id="bar"></div>
  <div id="list"></div>
  <p id="empty" class="hidden"></p>
  <button class="more hidden" id="morelist">Show more</button>
</main>
</div>
</div>

<dialog id="adddialog" aria-labelledby="addtitle">
  <form id="addpage" method="dialog">
    <h3 id="addtitle">Add a web page</h3>
    <p>Baird reads the page's acestream:// links now and every 30 minutes. Each page is one channel, and each of its links a source.</p>
    <label for="f-url">Address</label>
    <input id="f-url" name="url" type="url" placeholder="https://…" required>
    <label for="f-name">Channel name</label>
    <input id="f-name" name="name" type="text" placeholder="Sky Sports F1" required>
    <label for="f-group">Group, optional</label>
    <input id="f-group" name="group" type="text" placeholder="Formula 1">
    <p id="formerr" class="hidden"></p>
    <div class="buttons">
      <button type="button" class="btn quiet" id="canceladd">Cancel</button>
      <button class="btn" id="saveadd">Add page</button>
    </div>
  </form>
</dialog>
<div id="toast" class="hidden" role="status"></div>

<script>
var data = null, sel = { kind: 'in', key: '' }, shown = 150, allCats = false, allCountries = false;
var FLOOD = 50;
var $ = function (id) { return document.getElementById(id); };
var esc = function (s) { var d = document.createElement('div'); d.textContent = s == null ? '' : s; return d.innerHTML; };
var norm = function (s) { return (s || '').toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, ''); };
var regions = (function () { try { return new Intl.DisplayNames(['en'], { type: 'region' }); } catch (e) { return null; } })();
var ALL = '*';
function catName(c) { if (c === ALL) return 'All categories'; c = c.replace(/_/g, ' '); return c.charAt(0).toUpperCase() + c.slice(1); }
function inCategory(cats, k) { return k === ALL || cats.indexOf(k) >= 0; }
function countryName(c) {
  if (c === '-') return 'No country';
  if (regions && /^[a-z]{2}$/.test(c)) { try { var n = regions.of(c.toUpperCase()); if (n) return n; } catch (e) {} }
  return c.toUpperCase();
}
function countriesOf(c) { return c.countries && c.countries.length ? c.countries : ['-']; }
function plural(n, one, many) { return n + ' ' + (n === 1 ? one : many); }
function host(u) { try { return new URL(u).hostname.replace(/^www\./, ''); } catch (e) { return u; } }


function toast(msg) {
  var t = $('toast'); t.textContent = msg; t.classList.remove('hidden');
  clearTimeout(toast.timer); toast.timer = setTimeout(function () { t.classList.add('hidden'); }, 2500);
}

function load() {
  return fetch('/api/state').then(function (r) { return r.json(); }).then(function (d) { data = d; draw(); });
}

// Every channel in one shape. state: in, preview (would come in with its category), out.
function channels() {
  var cs = data.countries || [], cats = data.categories || [];
  var lists = data.lists.map(function (c) {
    return { kind: 'list', name: c.name, on: c.shown, logo: c.logo, live: c.sources, ruled: true, group: c.group || '',
             sub: [c.group || 'No group', plural(c.sources, 'source', 'sources'), c.kept ? c.kept + ' dropped upstream, kept a while' : ''] };
  });
  var cat = data.catalogue.map(function (c) {
    var fits = c.available > 0 && (!cs.length || countriesOf(c).some(function (x) { return cs.indexOf(x) >= 0; }));
    return { kind: 'cat', name: c.name, on: c.on, logo: c.icon, live: c.available, ruled: c.byRule, fits: fits,
             categories: c.categories || [], byHand: c.on && !c.byRule,
             sub: [(c.categories || []).map(catName).join(', '), countriesOf(c).map(countryName).join(', '),
                   c.available + ' of ' + c.sources + ' sources live'] };
  });
  return lists.concat(cat);
}

function draw() { summary(); nav(); main(); }

function summary() {
  var fromLists = data.lists.filter(function (c) { return c.shown; }).length;
  var built = data.builtAt && data.builtAt.indexOf('0001') !== 0;
  var t = !built ? 'Building the list…' : data.channels + ' channels and ' + data.sources + ' sources in the list: ' + fromLists +
          ' from your lists, ' + Math.max(0, data.channels - fromLists) + ' added here.';
  if (data.listsErr) t += ' The upstream lists file cannot be read (' + data.listsErr + ').';
  if (data.catalogErr) t += ' The engine is not answering, using the last catalogue.';
  $('summary').textContent = t;
}

function navItem(kind, key, label, on, count, extra) {
  var selected = sel.kind === kind && sel.key === key;
  return '<div class="item' + (selected ? ' sel' : '') + '">' +
    (on === null ? '' : '<button class="switch" role="switch" aria-checked="' + on + '" aria-label="' + esc(label) + ' in the list" data-toggle="' + kind + '" data-key="' + esc(key) + '"></button>') +
    '<button class="pick" data-kind="' + kind + '" data-key="' + esc(key) + '"' + (selected ? ' aria-current="true"' : '') + '><span>' + esc(label) +
    (extra ? ' <em class="new">' + esc(extra) + '</em>' : '') + '</span><small>' + count + '</small></button></div>';
}

function nav() {
  var all = channels(), pagesIn = (data.pages || []).filter(function (p) { return (p.ids || []).length; }).length;
  var views = { in: 'In the list ' + (all.filter(function (c) { return c.on; }).length + pagesIn),
                out: 'Hidden ' + all.filter(function (c) { return !c.on && c.ruled; }).length,
                all: 'All ' + all.length };
  document.querySelectorAll('.viewbtn').forEach(function (b) {
    b.textContent = views[b.dataset.view];
    b.classList.toggle('sel', sel.kind === b.dataset.view);
  });
  var nq = norm($('nq').value.trim()), match = function (label) { return !nq || norm(label).indexOf(nq) >= 0; };

  var hidden = new Set(data.hiddenGroups || []), fresh = new Set(data.newGroups || []), groups = {};
  data.lists.forEach(function (c) {
    var g = groups[c.group || ''] = groups[c.group || ''] || { total: 0, on: 0 };
    g.total++; if (c.shown) g.on++;
  });
  var names = Object.keys(groups).sort(function (a, b) { return a.localeCompare(b); }).filter(function (g) { return match(g || 'No group'); });
  $('navlists').innerHTML = names.map(function (g) {
    var x = groups[g], state = hidden.has(g) ? (x.on ? 'mixed' : 'false') : (x.on === x.total ? 'true' : 'mixed');
    return navItem('list', g, g || 'No group', state, x.on + '/' + x.total, fresh.has(g) ? 'new' : '');
  }).join('') || (nq ? '<p class="note">No group matches.</p>' : '');
  var shownLists = data.lists.filter(function (c) { return c.shown; }).length;
  $('listscount').textContent = shownLists + '/' + data.lists.length;

  var picked = new Set(data.categories || []), cats = {};
  all.forEach(function (c) {
    if (c.kind !== 'cat') return;
    c.categories.forEach(function (k) { var x = cats[k] = cats[k] || { total: 0, on: 0 }; x.total++; if (c.on) x.on++; });
  });
  var keys = Object.keys(cats).sort(function (a, b) { return cats[b].total - cats[a].total; });
  keys = keys.filter(function (k) { return match(catName(k)); });
  var main = nq ? keys : keys.filter(function (k) { return cats[k].total >= 3 || picked.has(k) || (sel.kind === 'cat' && sel.key === k); });
  var catOn = all.filter(function (c) { return c.kind === 'cat' && c.on; }).length;
  var allItem = match(catName(ALL)) ? navItem('cat', ALL, catName(ALL), picked.has(ALL) ? 'true' : (catOn ? 'mixed' : 'false'), catOn + '/' + data.catalogue.length) : '';
  $('navcats').innerHTML = allItem + (allCats ? keys : main).map(function (k) {
    return navItem('cat', k, catName(k), picked.has(k) ? 'true' : (cats[k].on ? 'mixed' : 'false'), cats[k].on + '/' + cats[k].total);
  }).join('') || (nq ? '<p class="note">No category matches.</p>' : '');
  var smaller = keys.length - main.length;
  $('morecats').textContent = allCats ? 'Show fewer' : 'Show ' + smaller + ' smaller ones';
  $('morecats').classList.toggle('hidden', smaller === 0);
  $('catcount').textContent = all.filter(function (c) { return c.kind === 'cat' && c.on; }).length + '/' + data.catalogue.length;

  $('navpages').innerHTML = (data.pages || []).filter(function (p) { return match(p.name); }).map(function (p) {
    return navItem('page', p.url, p.name, null, plural((p.ids || []).length, 'source', 'sources'), p.error ? 'unreadable' : '');
  }).join('') || '<p class="note">' + (nq && (data.pages || []).length ? 'No page matches.' : 'None yet. A page with acestream:// links becomes a channel.') + '</p>';
}

// Top to bottom: what this is, the filters, what is left with the action that applies to it, the list.
function main() {
  var q = norm($('q').value.trim()), all = channels(), rows = [], head = '', chips = '', bar = '', action = '';
  var title = '', kind = '';
  if (sel.kind === 'all') {
    rows = all; title = 'All channels'; kind = 'Every channel of your lists and of the engine catalogue, in the list or not.';
    bar = rows.length + ' channels, ' + rows.filter(function (c) { return c.on; }).length + ' in the list.';
  } else if (sel.kind === 'in') {
    rows = all.filter(function (c) { return c.on; }); title = 'In the list'; kind = 'What goes to the player now. Switch a channel off to hide it.';
    bar = plural(rows.length, 'channel', 'channels') + ' in the list.';
  } else if (sel.kind === 'out') {
    rows = all.filter(function (c) { return !c.on && c.ruled; }); title = 'Hidden';
    kind = 'Channels a switched-on group or category holds and you switched off. Switch one on to bring it back.';
    bar = plural(rows.length, 'channel', 'channels') + ' hidden.';
  } else if (sel.kind === 'list') {
    rows = all.filter(function (c) { return c.kind === 'list' && c.group === sel.key; });
    var inG = rows.filter(function (c) { return c.on; }).length, hid = (data.hiddenGroups || []).indexOf(sel.key) >= 0;
    title = sel.key || 'No group'; kind = 'A group of your lists. ' + (hid ? 'Off: channels that join it later stay out too.' : 'On: channels that join it later come in.');
    bar = inG + ' of ' + plural(rows.length, 'channel', 'channels') + ' in the list.';
    action = '<button class="btn" data-group-on="' + hid + '">' + (hid ? 'Add the group' : 'Hide the group') + '</button>';
  } else if (sel.kind === 'cat') {
    var on = (data.categories || []).indexOf(sel.key) >= 0, cs = data.countries || [];
    rows = all.filter(function (c) { return c.kind === 'cat' && inCategory(c.categories, sel.key); });
    rows.forEach(function (c) { if (!on && !c.on && c.fits) c.preview = true; });
    var inC = rows.filter(function (c) { return c.on; }).length, coming = rows.filter(function (c) { return c.preview; }).length;
    var where = cs.length ? ' from ' + cs.map(countryName).join(', ') : '';
    title = catName(sel.key);
    kind = (sel.key === ALL ? 'The whole engine catalogue, as one category.' : 'An engine catalogue category.') +
      (on ? ' On: its live channels' + where + ' come in, also ones that show up later.' : ' Off.');
    chips = countryChips();
    bar = on ? inC + ' of ' + plural(rows.length, 'channel', 'channels') + ' in the list.'
             : coming + ' of ' + rows.length + ' would come in: the live ones' + where + '.' + (inC ? ' ' + inC + ' already in.' : '');
    action = on ? '<button class="btn quiet" data-cat-on="false">Remove the category</button>'
                : '<button class="btn" data-cat-on="true"' + (coming ? '' : ' disabled') + '>Add the ' + coming + where + '</button>';
  } else if (sel.kind === 'page') {
    var p = (data.pages || []).filter(function (x) { return x.url === sel.key; })[0];
    if (!p) { sel = { kind: 'in', key: '' }; return main(); }
    title = p.name; kind = 'A web page, read again every 30 minutes' + (p.group ? ', in the group ' + p.group : '') + '.';
    bar = plural((p.ids || []).length, 'source', 'sources') + ' from <a href="' + esc(p.url) + '" target="_blank" rel="noopener">' + esc(host(p.url)) + '</a>. ' +
      (p.error ? '<span style="color:var(--warn)">The last read failed (' + esc(p.error) + '); the sources from before stay.</span>' : 'The last read worked.');
    action = '<button class="btn quiet" data-remove="' + esc(p.url) + '">Remove the page</button>';
    rows = (p.ids || []).map(function (id, i) { return { kind: 'pageid', name: 'Source ' + (i + 1), id: id }; });
  }
  $('head').innerHTML = '<h2>' + esc(title) + '</h2><p class="kind">' + esc(kind) + '</p>';
  $('fchips').innerHTML = chips;
  $('q').placeholder = 'Search in ' + title;
  $('q').setAttribute('aria-label', 'Search in ' + title);
  var total = rows.length;
  if (q) rows = rows.filter(function (c) { return norm(c.name).indexOf(q) >= 0; });
  $('bar').innerHTML = '<p>' + (sel.kind === 'page' ? bar : esc(bar)) + (q ? ' Showing the ' + rows.length + ' that match.' : '') + '</p>' + action;
  rows.sort(function (a, b) {
    return ((b.on || b.preview ? 1 : 0) - (a.on || a.preview ? 1 : 0)) || ((a.preview ? 1 : 0) - (b.preview ? 1 : 0)) ||
           (a.kind === b.kind ? 0 : a.kind === 'list' ? -1 : 1) || ((b.live || 0) - (a.live || 0)) || a.name.localeCompare(b.name);
  });
  $('list').innerHTML = rows.slice(0, shown).map(row).join('');
  $('empty').classList.toggle('hidden', rows.length > 0);
  $('empty').textContent = q ? 'None of the ' + total + ' matches.' : sel.kind === 'out' ? 'Nothing hidden.' : 'The list is empty. Switch a group or a category on, on the left.';
  $('morelist').classList.toggle('hidden', rows.length <= shown);
}

function countryChips() {
  var on = new Set(data.countries || []), counts = {};
  data.catalogue.forEach(function (c) {
    if (!inCategory(c.categories || [], sel.key)) return;
    countriesOf(c).forEach(function (k) { counts[k] = (counts[k] || 0) + 1; });
  });
  var keys = Object.keys(counts).sort(function (a, b) { return counts[b] - counts[a]; });
  var few = keys.filter(function (k, i) { return i < 12 || on.has(k); }), rest = keys.length - few.length;
  return '<div class="label">Countries <span class="kind">(apply to every category)</span></div><div class="chips">' +
    '<button class="chip" data-country="" aria-pressed="' + (on.size === 0) + '">Any country</button>' +
    (allCountries ? keys : few).map(function (k) {
      return '<button class="chip" data-country="' + esc(k) + '" aria-pressed="' + on.has(k) + '">' + esc(countryName(k)) + '<small>' + counts[k] + '</small></button>';
    }).join('') + (rest ? '<button class="more" data-morecountries="1">' + (allCountries ? 'Show fewer' : 'Show ' + rest + ' more countries') + '</button>' : '') + '</div>';
}

function row(c) {
  if (c.kind === 'pageid') {
    return '<div class="row page"><span class="mono" aria-hidden="true">' + c.name.slice(7) + '</span><div class="meta"><div class="name">' +
      esc(c.name) + '</div><div class="sub"><span class="id">' + esc(c.id) + '</span></div></div></div>';
  }
  var src = !c.logo ? '' : /^https:/.test(c.logo) ? c.logo : '/logo?u=' + encodeURIComponent(c.logo);
  var state = c.on ? (c.byHand ? 'added by hand' : '') : c.preview ? 'would come in' : c.ruled ? 'switched off' : '';
  return '<div class="row' + (c.on ? '' : c.preview ? ' preview' : ' out') + '">' +
    (src ? '<img src="' + esc(src) + '" alt="" loading="lazy" data-mono="' + esc(initials(c.name)) + '">'
         : '<span class="mono" aria-hidden="true">' + esc(initials(c.name)) + '</span>') +
    '<div class="meta"><div class="name">' + esc(c.name) + '</div><div class="sub">' +
    (state ? '<span class="state">' + state + '</span>' : '') +
    c.sub.filter(Boolean).map(function (s) { return '<span>' + esc(s) + '</span>'; }).join('') + '</div></div>' +
    '<button class="switch" role="switch" aria-checked="' + c.on + '" aria-label="' + esc(c.name) + ' in the list" data-kind="' +
    c.kind + '" data-name="' + esc(c.name) + '"></button></div>';
}

// "DAZN1" -> "D1", "M. Deportes 2 HD" -> "MD": first letters of the words, digits kept, quality tags dropped.
function initials(name) {
  var words = name.replace(/\[[^\]]*\]|\([^)]*\)/g, ' ').split(/[\s.\-_|]+/)
    .filter(function (w) { return w && !/^(hd|fhd|sd|uhd|4k|tv|\d{3,4}p)$/i.test(w); });
  if (!words.length) return '?';
  if (words.length === 1) { var m = words[0].match(/^(\D)\D*(\d+)$/); return m ? (m[1] + m[2]).toUpperCase() : words[0].slice(0, 2).toUpperCase(); }
  return (words[0][0] + words[1][0]).toUpperCase();
}

// A logo that fails to load becomes the initials, in its place.
document.addEventListener('error', function (e) {
  var img = e.target;
  if (img.tagName !== 'IMG' || !img.dataset.mono) return;
  var m = document.createElement('span'); m.className = 'mono'; m.textContent = img.dataset.mono;
  img.replaceWith(m);
}, true);

function post(path, names, on, msg) {
  return fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ names: names, picked: on }) })
    .then(function (r) { if (!r.ok) throw new Error(r.status); toast(msg); })
    .then(load).then(function () { setTimeout(load, 5000); })
    .catch(function () { toast('Not saved. Reload the page and try again.'); });
}

// How many channels switching a category on, or dropping the country filter, would bring in.
function wouldAdd(cats, countries) {
  return data.catalogue.filter(function (c) {
    if (c.on || !c.available || !(cats.indexOf(ALL) >= 0 || (c.categories || []).some(function (x) { return cats.indexOf(x) >= 0; }))) return false;
    return !countries.length || countriesOf(c).some(function (x) { return countries.indexOf(x) >= 0; });
  }).length;
}

function setCategory(cat, on) {
  var n = on ? wouldAdd([cat], data.countries || []) : 0;
  if (n > FLOOD && !confirm(catName(cat) + ' would add ' + n + ' channels to the list. Go ahead?')) return;
  post('/api/categories', [cat], on, (on ? 'Switched on ' : 'Switched off ') + catName(cat));
}

function setGroup(group, on) {
  post('/api/listgroups', [group], on, (on ? 'Switched on ' : 'Switched off ') + (group || 'No group'));
}

function select(kind, key) {
  sel = { kind: kind, key: key }; shown = 150; $('q').value = '';
  document.querySelector('.layout').classList.add('detail');
  nav(); main(); window.scrollTo({ top: 0 });
}
$('back').addEventListener('click', function () {
  document.querySelector('.layout').classList.remove('detail');
  window.scrollTo({ top: 0 });
});

document.querySelector('.layout > nav').addEventListener('click', function (e) {
  var t = e.target.closest('[data-toggle]');
  if (t) {
    var on = t.getAttribute('aria-checked') !== 'true';
    if (t.dataset.toggle === 'list') setGroup(t.dataset.key, on); else setCategory(t.dataset.key, on);
    return;
  }
  var p = e.target.closest('.pick'); if (p) { select(p.dataset.kind, p.dataset.key); return; }
  var v = e.target.closest('.viewbtn'); if (v) select(v.dataset.view, '');
});
$('morecats').addEventListener('click', function () { allCats = !allCats; nav(); });
$('q').addEventListener('input', function () { shown = 150; main(); });
$('nq').addEventListener('input', nav);
$('morelist').addEventListener('click', function () { shown += 300; main(); });

document.querySelector('main').addEventListener('click', function (e) {
  var b = e.target.closest('button'); if (!b || b.closest('#list')) return;
  if (b.dataset.morecountries) { allCountries = !allCountries; main(); }
  else if (b.dataset.groupOn) setGroup(sel.key, b.dataset.groupOn === 'true');
  else if (b.dataset.catOn) setCategory(sel.key, b.dataset.catOn === 'true');
  else if (b.dataset.remove) {
    if (!confirm('Remove this page and its sources from the list?')) return;
    fetch('/api/pages', { method: 'DELETE', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ url: b.dataset.remove }) })
      .then(function () { toast('Page removed'); sel = { kind: 'in', key: '' }; return load(); });
  } else if (b.dataset.country !== undefined) {
    var k = b.dataset.country;
    if (k === '') {
      var n = wouldAdd(data.categories || [], []) - wouldAdd(data.categories || [], data.countries || []);
      if (n > FLOOD && !confirm('Any country would add ' + n + ' channels to the list. Go ahead?')) return;
      if ((data.countries || []).length) post('/api/countries', data.countries, false, 'Any country');
      return;
    }
    var on = b.getAttribute('aria-pressed') !== 'true';
    post('/api/countries', [k], on, (on ? 'Only ' : 'Removed ') + countryName(k));
  }
});

$('list').addEventListener('click', function (e) {
  var b = e.target.closest('.switch'); if (!b) return;
  var on = b.getAttribute('aria-checked') !== 'true';
  b.setAttribute('aria-checked', on);
  post(b.dataset.kind === 'list' ? '/api/listchannels' : '/api/pick', [b.dataset.name], on, (on ? 'Showing ' : 'Hiding ') + b.dataset.name);
});

var dialog = $('adddialog'), formErr = $('formerr');
$('openadd').addEventListener('click', function () { formErr.classList.add('hidden'); dialog.showModal(); });
$('canceladd').addEventListener('click', function () { dialog.close(); });
$('addpage').addEventListener('submit', function (e) {
  e.preventDefault();
  var f = e.target, btn = $('saveadd');
  btn.disabled = true; btn.textContent = 'Reading the page…'; formErr.classList.add('hidden');
  fetch('/api/pages', { method: 'POST', headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ url: f.url.value.trim(), name: f.name.value.trim(), group: f.group.value.trim() }) })
    .then(function (r) { return r.ok ? null : r.text(); })
    .then(function (err) {
      if (err) {
        formErr.textContent = err.indexOf('no acestream') >= 0 ? 'That page has no acestream:// links in its HTML.'
          : err.indexOf('already') >= 0 ? 'That page is already listed.' : 'Could not read the page: ' + err.trim();
        formErr.classList.remove('hidden'); return;
      }
      var url = f.url.value.trim();
      f.reset(); dialog.close(); toast('Page added'); sel = { kind: 'page', key: url }; return load();
    })
    .catch(function () { formErr.textContent = 'Baird is not answering.'; formErr.classList.remove('hidden'); })
    .finally(function () { btn.disabled = false; btn.textContent = 'Add page'; });
});

load();
</script>
</body>
</html>`
