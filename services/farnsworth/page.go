package main

import (
	"html/template"
	"log"
	"net/http"
)

func render(w http.ResponseWriter, groups []group, streamBase string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct {
		Groups     []group
		StreamBase string
	}{groups, streamBase}
	if err := pageTmpl.Execute(w, data); err != nil {
		log.Printf("farnsworth: render failed: %v", err)
	}
}

func icon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(iconSVG))
}

// A cathode ray tube with an antenna, for the man who built the first one that worked.
const iconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
  <rect width="64" height="64" rx="14" fill="#1a1024"/>
  <path d="M22 16 L32 26 L42 16" stroke="#c084fc" stroke-width="3" fill="none" stroke-linecap="round"/>
  <rect x="12" y="26" width="40" height="26" rx="5" fill="none" stroke="#c084fc" stroke-width="3"/>
  <path d="M28 34 L38 39 L28 44 Z" fill="#c084fc"/>
</svg>`

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Farnsworth</title>
<link rel="icon" href="/icon.svg">
<style>
  :root { color-scheme: dark; --bg:#140d1c; --card:#1f1630; --line:#332748; --ink:#ede9fe; --dim:#a99cc4; --accent:#c084fc; }
  * { box-sizing: border-box; }
  body { margin:0; padding:1.5rem 1rem 3rem; background:var(--bg); color:var(--ink);
         font:16px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
  header { max-width:64rem; margin:0 auto 1.5rem; display:flex; align-items:center; gap:.75rem; }
  header img { width:34px; height:34px; }
  h1 { font-size:1.35rem; margin:0; letter-spacing:-.02em; }
  header p { margin:.15rem 0 0; color:var(--dim); font-size:.85rem; }
  main { max-width:64rem; margin:0 auto; }
  h2 { font-size:.78rem; text-transform:uppercase; letter-spacing:.09em; color:var(--dim);
       margin:2rem 0 .75rem; font-weight:600; }
  .grid { display:grid; gap:.7rem; grid-template-columns:repeat(auto-fill, minmax(15rem, 1fr)); }
  .ch { background:var(--card); border:1px solid var(--line); border-radius:.7rem; padding:.8rem;
        display:flex; gap:.7rem; align-items:center; }
  .ch img { width:40px; height:40px; object-fit:contain; border-radius:.35rem; flex:none; background:#0d0814; }
  .meta { min-width:0; flex:1; }
  .name { font-weight:600; font-size:.92rem; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .btn { background:var(--accent); color:#1a1024; border:0; border-radius:.5rem; font-weight:700;
         padding:.55rem .85rem; font-size:.85rem; cursor:pointer; flex:none; text-decoration:none; }
  .btn:active { transform:scale(.97); }
  .btn.ghost { background:transparent; color:var(--accent); border:1px solid var(--accent); }
  .actions { display:flex; gap:.4rem; flex:none; }
  .setup { max-width:64rem; margin:0 auto 1.25rem; background:var(--card); border:1px solid var(--line);
           border-radius:.7rem; padding:.9rem 1rem; font-size:.85rem; color:var(--dim); }
  .setup b { color:var(--ink); }
  .setup .row { display:flex; gap:.5rem; margin-top:.55rem; align-items:center; }
  .setup input { flex:1; min-width:0; background:#0d0814; border:1px solid var(--line); color:var(--ink);
                 border-radius:.4rem; padding:.45rem .6rem; font-size:.8rem; }
  footer { max-width:64rem; margin:2.5rem auto 0; color:var(--dim); font-size:.78rem;
           border-top:1px solid var(--line); padding-top:1rem; }
  code { background:#0d0814; padding:.1rem .35rem; border-radius:.25rem; font-size:.75rem; }
  #q { width:100%; max-width:64rem; margin:0 auto 0; display:block; background:var(--card);
       border:1px solid var(--line); border-radius:.6rem; color:var(--ink); padding:.7rem .9rem;
       font-size:1rem; }
  #q::placeholder { color:var(--dim); }
  #q:focus { outline:none; border-color:var(--accent); }
  .hidden { display:none; }
  #empty { color:var(--dim); text-align:center; padding:2rem 0; }
  #toast { position:fixed; left:1rem; right:1rem; bottom:1rem; max-width:34rem; margin:0 auto;
           background:var(--card); border:1px solid var(--accent); border-radius:.7rem;
           padding:.9rem 1rem; font-size:.88rem; box-shadow:0 8px 30px #0009; }
  #toast a { color:var(--accent); }
  #toast .x { float:right; color:var(--dim); text-decoration:none; padding-left:.75rem; }
</style>
</head>
<body>
<header>
  <img src="/icon.svg" alt="">
  <div>
    <h1>Farnsworth</h1>
    <p>36 canales, directos a tu reproductor.</p>
  </div>
</header>

<div class="setup">
  <b>La lista entera</b>, para añadirla una vez en VLC, Kodi o el reproductor que uses.
  <div class="row">
    <input id="url" readonly value="">
    <a class="btn" href="/all.m3u">Descargar</a>
  </div>
</div>

<input id="q" type="search" placeholder="Buscar canal…" autocomplete="off" autocapitalize="off" spellcheck="false">

<main>
{{range .Groups}}
  <section data-group="{{.Name}}">
  <h2>{{.Name}}</h2>
  <div class="grid">
  {{range .Channels}}
    <div class="ch" data-name="{{.Name}}">
      {{if .Logo}}<img src="{{.Logo}}" alt="" loading="lazy">{{end}}
      <div class="meta">
        <div class="name" title="{{.Name}}">{{.Name}}</div>
      </div>
      <div class="actions">
        <a class="btn" href="#" onclick="return vlc('{{.ID}}')">VLC</a>
        <a class="btn ghost" href="/m3u/{{.ID}}">Descargar</a>
      </div>
    </div>
  {{end}}
  </div>
  </section>
{{end}}
<p id="empty" class="hidden">Ningún canal coincide.</p>
</main>

<div id="toast" class="hidden">
  <a class="x" href="#" onclick="return hideToast()">✕</a>
  VLC no se ha abierto. Si no lo tienes instalado,
  <a href="https://apps.apple.com/app/vlc-media-player/id650377962" target="_blank" rel="noopener">descárgalo aquí</a>
  o usa <b>Abrir</b>, que funciona con el reproductor que ya tengas.
</div>

<footer>
  Si un canal no arranca en un minuto, nadie lo está compartiendo: prueba la otra fuente del mismo
  canal. Los streams salen de <code>{{.StreamBase}}</code>, con Tailscale o desde casa.
</footer>

<script>
// The scheme jump never reaches the server, so the play is reported first. Ignore beacon failures:
// not being able to log is no reason to stop someone watching television.
document.getElementById('url').value = location.origin + '/all.m3u';
document.getElementById('url').addEventListener('focus', function () { this.select(); });

// Kept as a secondary link only: VLC's iOS scheme is unreliable, so the button people press is the
// playlist download, which every device knows how to route.
var toast = document.getElementById('toast');
function hideToast() { toast.classList.add('hidden'); return false; }

// The vlc-x-callback scheme only exists on iOS. macOS VLC registers protocol schemes (http, rtsp,
// smb) and no app scheme at all, so calling it there does nothing however well VLC is installed.
// Everywhere else the playlist file is the mechanism: VLC claims public.m3u-playlist and the OS
// routes it. Checked against VLC.app's own Info.plist rather than assumed.
var isIOS = /iPad|iPhone|iPod/.test(navigator.userAgent) ||
            (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);

function vlc(id) {
  try { navigator.sendBeacon('/click/' + id); } catch (e) {}

  if (!isIOS) { window.location.href = '/m3u/' + id; return false; }

  var url = {{.StreamBase}} + '/ace/getstream?id=' + id;
  var left = false;
  var onHide = function () { left = true; };
  document.addEventListener('visibilitychange', onHide, { once: true });
  window.addEventListener('pagehide', onHide, { once: true });

  window.location.href = 'vlc-x-callback://x-callback-url/stream?url=' + encodeURIComponent(url)
    + '&x-success=' + encodeURIComponent(location.href);

  setTimeout(function () {
    if (!left && document.visibilityState === 'visible') { toast.classList.remove('hidden'); }
    document.removeEventListener('visibilitychange', onHide);
  }, 1500);
  return false;
}

// Matches the group as well as the name, so "motogp" finds the group and "eurosport 2" the channel.
// Accents are stripped both sides: nobody types them on a phone while a race is starting.
var q = document.getElementById('q'), empty = document.getElementById('empty');
var norm = function (s) {
  return s.toLowerCase().normalize('NFD').replace(/[\u0300-\u036f]/g, '');
};
// Start typing anywhere and the box takes over, so finding a channel is one action rather than
// aim-then-type. Deliberately not autofocus: on a tablet that throws the keyboard up every visit.
document.addEventListener('keydown', function (e) {
  if (e.target === q) {
    if (e.key === 'Escape') { q.value = ''; q.dispatchEvent(new Event('input')); q.blur(); }
    return;
  }
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  if (e.key.length === 1) { q.focus(); }
});

q.addEventListener('input', function () {
  var needle = norm(q.value.trim()), any = false;
  document.querySelectorAll('section[data-group]').forEach(function (sec) {
    var group = norm(sec.dataset.group), shown = 0;
    sec.querySelectorAll('.ch').forEach(function (ch) {
      var hit = !needle || group.indexOf(needle) >= 0 || norm(ch.dataset.name).indexOf(needle) >= 0;
      ch.classList.toggle('hidden', !hit);
      if (hit) shown++;
    });
    sec.classList.toggle('hidden', shown === 0);
    any = any || shown > 0;
  });
  empty.classList.toggle('hidden', any);
});
</script>
</body>
</html>`))
