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
  <rect width="64" height="64" rx="14" fill="#1d1530"/>
  <path fill="#a78bfa" d="M12 26h8l11-9v30l-11-9h-8z"/>
  <path d="M38 24a11 11 0 0 1 0 16M43 18a19 19 0 0 1 0 28" stroke="#a78bfa" stroke-width="4.5" stroke-linecap="round" fill="none"/>
</svg>`

const pageHTML = `<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Audio convert</title>
<link rel="icon" href="/icon.svg">
<style>
  :root { color-scheme: dark; --bg:#110d1c; --card:#1b1530; --line:#2d2547; --ink:#f1ecff; --dim:#a99fc6;
          --accent:#a78bfa; --ok:#34d399; --warn:#fbbf24; --bad:#f87171; }
  * { box-sizing: border-box; }
  body { margin:0; padding:1.5rem 1rem 3rem; background:var(--bg); color:var(--ink);
         font:16px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
  header, main, footer { max-width:52rem; margin-left:auto; margin-right:auto; }
  header { margin-bottom:1.25rem; display:flex; align-items:center; gap:.75rem; }
  header img { width:34px; height:34px; }
  h1 { font-size:1.35rem; margin:0; letter-spacing:-.02em; }
  header p { margin:.15rem 0 0; color:var(--dim); font-size:.85rem; }
  .facts { display:flex; gap:1rem; flex-wrap:wrap; color:var(--dim); font-size:.8rem; margin-bottom:.75rem; }
  .banner { background:#2a2140; border:1px solid var(--warn); color:var(--warn); border-radius:.6rem;
            padding:.6rem .9rem; font-size:.85rem; margin-bottom:.75rem; }
  .banner:empty { display:none; }
  h2 { font-size:.78rem; text-transform:uppercase; letter-spacing:.09em; color:var(--dim);
       margin:2rem 0 .75rem; font-weight:600; }
  .t { background:var(--card); border:1px solid var(--line); border-radius:.7rem; padding:.8rem .9rem; margin-bottom:.6rem; }
  .t.off { opacity:.55; }
  .t .name { font-weight:600; font-size:.95rem; word-break:break-word; }
  .t .why { color:var(--dim); font-size:.82rem; }
  .t .plan { margin:.4rem 0 .2rem; font-size:.88rem; }
  .t .meta { display:flex; gap:.7rem; align-items:center; flex-wrap:wrap; color:var(--dim); font-size:.8rem; margin-top:.35rem; }
  .tag { border:1px solid var(--line); border-radius:.35rem; padding:0 .4rem; font-size:.75rem; }
  .tag.warn { color:var(--warn); border-color:var(--warn); }
  .tag.ok { color:var(--ok); border-color:var(--ok); }
  .tag.bad { color:var(--bad); border-color:var(--bad); }
  .bar { height:8px; background:#0d0a17; border-radius:4px; margin:.6rem 0 .3rem; overflow:hidden; }
  .bar i { display:block; height:100%; background:var(--accent); transition:width .8s linear; }
  .btn { background:var(--accent); color:#1d1530; border:0; border-radius:.5rem; font-weight:700;
         padding:.45rem .9rem; font-size:.85rem; cursor:pointer; margin-left:auto; }
  .btn.ghost { background:transparent; color:var(--accent); border:1px solid var(--accent); font-weight:600; }
  .btn[disabled] { opacity:.45; cursor:default; }
  .empty { color:var(--dim); text-align:center; padding:1.5rem 0; font-size:.88rem; }
  .h { display:grid; grid-template-columns: 1fr auto; gap:.1rem .8rem; font-size:.82rem; padding:.45rem 0;
       border-bottom:1px solid var(--line); }
  .h .sub { color:var(--dim); grid-column: 1 / -1; }
  #msg { font-size:.85rem; min-height:1.25rem; color:var(--warn); }
  footer { margin-top:2.5rem; color:var(--dim); font-size:.78rem; border-top:1px solid var(--line); padding-top:1rem; }
  code { background:#0d0a17; padding:.1rem .35rem; border-radius:.25rem; font-size:.75rem; }
</style>
</head>
<body>
<header>
  <img src="/icon.svg" alt="">
  <div>
    <h1>Audio convert</h1>
    <p>Ficheros cuyo audio tuvo que recodificarse para un cliente, con lo que haría falta para que no vuelva a pasar.</p>
  </div>
</header>

<main>
  <div class="facts" id="facts"></div>
  <div class="banner" id="banner"></div>
  <div id="msg"></div>

  <h2>Por convertir</h2>
  <div id="list"><div class="empty">Cargando…</div></div>

  <h2>Historial</h2>
  <div id="history"><div class="empty">Nada convertido todavía.</div></div>
</main>

<footer>
  Convierte <code>scripts/sync/audio-compat.py --file</code>, lo mismo que la pasada de las 01:30: añade
  una pista AC3 delante, conserva todas las demás y deja el fichero anterior como <code>.original</code>.
  Una conversión a la vez; si la pasada nocturna está trabajando, la cola espera.
</footer>

<script>
var REASONS = {
  direct_play_off: "El cliente desactivó la reproducción directa: activa «Force Direct Play» en el cliente. Cambiar el fichero no ayuda.",
  refuses_target: "El cliente tampoco acepta AC3, que es lo que se añadiría. Cambiar el fichero no ayuda.",
  not_library: "No está en films/ ni series/, así que no hay fichero que cambiar."
};

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
  return new Date(epoch * 1000).toLocaleString("es-ES", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

// Plex calls DTS dca, as audio-compat's FFPROBE_NAMES knows.
function codecName(codec) { return ({ dca: "dts" }[(codec || "?").toLowerCase()] || codec || "?").toUpperCase(); }

function channels(n) { return { 1: "mono", 2: "estéreo", 6: "5.1", 8: "7.1" }[n] || (n + " canales"); }

function trackName(t) {
  if (t.title) return t.title;
  return [t.language, (t.codec || "").toUpperCase(), t.channels ? channels(t.channels) : ""].filter(Boolean).join(" ");
}

function planText(p) {
  return "Añadir " + p.target_codec.toUpperCase() + " " + channels(p.target_channels) + " (" + p.target_bitrate_kbps +
    " kb/s) desde la pista «" + trackName(p.source) + "»" + (p.source["default"] ? ", la marcada por defecto" : "") +
    ". Se conservan todas las pistas.";
}

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
    var ok = confirm("«" + (it.title || it.library_path) + "» sigue sembrando (tiene un hardlink con el torrent).\n\n" +
      "Convertirlo deja el fichero actual como .original, que sigue enlazado al torrent, así que el torrent no se entera. " +
      "Pero el disco guarda las dos copias: " + human(it.size) + " más hasta que el torrent se borre y se elimine el .original.\n\n" +
      "¿Convertir igualmente?");
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

function card(it, state) {
  var actionable = it.actionable;
  var box = el("div", "t" + (actionable ? "" : " off"));
  box.appendChild(el("div", "name", it.title || it.library_path || it.path));
  var who = it.client + " (" + it.app + ") no aceptó " + codecName(it.codec) + " · " + when(it.seen);
  box.appendChild(el("div", "why", who));

  if (!actionable) {
    var reason = REASONS[it.reason_code];
    if (it.reason_code === "has_playable") {
      reason = "Ya tiene «" + it.reason_detail + "», un códec que ningún cliente ha rechazado: elige esa pista en el reproductor.";
    }
    box.appendChild(el("div", "plan", reason || it.reason || ""));
    return box;
  }

  box.appendChild(el("div", "plan", it.plan ? planText(it.plan) :
    "El servidor no dio las pistas; el script elegirá la marcada por defecto."));

  if (it.status === "converting" && state.running) {
    var job = state.running.job;
    var pct = job.duration ? Math.min(100, job.seconds / job.duration * 100) : 0;
    var bar = el("div", "bar"), fill = el("i");
    fill.style.width = pct.toFixed(1) + "%";
    bar.appendChild(fill);
    box.appendChild(bar);
  }

  var meta = el("div", "meta");
  var pending = it.status !== "done" && it.status !== "missing";
  if (it.plan && pending) meta.appendChild(el("span", "", "+" + human(it.plan.extra_bytes) + " de pista nueva"));
  if (it.exists && pending) meta.appendChild(el("span", "", "y el .original ocupa " + human(it.size) + " hasta que se borre"));

  if (it.status === "converting" && state.running) {
    var job2 = state.running.job;
    var pct2 = job2.duration ? Math.round(job2.seconds / job2.duration * 100) : 0;
    meta.appendChild(el("span", "tag ok", "convirtiendo " + pct2 + "%"));
    meta.appendChild(el("span", "", "quedan " + clock(state.running.eta) + " a " + job2.speed.toFixed(0) + "×"));
    if (job2.source) meta.appendChild(el("span", "", "desde la pista " + job2.source.position + " (" + trackName(job2.source) + ")"));
  } else if (it.duration && pending) {
    meta.appendChild(el("span", "", "~" + clock(it.eta) + " (" + clock(it.duration) + " de película)"));
  }
  if (it.links > 1) meta.appendChild(el("span", "tag warn", "sigue sembrando (hardlink)"));
  if (it.exists && !it.enough_space) meta.appendChild(el("span", "tag bad", "no hay sitio"));

  var button;
  switch (it.status) {
    case "ready":
      button = el("button", "btn", "▶ Convertir");
      button.disabled = !it.enough_space;
      button.onclick = function () { play(it, button); };
      break;
    case "queued":
      meta.appendChild(el("span", "tag", "en cola"));
      button = el("button", "btn ghost", "Quitar");
      button.onclick = function () { post("/api/cancel", { library_path: it.library_path }).then(refresh); };
      break;
    case "done":
      meta.appendChild(el("span", "tag ok", "hecho " + (it.outcome.mode === "nightly" ? "por la noche" : "") + " · " + when(it.outcome.time)));
      break;
    case "failed":
      meta.appendChild(el("span", "tag bad", "falló: " + it.outcome.reason));
      button = el("button", "btn ghost", "Reintentar");
      button.disabled = !it.enough_space;
      button.onclick = function () { play(it, button); };
      break;
    case "missing":
      meta.appendChild(el("span", "tag", "el fichero ya no está (¿reemplazado?)"));
      break;
  }
  if (button) meta.appendChild(button);
  box.appendChild(meta);
  return box;
}

function historyRow(h) {
  var row = el("div", "h");
  row.appendChild(el("span", "", h.name));
  var ok = h.result === "done";
  row.appendChild(el("span", "tag " + (ok ? "ok" : "bad"), ok ? "hecho" : "falló"));
  var bits = [when(h.time), h.mode === "nightly" ? "pasada nocturna" : "desde aquí"];
  if (h.seconds) bits.push(clock(h.seconds));
  if (h.speed) bits.push(h.speed.toFixed(0) + "× tiempo real");
  if (h.reason) bits.push(h.reason);
  row.appendChild(el("span", "sub", bits.join(" · ")));
  return row;
}

var timer = null;

function refresh() {
  return fetch("/api/state").then(function (r) { return r.json(); }).then(function (d) {
    if (d.error) say(d.error);
    var facts = document.getElementById("facts");
    facts.textContent = "";
    facts.appendChild(el("span", "", human(d.free) + " libres (se dejan " + human(d.minFree) + ")"));
    facts.appendChild(el("span", "", "velocidad media " + d.speed.toFixed(0) + "× tiempo real"));
    if (d.queue && d.queue.length) facts.appendChild(el("span", "", d.queue.length + " en cola"));

    var banner = document.getElementById("banner");
    banner.textContent = "";
    if (d.holder && !d.running) {
      var file = (d.holder.file || "").split("/").pop();
      banner.textContent = d.holder.mode === "nightly"
        ? "La pasada nocturna está " + (file ? "convirtiendo " + file : "revisando la biblioteca") + "; la cola espera a que acabe."
        : "Otra conversión tiene el cerrojo" + (file ? " (" + file + ")" : "") + "; la cola espera.";
    }

    var list = document.getElementById("list");
    list.textContent = "";
    if (!d.items || !d.items.length) {
      list.appendChild(el("div", "empty", "Ningún cliente ha necesitado recodificar audio todavía."));
    } else {
      d.items.forEach(function (it) { list.appendChild(card(it, d)); });
    }

    var hist = document.getElementById("history");
    hist.textContent = "";
    if (!d.history || !d.history.length) hist.appendChild(el("div", "empty", "Nada convertido todavía."));
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
