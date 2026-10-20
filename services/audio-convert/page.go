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

// A speaker with an arrow turning into it. Filled shapes, since Homepage draws it at 34 px.
const iconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
  <rect width="64" height="64" rx="14" fill="#1c2321"/>
  <path fill="#c9a764" d="M12 26h8l11-9v30l-11-9h-8z"/>
  <path d="M38 24a11 11 0 0 1 0 16M43 18a19 19 0 0 1 0 28" stroke="#c9a764" stroke-width="4.5" stroke-linecap="round" fill="none"/>
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
    color-scheme: light dark;
    --paper:#eef0ee; --ink:#1c2321; --quiet:#6b7571; --hair:#d5dad7; --brass:#9a7b3f; --brass-ink:#fbf8f1;
    --ok:#3f7a5a; --bad:#a6453a; --warn:#8a6420;
    --serif:"Iowan Old Style", "Palatino Linotype", Palatino, "Book Antiqua", ui-serif, Georgia, serif;
    --sans:-apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
  }
  @media (prefers-color-scheme: dark) {
    :root { --paper:#151a19; --ink:#e6eae7; --quiet:#8e9893; --hair:#2a322f; --brass:#c9a764; --brass-ink:#151a19;
            --ok:#7fbf99; --bad:#e08a7e; --warn:#d9b26a; }
  }
  * { box-sizing:border-box; }
  body { margin:0; background:var(--paper); color:var(--ink); font:16px/1.55 var(--sans);
         font-variant-numeric:tabular-nums; -webkit-font-smoothing:antialiased; }
  .page { max-width:40rem; margin:0 auto; padding:4.5rem 1.5rem 4rem; }

  .brand { display:flex; align-items:center; gap:.6rem; color:var(--quiet); font-size:.85rem; margin-bottom:3rem; }
  .brand img { width:22px; height:22px; border-radius:5px; }

  .now { font:400 2.3rem/1.15 var(--serif); letter-spacing:-.01em; margin:0; text-wrap:balance; }
  .now-detail { color:var(--quiet); margin:.9rem 0 0; font-size:.95rem; }
  .line { height:2px; background:var(--hair); margin:1.6rem 0 0; overflow:hidden; }
  .line i { display:block; height:100%; width:0; background:var(--brass); transition:width 1s linear; }
  .line[hidden] { display:none; }
  .facts { color:var(--quiet); font-size:.82rem; margin:1.6rem 0 0; }
  .notice { color:var(--warn); font-size:.88rem; margin:1rem 0 0; }
  .notice:empty { display:none; }

  section { margin-top:4rem; }
  h2 { font:italic 400 1.15rem/1.3 var(--serif); color:var(--quiet); margin:0 0 1.2rem; }

  .item { padding:1.4rem 0; border-top:1px solid var(--hair); }
  .item:last-child { border-bottom:1px solid var(--hair); }
  .item.off { color:var(--quiet); }
  .item .name { font:400 1.35rem/1.25 var(--serif); margin:0; word-break:break-word; }
  .item .why { color:var(--quiet); font-size:.85rem; margin:.3rem 0 0; }
  .item .plan { margin:.8rem 0 0; font-size:.95rem; max-width:34rem; }
  .item .cost { color:var(--quiet); font-size:.85rem; margin:.5rem 0 0; }
  .item .row { display:flex; align-items:center; gap:1rem; flex-wrap:wrap; margin-top:1rem; }
  .state { font-size:.85rem; color:var(--quiet); }
  .state.ok { color:var(--ok); } .state.bad { color:var(--bad); } .state.warn { color:var(--warn); }
  .item .line { margin-top:1rem; }

  button { font:500 .9rem/1 var(--sans); cursor:pointer; border-radius:999px; padding:.65rem 1.25rem; }
  .go { background:var(--brass); color:var(--brass-ink); border:1px solid var(--brass); }
  .go:hover { filter:brightness(1.06); }
  .plain { background:transparent; color:var(--ink); border:1px solid var(--hair); }
  .plain:hover { border-color:var(--quiet); }
  button:focus-visible { outline:2px solid var(--brass); outline-offset:3px; }
  button[disabled] { opacity:.4; cursor:default; filter:none; }
  .push { margin-left:auto; }

  .empty { color:var(--quiet); font-size:.95rem; margin:0; }
  .h { display:flex; justify-content:space-between; gap:1rem; padding:.85rem 0; border-top:1px solid var(--hair); font-size:.9rem; }
  .h:last-child { border-bottom:1px solid var(--hair); }
  .h .sub { color:var(--quiet); font-size:.82rem; margin-top:.15rem; }
  .h .res { white-space:nowrap; font-size:.85rem; }

  footer { margin-top:4.5rem; color:var(--quiet); font-size:.82rem; max-width:34rem; }
  code { font-size:.8rem; }
  @media (max-width:520px) { .page { padding-top:2.5rem; } .now { font-size:1.85rem; } }
  @media (prefers-reduced-motion: reduce) { .line i { transition:none; } }
</style>
</head>
<body>
<div class="page">
  <div class="brand"><img src="/icon.svg" alt="">Audio convert</div>

  <header>
    <h1 class="now" id="now">Checking the library</h1>
    <p class="now-detail" id="now-detail"></p>
    <div class="line" id="now-line" hidden><i></i></div>
    <p class="facts" id="facts"></p>
    <p class="notice" id="msg"></p>
  </header>

  <section>
    <h2>Waiting for a new audio track</h2>
    <div id="list"></div>
  </section>

  <section>
    <h2>Converted</h2>
    <div id="history"></div>
  </section>

  <footer>
    Each conversion runs <code>scripts/sync/audio-compat.py --file</code>, the same script as the 01:30
    pass. It puts an AC3 track first, keeps every other track, and leaves the previous file beside it as
    <code>.original</code>. One conversion runs at a time, and the queue waits while the nightly pass works.
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
  var m = Math.floor(seconds / 60), s = seconds % 60;
  if (m < 60) return m + " min" + (s && m < 10 ? " " + s + " s" : "");
  return Math.floor(m / 60) + " h " + (m % 60) + " min";
}

function when(epoch) {
  return new Date(epoch * 1000).toLocaleString("en-GB", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

// Plex calls DTS dca, as audio-compat's FFPROBE_NAMES knows.
function codecName(codec) { return ({ dca: "dts" }[(codec || "?").toLowerCase()] || codec || "?").toUpperCase(); }

function channels(n) { return { 1: "mono", 2: "stereo", 6: "5.1", 8: "7.1" }[n] || (n + " channels"); }

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

function planText(p) {
  return "Add " + p.target_codec.toUpperCase() + " " + channels(p.target_channels) + " (" + p.target_bitrate_kbps +
    " kb/s) from the " + trackName(p.source) + " track" + (p.source.played ? ", the one the client played" :
    p.source["default"] ? ", the default one" : "") + ". Every track is kept.";
}

function el(tag, cls, text) {
  var e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function line(pct) {
  var bar = el("div", "line"), fill = el("i");
  fill.style.width = pct.toFixed(1) + "%";
  bar.appendChild(fill);
  return bar;
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

function item(it, state) {
  var box = el("article", "item" + (it.actionable ? "" : " off"));
  box.appendChild(el("h3", "name", it.title || it.library_path || it.path));
  var what = it.cause === "codec" ? " refused " + codecName(it.codec) : " had its " + codecName(it.codec) + " re-encoded";
  box.appendChild(el("p", "why", it.client + " in " + appName(it.app) + what + ", " + when(it.seen)));

  if (!it.actionable) {
    box.appendChild(el("p", "plan", it.reason ? it.reason.charAt(0).toUpperCase() + it.reason.slice(1) + "." : ""));
    return box;
  }

  box.appendChild(el("p", "plan", it.plan ? planText(it.plan) :
    "The server did not list the tracks, so the script will use the default one."));

  var pending = it.status !== "done" && it.status !== "missing";
  var running = it.status === "converting" && state.running;
  if (pending && !running) {
    var cost = [];
    if (it.plan) cost.push(human(it.plan.extra_bytes) + " for the new track");
    if (it.exists) cost.push(human(it.size) + " for the .original until it is deleted");
    if (it.duration) cost.push("about " + clock(it.eta) + " for " + clock(it.duration) + " of film");
    if (cost.length) box.appendChild(el("p", "cost", cost.join(", ")));
  }

  if (running) box.appendChild(line(progress(state)));

  var row = el("div", "row");
  if (running) {
    var job = state.running.job;
    row.appendChild(el("span", "state", Math.round(progress(state)) + "%, " + clock(state.running.eta) +
      " left at " + job.speed.toFixed(0) + "× the film's speed"));
  }
  if (it.links > 1 && pending) row.appendChild(el("span", "state warn", "Still seeding"));
  if (it.exists && !it.enough_space && pending) row.appendChild(el("span", "state bad", "Not enough free space"));

  var button;
  switch (it.status) {
    case "ready":
      button = el("button", "go push", "Convert");
      button.disabled = !it.enough_space;
      button.onclick = function () { play(it, button); };
      break;
    case "queued":
      row.appendChild(el("span", "state", "Queued"));
      button = el("button", "plain push", "Remove from queue");
      button.onclick = function () { post("/api/cancel", { library_path: it.library_path }).then(refresh); };
      break;
    case "done":
      row.appendChild(el("span", "state ok", "Converted " + (it.outcome.mode === "nightly" ? "by the nightly pass " : "") +
        "on " + when(it.outcome.time)));
      break;
    case "failed":
      row.appendChild(el("span", "state bad", "Failed: " + it.outcome.reason));
      button = el("button", "plain push", "Try again");
      button.disabled = !it.enough_space;
      button.onclick = function () { play(it, button); };
      break;
    case "missing":
      row.appendChild(el("span", "state", "The file is gone, probably replaced"));
      break;
  }
  if (button) row.appendChild(button);
  if (row.childNodes.length) box.appendChild(row);
  return box;
}

function historyRow(h) {
  var row = el("div", "h"), left = el("div");
  left.appendChild(el("div", "", h.name));
  var bits = [when(h.time), h.mode === "nightly" ? "nightly pass" : "from this page"];
  if (h.seconds) bits.push("took " + clock(h.seconds));
  if (h.speed) bits.push(h.speed.toFixed(0) + "× the film's speed");
  if (h.reason) bits.push(h.reason);
  left.appendChild(el("div", "sub", bits.join(", ")));
  row.appendChild(left);
  var ok = h.result === "done";
  row.appendChild(el("span", "res state " + (ok ? "ok" : "bad"), ok ? "Converted" : "Failed"));
  return row;
}

function headline(d) {
  var now = document.getElementById("now"), detail = document.getElementById("now-detail");
  var bar = document.getElementById("now-line");
  var ready = (d.items || []).filter(function (it) { return it.actionable && it.status === "ready"; });
  bar.hidden = true;
  detail.textContent = "";
  if (d.running) {
    var job = d.running.job, pct = progress(d);
    now.textContent = "Converting " + (d.running.title || job.title || "a file") + ", " + Math.round(pct) + "%";
    detail.textContent = clock(d.running.eta) + " left.";
    bar.hidden = false;
    bar.firstChild.style.width = pct.toFixed(1) + "%";
  } else if (d.holder) {
    var file = (d.holder.file || "").split("/").pop();
    now.textContent = d.holder.mode === "nightly" ? "The nightly pass is working" : "Another conversion is running";
    detail.textContent = (file ? "It is on " + file + ". " : "") + "The queue starts when it finishes.";
  } else if (ready.length) {
    now.textContent = ready.length === 1 ? "One film can play without re-encoding" : ready.length + " films can play without re-encoding";
    detail.textContent = "Each needs an audio track the TV reads. Convert it below.";
  } else {
    now.textContent = "Every file plays as it is";
    detail.textContent = "When a client has its audio re-encoded, the file appears here with a plan to fix it.";
  }
}

var timer = null;

function refresh() {
  return fetch("/api/state").then(function (r) { return r.json(); }).then(function (d) {
    if (d.error) say(d.error);
    headline(d);

    var facts = [human(d.free) + " free, " + human(d.minFree) + " always kept back",
                 "conversions run at about " + d.speed.toFixed(0) + "× the film's speed"];
    if (d.queue && d.queue.length) facts.push(d.queue.length + " queued");
    document.getElementById("facts").textContent = facts.join(", ") + ".";

    var list = document.getElementById("list");
    list.textContent = "";
    if (!d.items || !d.items.length) list.appendChild(el("p", "empty", "Nothing is waiting."));
    else d.items.forEach(function (it) { list.appendChild(item(it, d)); });

    var hist = document.getElementById("history");
    hist.textContent = "";
    if (!d.history || !d.history.length) hist.appendChild(el("p", "empty", "Nothing has been converted yet."));
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
