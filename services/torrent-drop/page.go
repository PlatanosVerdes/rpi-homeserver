package main

import (
	"log"
	"net/http"
)

func render(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(pageHTML)); err != nil {
		log.Printf("torrent-drop: render failed: %v", err)
	}
}

func icon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(iconSVG))
}

const iconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
  <rect width="64" height="64" rx="14" fill="#08201a"/>
  <path d="M32 12 V34" stroke="#34d399" stroke-width="4" fill="none" stroke-linecap="round"/>
  <path d="M22 26 L32 36 L42 26" stroke="#34d399" stroke-width="4" fill="none" stroke-linecap="round" stroke-linejoin="round"/>
  <path d="M14 42 V48 H50 V42" stroke="#34d399" stroke-width="4" fill="none" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`

const pageHTML = `<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Torrent drop</title>
<link rel="icon" href="/icon.svg">
<style>
  :root { color-scheme: dark; --bg:#0b1512; --card:#132420; --line:#1f3b33; --ink:#e6fff5; --dim:#8fb3a7; --accent:#34d399; --warn:#fbbf24; }
  * { box-sizing: border-box; }
  body { margin:0; padding:1.5rem 1rem 3rem; background:var(--bg); color:var(--ink);
         font:16px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
  header { max-width:52rem; margin:0 auto 1.25rem; display:flex; align-items:center; gap:.75rem; }
  header img { width:34px; height:34px; }
  h1 { font-size:1.35rem; margin:0; letter-spacing:-.02em; }
  header p { margin:.15rem 0 0; color:var(--dim); font-size:.85rem; }
  main { max-width:52rem; margin:0 auto; }
  #drop { border:2px dashed var(--line); border-radius:.9rem; background:var(--card);
          padding:2rem 1rem; text-align:center; cursor:pointer; transition:border-color .15s, background .15s; }
  #drop.over { border-color:var(--accent); background:#16302a; }
  #drop b { display:block; font-size:1rem; }
  #drop span { color:var(--dim); font-size:.85rem; }
  textarea { width:100%; margin-top:.75rem; min-height:4.5rem; resize:vertical; background:var(--card);
             border:1px solid var(--line); border-radius:.6rem; color:var(--ink); padding:.7rem .9rem;
             font:inherit; font-size:.9rem; }
  textarea:focus { outline:none; border-color:var(--accent); }
  .row { display:flex; align-items:center; gap:.9rem; margin-top:.75rem; flex-wrap:wrap; }
  .btn { background:var(--accent); color:#08201a; border:0; border-radius:.5rem; font-weight:700;
         padding:.6rem 1rem; font-size:.9rem; cursor:pointer; }
  .btn:active { transform:scale(.97); }
  .btn.ghost { background:transparent; color:var(--accent); border:1px solid var(--accent);
               font-weight:600; padding:.35rem .6rem; font-size:.78rem; }
  .btn[disabled] { opacity:.5; cursor:default; }
  label.keep { color:var(--dim); font-size:.85rem; display:flex; align-items:center; gap:.4rem; }
  .free { margin-left:auto; color:var(--dim); font-size:.8rem; }
  h2 { font-size:.78rem; text-transform:uppercase; letter-spacing:.09em; color:var(--dim);
       margin:2rem 0 .75rem; font-weight:600; }
  .t { background:var(--card); border:1px solid var(--line); border-radius:.7rem; padding:.8rem .9rem;
       margin-bottom:.6rem; }
  .t .name { font-weight:600; font-size:.92rem; word-break:break-word; }
  .bar { height:6px; background:#0a1a15; border-radius:3px; margin:.55rem 0 .45rem; overflow:hidden; }
  .bar i { display:block; height:100%; background:var(--accent); }
  .t .meta { display:flex; gap:.6rem; align-items:center; flex-wrap:wrap; color:var(--dim); font-size:.8rem; }
  .t .meta .state { color:var(--ink); }
  .t .meta .done { color:var(--accent); }
  .t .meta .pend { color:var(--warn); }
  .empty { color:var(--dim); text-align:center; padding:1.5rem 0; font-size:.88rem; }
  #msg { margin-top:.75rem; font-size:.85rem; color:var(--accent); min-height:1.25rem; }
  #msg.bad { color:var(--warn); }
  footer { max-width:52rem; margin:2.5rem auto 0; color:var(--dim); font-size:.78rem;
           border-top:1px solid var(--line); padding-top:1rem; }
  code { background:#0a1a15; padding:.1rem .35rem; border-radius:.25rem; font-size:.75rem; }
</style>
</head>
<body>
<header>
  <img src="/icon.svg" alt="">
  <div>
    <h1>Torrent drop</h1>
    <p>Suelta un .torrent o pega un magnet. Al terminar, cross-seed lo busca en los demás trackers.</p>
  </div>
</header>

<main>
  <div id="drop">
    <b>Suelta el .torrent aquí</b>
    <span>o pulsa para elegirlo, o pega un magnet con Ctrl+V</span>
    <input id="file" type="file" accept=".torrent" multiple hidden>
  </div>

  <textarea id="links" placeholder="magnet:?xt=urn:btih:… , un enlace https a un .torrent, o el infohash pelado. Uno por línea."></textarea>

  <div class="row">
    <button class="btn" id="send">Añadir</button>
    <label class="keep"><input type="checkbox" id="keep"> Seguir sembrando siempre</label>
    <span class="free" id="free"></span>
  </div>
  <div id="msg"></div>

  <h2>Descargas a mano</h2>
  <div id="list"><div class="empty">Nada por aquí todavía.</div></div>
</main>

<footer>
  Se añaden con la categoría <code>manual</code>: siembran el plazo de su tracker, se paran solas y
  el fichero se queda. Marca "seguir sembrando" para que no se paren nunca.
</footer>

<script>
var STATES = {
  downloading: "descargando", forcedDL: "descargando (forzado)",
  metaDL: "buscando el .torrent", forcedMetaDL: "buscando el .torrent",
  stalledDL: "sin seeds", queuedDL: "en cola (1 descarga a la vez)",
  allocating: "reservando sitio", moving: "moviendo",
  checkingDL: "comprobando", checkingUP: "comprobando", checkingResumeData: "comprobando",
  uploading: "sembrando", forcedUP: "sembrando (forzado)",
  stalledUP: "sembrando, nadie pide", queuedUP: "esperando turno para sembrar",
  pausedUP: "hecho, parado", stoppedUP: "hecho, parado",
  pausedDL: "pausado", stoppedDL: "pausado",
  error: "error", missingFiles: "faltan ficheros"
};

function human(bytes) {
  if (!bytes) return "0 B";
  var units = ["B", "KB", "MB", "GB", "TB"], i = 0, n = bytes;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return (n >= 10 || i === 0 ? Math.round(n) : n.toFixed(1)) + " " + units[i];
}

function tags(t) { return (t.tags || "").split(",").map(function (s) { return s.trim(); }); }

function say(text, bad) {
  var msg = document.getElementById("msg");
  msg.textContent = text;
  msg.className = bad ? "bad" : "";
}

function card(t) {
  var pct = Math.round(t.progress * 100);
  var state = STATES[t.state] || t.state;
  var el = document.createElement("div");
  el.className = "t";

  var name = document.createElement("div");
  name.className = "name";
  name.textContent = t.name;
  el.appendChild(name);

  var bar = document.createElement("div");
  bar.className = "bar";
  var fill = document.createElement("i");
  fill.style.width = pct + "%";
  bar.appendChild(fill);
  el.appendChild(bar);

  var meta = document.createElement("div");
  meta.className = "meta";
  var bits = [
    ["state", state],
    ["", pct + "%"],
    ["", human(t.size)]
  ];
  if (t.dlspeed > 0) bits.push(["", human(t.dlspeed) + "/s"]);
  if (t.progress >= 1) bits.push(["", "ratio " + t.ratio.toFixed(2)]);
  bits.forEach(function (b) {
    var s = document.createElement("span");
    s.className = b[0];
    s.textContent = b[1];
    meta.appendChild(s);
  });

  if (tags(t).indexOf("xseed") !== -1) {
    var done = document.createElement("span");
    done.className = "done";
    done.textContent = "cross-seed buscado";
    meta.appendChild(done);
  } else if (t.progress >= 1) {
    var btn = document.createElement("button");
    btn.className = "btn ghost";
    btn.textContent = "buscar cross-seed";
    btn.onclick = function () {
      btn.disabled = true;
      btn.textContent = "buscando…";
      var body = new URLSearchParams();
      body.set("hash", t.hash);
      fetch("/api/cross-seed", { method: "POST", body: body })
        .then(function (r) { return r.json(); })
        .then(function (d) {
          if (d.error) { say(d.error, true); btn.disabled = false; btn.textContent = "buscar cross-seed"; }
          else { say("cross-seed ha buscado " + t.name); refresh(); }
        });
    };
    meta.appendChild(btn);
  } else {
    var pend = document.createElement("span");
    pend.className = "pend";
    pend.textContent = "cross-seed al acabar";
    meta.appendChild(pend);
  }

  el.appendChild(meta);
  return el;
}

function refresh() {
  return fetch("/api/list").then(function (r) { return r.json(); }).then(function (d) {
    if (d.error) { say(d.error, true); return; }
    if (d.sweepError) say("cross-seed no contesta: " + d.sweepError, true);
    document.getElementById("free").textContent = d.freeSpace ? human(d.freeSpace) + " libres" : "";
    var list = document.getElementById("list");
    list.textContent = "";
    if (!d.torrents || d.torrents.length === 0) {
      var empty = document.createElement("div");
      empty.className = "empty";
      empty.textContent = "Nada por aquí todavía.";
      list.appendChild(empty);
      return;
    }
    d.torrents.forEach(function (t) { list.appendChild(card(t)); });
  });
}

function send(files) {
  var data = new FormData();
  var links = document.getElementById("links").value;
  if (links.trim()) data.append("links", links);
  if (files) for (var i = 0; i < files.length; i++) data.append("torrents", files[i]);
  if (!links.trim() && (!files || files.length === 0)) { say("no hay nada que añadir", true); return; }
  if (document.getElementById("keep").checked) data.append("keep", "1");

  var button = document.getElementById("send");
  button.disabled = true;
  say("enviando…");
  fetch("/api/add", { method: "POST", body: data })
    .then(function (r) { return r.json(); })
    .then(function (d) {
      button.disabled = false;
      if (d.error) { say(d.error, true); return; }
      var text = d.added + (d.added === 1 ? " añadido" : " añadidos");
      if (d.rejected && d.rejected.length) text += ", " + d.rejected.length + " línea(s) sin entender";
      say(text);
      document.getElementById("links").value = "";
      refresh();
    })
    .catch(function (e) { button.disabled = false; say(String(e), true); });
}

var drop = document.getElementById("drop"), file = document.getElementById("file");
drop.onclick = function () { file.click(); };
file.onchange = function () { if (file.files.length) send(file.files); file.value = ""; };
document.getElementById("send").onclick = function () { send(null); };

["dragenter", "dragover"].forEach(function (ev) {
  document.addEventListener(ev, function (e) { e.preventDefault(); drop.classList.add("over"); });
});
["dragleave", "drop"].forEach(function (ev) {
  document.addEventListener(ev, function (e) {
    e.preventDefault();
    if (ev === "dragleave" && e.relatedTarget) return;
    drop.classList.remove("over");
  });
});
document.addEventListener("drop", function (e) {
  if (e.dataTransfer.files && e.dataTransfer.files.length) { send(e.dataTransfer.files); return; }
  var text = e.dataTransfer.getData("text");
  if (text) { document.getElementById("links").value = text; send(null); }
});

// Pasting a magnet is the common case on a phone, so it adds it without a second tap. Typing into
// the textarea is not: that would fire on every paste while editing.
document.addEventListener("paste", function (e) {
  if (e.target.tagName === "TEXTAREA") return;
  var text = (e.clipboardData || window.clipboardData).getData("text").trim();
  if (!text) return;
  document.getElementById("links").value = text;
  send(null);
});

refresh();
setInterval(function () { if (!document.hidden) refresh(); }, 2000);
</script>
</body>
</html>`
