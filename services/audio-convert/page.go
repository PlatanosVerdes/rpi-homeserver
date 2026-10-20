package main

import (
	"log"
	"net/http"
)

func render(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(pageHTML)); err != nil {
		log.Printf("audio-convert: render failed: %v", err)
	}
}

func icon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(iconSVG))
}

// A waveform whose bars light up from left to right: the audio before and after the conversion.
// Thick rounded bars and no detail finer than 4 units, since Homepage draws it at 34 px.
const iconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#1f2130"/><stop offset="1" stop-color="#121318"/></linearGradient>
    <linearGradient id="lit" gradientUnits="userSpaceOnUse" x1="34" y1="0" x2="54" y2="0"><stop offset="0" stop-color="#7c84f3"/><stop offset="1" stop-color="#c084fc"/></linearGradient>
  </defs>
  <rect width="64" height="64" rx="15" fill="url(#bg)"/>
  <rect x="0.5" y="0.5" width="63" height="63" rx="14.5" fill="none" stroke="#ffffff" stroke-opacity=".08"/>
  <g stroke-linecap="round" stroke-width="5">
    <g stroke="#4a4e5a">
      <path d="M12 28v8M20 22v20M28 16v32"/>
    </g>
    <g stroke="url(#lit)">
      <path d="M36 12v40M44 20v24M52 26v12"/>
    </g>
  </g>
</svg>`

const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Audio convert</title>
<link rel="icon" href="/icon.svg">
<style>
  :root {
    color-scheme: dark;
    --bg:#0f1012; --panel:#15161a; --hover:#1b1c21; --line:#24262c; --ink:#e4e5e9; --dim:#8a8f99; --faint:#5b5f69;
    --accent:#7c84f3; --ok:#4cb782; --bad:#eb5757; --warn:#f2c94c;
  }
  * { box-sizing:border-box; }
  body { margin:0; background:var(--bg); color:var(--ink);
         font:13px/1.45 Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
         font-variant-numeric:tabular-nums; -webkit-font-smoothing:antialiased; }
  .page { max-width:60rem; margin:0 auto; padding:1.25rem 1rem 3rem; }

  .top { display:flex; align-items:center; gap:.6rem; height:2.5rem; }
  .top img { width:18px; height:18px; border-radius:4px; }
  .top h1 { font-size:13px; font-weight:600; margin:0; }
  .top .facts { margin-left:auto; color:var(--dim); display:flex; gap:1rem; }
  #msg { color:var(--warn); margin:.25rem 0 0; }
  #msg:empty { display:none; }

  .group { margin-top:1.25rem; border:1px solid var(--line); border-radius:8px; background:var(--panel); overflow:hidden; }
  .group h2 { margin:0; padding:.55rem .85rem; font-size:12px; font-weight:500; color:var(--dim);
              border-bottom:1px solid var(--line); display:flex; gap:.5rem; }
  .group h2 .n { color:var(--faint); }
  .empty { color:var(--faint); padding:.75rem .85rem; margin:0; }

  .row { display:grid; grid-template-columns:14px minmax(0,1fr) 9.5rem 5.5rem 5.5rem 6rem; align-items:center;
         gap:.75rem; min-height:2.6rem; padding:0 .85rem; border-top:1px solid var(--line); cursor:default; }
  #list > .row:first-child, #history > .row:first-child { border-top:0; }
  .row.click { cursor:pointer; }
  .row.click:hover { background:var(--hover); }
  .row.off { color:var(--dim); }
  .dot { width:8px; height:8px; border-radius:50%; background:var(--accent); justify-self:center; }
  .dot.idle { background:transparent; border:1.5px solid var(--faint); width:9px; height:9px; }
  .dot.run { background:conic-gradient(var(--accent) var(--p,0%), var(--line) 0); width:11px; height:11px; }
  .dot.ok { background:var(--ok); } .dot.bad { background:var(--bad); } .dot.warn { background:var(--warn); }
  .title { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .title small { color:var(--faint); margin-left:.5rem; }
  .c { color:var(--dim); white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
  .c.r { text-align:right; }
  .span2 { grid-column:4 / 6; }
  .span3 { grid-column:3 / 6; }
  .span4 { grid-column:3 / 7; }

  .meta { display:contents; }
  .bar { height:4px; background:var(--line); border-radius:2px; overflow:hidden; }
  .bar i { display:block; height:100%; background:var(--accent); transition:width 1s linear; }

  button { justify-self:end; font:500 12px/1 inherit; font-family:inherit; color:var(--ink); background:var(--hover);
           border:1px solid var(--line); border-radius:6px; padding:.4rem .7rem; cursor:pointer; }
  button:hover { border-color:var(--faint); }
  button.primary { background:var(--accent); border-color:var(--accent); color:#fff; }
  button.primary:hover { filter:brightness(1.08); }
  button:focus-visible { outline:2px solid var(--accent); outline-offset:2px; }
  button[disabled] { opacity:.4; cursor:default; filter:none; }

  .more { display:none; padding:.1rem .85rem .8rem calc(.85rem + 14px + .75rem); color:var(--dim); line-height:1.6; }
  .more.open { display:block; }
  .more b { color:var(--ink); font-weight:500; }

  footer { margin-top:1.5rem; color:var(--faint); font-size:12px; max-width:44rem; }
  code { font:11px ui-monospace, SFMono-Regular, Menlo, monospace; color:var(--dim); }

  @media (max-width:700px) {
    .row { grid-template-columns:14px minmax(0,1fr) auto; row-gap:.15rem; padding:.6rem .85rem; }
    .row > .dot { grid-row:1 / span 2; align-self:start; margin-top:.35rem; }
    .meta { display:flex; flex-wrap:wrap; gap:.15rem .9rem; grid-column:2; min-width:0; }
    .meta .bar { flex:1 0 100%; margin:.3rem 0 .1rem; }
    .meta .c { text-align:left; }
    .row > button { grid-column:3; grid-row:1 / span 2; }
    .more { padding-left:calc(.85rem + 14px + .75rem); }
    .top .facts { display:none; }
  }
  @media (prefers-reduced-motion: reduce) { .bar i { transition:none; } }
</style>
</head>
<body>
<div class="page">
  <div class="top">
    <img src="/icon.svg" alt="">
    <h1>Audio convert</h1>
    <div class="facts" id="facts"></div>
  </div>
  <p id="msg"></p>

  <div class="group">
    <h2>Files <span class="n" id="count"></span></h2>
    <div id="list"></div>
  </div>

  <div class="group">
    <h2>History</h2>
    <div id="history"></div>
  </div>

  <footer>
    Runs <code>audio-compat.py --file</code>, the same script as the 01:30 pass: an AC3 track goes first, every
    other track stays, and the previous file is kept beside it as <code>.original</code>. One at a time; the
    queue waits while the nightly pass works. Click a file for its details.
  </footer>
</div>

<script>
function human(bytes) {
  if (!bytes) return "0 B";
  var units = ["B", "KB", "MB", "GB", "TB"], i = 0, n = bytes;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return (n >= 10 || i === 0 ? Math.round(n) : n.toFixed(1)) + " " + units[i];
}

function clock(seconds) {
  seconds = Math.max(0, Math.round(seconds));
  if (seconds < 60) return seconds + " s";
  var m = Math.floor(seconds / 60);
  if (m < 60) return m + " min";
  return Math.floor(m / 60) + " h " + (m % 60) + " min";
}

function when(epoch) {
  return new Date(epoch * 1000).toLocaleString("en-GB", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

// Plex calls DTS dca, as audio-compat's FFPROBE_NAMES knows.
function codecName(codec) { return ({ dca: "dts" }[(codec || "?").toLowerCase()] || codec || "?").toUpperCase(); }

function channels(n) { return { 1: "mono", 2: "stereo", 6: "5.1", 8: "7.1" }[n] || (n + " ch"); }

// The servers report ISO 639-2 codes, which Intl.DisplayNames does not take.
var LANGUAGES = { eng: "English", spa: "Spanish", jpn: "Japanese", fre: "French", fra: "French", ger: "German",
  deu: "German", ita: "Italian", por: "Portuguese", kor: "Korean", chi: "Chinese", zho: "Chinese", hin: "Hindi",
  rus: "Russian", dut: "Dutch", nld: "Dutch" };

function language(code) { return LANGUAGES[(code || "").toLowerCase()] || code; }

function trackName(t) {
  if (t.title) return t.title;
  return [language(t.language), (t.codec || "").toUpperCase(), t.channels ? channels(t.channels) : ""].filter(Boolean).join(" ");
}

function appName(app) { return ({ plex: "Plex", jellyfin: "Jellyfin" })[app] || app; }

function el(tag, cls, text) {
  var e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function say(text) { document.getElementById("msg").textContent = text || ""; }

function post(url, data) {
  var body = new URLSearchParams();
  Object.keys(data).forEach(function (k) { body.set(k, data[k]); });
  return fetch(url, { method: "POST", body: body }).then(function (r) { return r.json(); });
}

function play(it, button) {
  var data = { library_path: it.library_path };
  if (it.links > 1) {
    var ok = confirm((it.title || it.library_path) + " is still seeding (hardlinked to the torrent).\n\n" +
      "Converting it keeps the current file as .original, still linked to the torrent, so the torrent is unaffected. " +
      "But the disk holds both copies: " + human(it.size) + " more until the torrent is removed and .original deleted.\n\n" +
      "Convert it anyway?");
    if (!ok) return;
    data.allow_hardlinked = "1";
  }
  button.disabled = true;
  post("/api/play", data).then(function (d) {
    if (d.error) { say(d.error); button.disabled = false; return; }
    say("");
    refresh();
  });
}

function progress(state) {
  var job = state.running.job;
  return job.duration ? Math.min(100, job.seconds / job.duration * 100) : 0;
}

var opened = {};

function details(it) {
  var more = el("div", "more" + (opened[it.library_path] ? " open" : ""));
  var lines = [];
  var verb = it.cause === "codec" ? " refused " : " had its audio re-encoded from ";
  lines.push([it.client + " in " + appName(it.app) + verb, codecName(it.codec), " on " + when(it.seen) + "."]);
  if (!it.actionable && it.reason) lines.push(["", it.reason.charAt(0).toUpperCase() + it.reason.slice(1) + ".", ""]);
  if (it.plan) {
    var p = it.plan;
    lines.push(["Adds ", p.target_codec.toUpperCase() + " " + channels(p.target_channels) + " at " + p.target_bitrate_kbps + " kb/s",
      " built from the " + trackName(p.source) + " track" + (p.source.played ? ", the one that was playing" :
      p.source["default"] ? ", the default one" : "") + ". Every track is kept."]);
  }
  if (it.actionable && it.exists && it.status !== "done") {
    lines.push(["Disk: ", human((it.plan ? it.plan.extra_bytes : 0) + it.size), " more while the .original is kept" +
      (it.links > 1 ? ". It is still seeding, so converting asks first." : ".")]);
  }
  if (it.status === "failed") lines.push(["Last try failed: ", it.outcome.reason, ""]);
  lines.forEach(function (l) {
    var p = el("div");
    p.appendChild(document.createTextNode(l[0]));
    p.appendChild(el("b", "", l[1]));
    p.appendChild(document.createTextNode(l[2]));
    more.appendChild(p);
  });
  return more;
}

function item(it, state) {
  var frag = document.createDocumentFragment();
  var row = el("div", "row click" + (it.actionable ? "" : " off"));
  var running = it.status === "converting" && state.running;
  var dot = el("span", "dot");
  if (!it.actionable) dot.className = "dot idle";
  else if (running) { dot.className = "dot run"; dot.style.setProperty("--p", progress(state).toFixed(1) + "%"); }
  else if (it.status === "done") dot.className = "dot ok";
  else if (it.status === "failed") dot.className = "dot bad";
  else if (it.links > 1) dot.className = "dot warn";
  row.appendChild(dot);

  var title = el("div", "title", it.title || it.library_path || it.path);
  row.appendChild(title);

  var meta = el("div", "meta");
  row.appendChild(meta);
  if (!it.actionable) {
    meta.appendChild(el("div", "c span4", it.reason_code === "no_direct_play" || it.cause === "no_direct_play"
      ? "Client setting: turn on Force Direct Play" : (it.reason || "Nothing to fix in the file")));
  } else {
    var change = codecName(it.codec) + " → " + (it.plan ? it.plan.target_codec.toUpperCase() + " " + channels(it.plan.target_channels) : "AC3");
    meta.appendChild(el("div", "c", change));
    if (running) {
      var bar = el("div", "bar"), fill = el("i");
      fill.style.width = progress(state).toFixed(1) + "%";
      bar.appendChild(fill);
      meta.appendChild(bar);
      meta.appendChild(el("div", "c r", Math.round(progress(state)) + "%, " + clock(state.running.eta)));
    } else if (it.status === "done") {
      meta.appendChild(el("div", "c span3", "Converted " + when(it.outcome.time) + (it.outcome.mode === "nightly" ? " by the nightly pass" : "")));
    } else if (it.status === "missing") {
      meta.appendChild(el("div", "c span3", "File replaced since"));
    } else {
      meta.appendChild(el("div", "c r", it.plan ? "+" + human(it.plan.extra_bytes) : ""));
      meta.appendChild(el("div", "c r", it.status === "queued" ? "queued" :
        it.status === "failed" ? "failed" : it.duration ? "~" + clock(it.eta) : ""));
    }
  }

  var button = null;
  if (it.actionable && (it.status === "ready" || it.status === "failed")) {
    button = el("button", it.status === "ready" ? "primary" : "", it.status === "ready" ? "Convert" : "Retry");
    button.disabled = !it.enough_space;
    if (!it.enough_space) button.title = "Not enough free space";
    button.onclick = function (e) { e.stopPropagation(); play(it, button); };
  } else if (it.status === "queued") {
    button = el("button", "", "Remove");
    button.onclick = function (e) { e.stopPropagation(); post("/api/cancel", { library_path: it.library_path }).then(refresh); };
  }
  if (button) row.appendChild(button);
  else if (it.actionable) row.appendChild(el("span"));

  var more = details(it);
  row.onclick = function () {
    opened[it.library_path] = !opened[it.library_path];
    more.classList.toggle("open", opened[it.library_path]);
  };
  frag.appendChild(row);
  frag.appendChild(more);
  return frag;
}

function historyRow(h) {
  var ok = h.result === "done";
  var row = el("div", "row" + (ok ? "" : " off"));
  row.appendChild(el("span", "dot " + (ok ? "ok" : "bad")));
  row.appendChild(el("div", "title", h.name));
  var meta = el("div", "meta");
  row.appendChild(meta);
  meta.appendChild(el("div", "c", h.mode === "nightly" ? "nightly pass" : "from this page"));
  if (ok) {
    meta.appendChild(el("div", "c r", h.seconds ? clock(h.seconds) : ""));
    meta.appendChild(el("div", "c r", h.speed ? h.speed.toFixed(0) + "×" : ""));
  } else {
    meta.appendChild(el("div", "c span2", h.reason || "failed"));
  }
  meta.appendChild(el("div", "c r", when(h.time)));
  return row;
}

var timer = null;

function refresh() {
  return fetch("/api/state").then(function (r) { return r.json(); }).then(function (d) {
    if (d.error) say(d.error);
    var facts = document.getElementById("facts");
    facts.textContent = "";
    if (d.holder && !d.running) {
      var file = (d.holder.file || "").split("/").pop();
      facts.appendChild(el("span", "", (d.holder.mode === "nightly" ? "Nightly pass on " : "Busy with ") + (file || "the library")));
    }
    if (d.queue && d.queue.length) facts.appendChild(el("span", "", d.queue.length + " queued"));
    facts.appendChild(el("span", "", human(d.free) + " free"));
    facts.appendChild(el("span", "", d.speed.toFixed(0) + "× realtime"));

    var list = document.getElementById("list"), items = d.items || [];
    list.textContent = "";
    var todo = items.filter(function (it) { return it.actionable && it.status !== "done" && it.status !== "missing"; }).length;
    document.getElementById("count").textContent = todo ? todo + " to convert" : "";
    if (!items.length) list.appendChild(el("p", "empty", "Nothing yet. A file shows up here when a client has its audio re-encoded."));
    else items.forEach(function (it) { list.appendChild(item(it, d)); });

    var hist = document.getElementById("history");
    hist.textContent = "";
    if (!d.history || !d.history.length) hist.appendChild(el("p", "empty", "Nothing converted yet."));
    else d.history.forEach(function (h) { hist.appendChild(historyRow(h)); });

    clearTimeout(timer);
    timer = setTimeout(tick, d.running || (d.queue && d.queue.length) ? 1000 : 10000);
  }).catch(function (e) { say(String(e)); clearTimeout(timer); timer = setTimeout(tick, 5000); });
}

function tick() { if (document.hidden) { timer = setTimeout(tick, 2000); return; } refresh(); }

refresh();
</script>
</body>
</html>`
