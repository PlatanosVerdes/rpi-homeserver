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

function ago(epoch) {
  var seconds = Date.now() / 1000 - epoch;
  if (seconds < 60) return "just now";
  if (seconds < 3600) return Math.floor(seconds / 60) + " min ago";
  if (seconds < 86400) return Math.floor(seconds / 3600) + " h ago";
  return when(epoch);
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

// What --target takes, from audio-compat.py's last library scan; empty until the first one.
var targets = {}, defaultTarget = "ac3", chosen = {};

function sourceChannels(it) { return it.plan && it.plan.source.channels ? it.plan.source.channels : 6; }

function targetLabel(name, it) {
  var spec = targets[name];
  return name.toUpperCase() + " " + channels(Math.min(sourceChannels(it), spec ? spec.max_channels : 6));
}

// A target in the codec the client refused would be re-encoded all the same.
function usable(name, it) { return !targets[name] || targets[name].codec !== codecName(it.codec).toLowerCase(); }

// The default first, then the most channels: JSON objects keep no order once Go has sorted the keys.
function targetNames() {
  return Object.keys(targets).sort(function (a, b) {
    return (b === defaultTarget) - (a === defaultTarget) ||
      targets[b].max_channels - targets[a].max_channels || (a < b ? -1 : 1);
  });
}

function pick(it) {
  var names = targetNames(), want = chosen[it.library_path] || defaultTarget;
  if (usable(want, it)) return want;
  return names.filter(function (n) { return usable(n, it); })[0] || want;
}

function extraBytes(it, name) {
  var spec = targets[name];
  if (spec && it.duration) return spec.bitrate_kbps * 1000 / 8 * it.duration;
  return it.plan ? it.plan.extra_bytes : 0;
}

function play(it, button, target) {
  var data = { library_path: it.library_path };
  if (target && targets[target]) data.target = target;
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
  if (it.cause === "library") {
    lines.push(["No client has played it yet. Its main track is ", codecName(it.codec),
      ", which the nightly pass treats as one the TV cannot play."]);
    lines.push(["", it.links > 1 ? "The nightly pass waits until it stops seeding" :
      "The nightly pass converts it on its own, between 05:00 and 07:00", "; Convert does it now."]);
  } else {
    var verb = it.cause === "codec" ? " refused " : " had its audio re-encoded from ";
    lines.push([it.client + " in " + appName(it.app) + verb, codecName(it.codec), " on " + when(it.seen) + "."]);
  }
  if (!it.actionable && it.reason) lines.push(["", it.reason.charAt(0).toUpperCase() + it.reason.slice(1) + ".", ""]);
  if (it.plan) {
    var p = it.plan;
    var sources = p.sources && p.sources.length ? p.sources : [p.source];
    if (sources.length > 1) {
      lines.push(["Adds ", sources.length + " " + p.target_codec.toUpperCase() + " tracks at " + p.target_bitrate_kbps + " kb/s each",
        ", one per language the TV cannot play, built from the " + sources.map(trackName).join(" and ") +
        " tracks. Every track is kept."]);
    } else {
      lines.push(["Adds ", p.target_codec.toUpperCase() + " " + channels(p.target_channels) + " at " + p.target_bitrate_kbps + " kb/s",
        " built from the " + trackName(p.source) + " track" + (p.source.played ? ", the one that was playing" :
        p.source["default"] ? ", the default one" : "") + ". Every track is kept."]);
    }
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
    var target = pick(it), names = targetNames();
    var choosable = names.length && (it.status === "ready" || it.status === "failed");
    var change = el("div", "c change", codecName(it.codec) + " → ");
    if (choosable) {
      var select = el("select");
      select.setAttribute("aria-label", "Codec of the added track");
      names.forEach(function (n) {
        var option = el("option", "", targetLabel(n, it) + (usable(n, it) ? "" : " (refused)"));
        option.value = n;
        option.disabled = !usable(n, it);
        select.appendChild(option);
      });
      select.value = target;
      select.onclick = function (e) { e.stopPropagation(); };
      select.onchange = function () { chosen[it.library_path] = select.value; select.blur(); refresh(); };
      change.appendChild(select);
    } else {
      change.appendChild(document.createTextNode(names.length ? targetLabel(target, it) :
        it.plan ? it.plan.target_codec.toUpperCase() + " " + channels(it.plan.target_channels) : "AC3"));
    }
    meta.appendChild(change);
    if (running) {
      var bar = el("div", "bar"), fill = el("i");
      fill.style.width = progress(state).toFixed(1) + "%";
      bar.appendChild(fill);
      meta.appendChild(bar);
      meta.appendChild(el("div", "c r", Math.round(progress(state)) + "%, " + clock(state.running.eta)));
    } else if (it.status === "done") {
      meta.appendChild(el("div", "c rest", "Converted " + when(it.outcome.time) + (it.outcome.mode === "nightly" ? " by the nightly pass" : "")));
    } else if (it.status === "missing") {
      meta.appendChild(el("div", "c rest", "File replaced since"));
    } else {
      var extra = extraBytes(it, target);
      meta.appendChild(el("div", "c r", extra ? "+" + human(extra) : ""));
      meta.appendChild(el("div", "c r", it.status === "queued" ? "queued" :
        it.status === "failed" ? "failed" : it.duration ? "~" + clock(it.eta) : ""));
    }
  }

  var button = null;
  if (it.actionable && (it.status === "ready" || it.status === "failed")) {
    var go = el("button", it.status === "ready" ? "primary" : "", it.status === "ready" ? "Convert" : "Retry");
    go.disabled = !it.enough_space;
    if (!it.enough_space) go.title = "Not enough free space";
    go.onclick = function (e) { e.stopPropagation(); play(it, go, select ? select.value : ""); };
    button = go;
  } else if (it.status === "queued") {
    button = el("button", "", "Remove");
    button.onclick = function (e) { e.stopPropagation(); post("/api/cancel", { library_path: it.library_path }).then(refresh); };
  }
  if (button) row.appendChild(button);
  else if (running) row.appendChild(el("span"));

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

var DUB_STATUS = { requested: "Requested", cancelling: "Cancelling", queued: "Waiting",
  trying: "Searching", probing: "Checking the dub", downloading: "Downloading", done: "Added",
  failed: "Failed", cancelled: "Cancelled" };

function dubActive(status) { return status !== "done" && status !== "failed" && status !== "cancelled"; }

var dub = null, dubLang = "";

function dubKey(id, lang) { return id + "-" + lang; }

// What is under way for each film and language: a job, or a request the service has not taken yet.
function dubPending() {
  var out = {};
  (dub.jobs || []).forEach(function (j) { if (dubActive(j.status)) out[dubKey(j.movie_id, j.lang)] = j.status; });
  (dub.requests || []).forEach(function (r) { out[dubKey(r.movie_id, r.lang)] = r.cancel ? "cancelling" : "requested"; });
  return out;
}

function langName(code) { return (dub.languages || {})[code] || language(code); }

function dubPost(url, id, lang, button) {
  button.disabled = true;
  post(url, { movie_id: id, lang: lang }).then(function (d) {
    if (d.error) { say(d.error); button.disabled = false; return; }
    say("");
    refresh();
  });
}

function dubCancel(id, lang) {
  var b = el("button", "", "Cancel");
  b.onclick = function () { dubPost("/api/add-audio/cancel", id, lang, b); };
  return b;
}

function dubJobRow(j, pending) {
  var frag = document.createDocumentFragment();
  var status = pending[dubKey(j.movie_id, j.lang)] === "cancelling" ? "cancelling" : j.status;
  var row = el("div", "row" + (dubActive(j.status) ? "" : " off"));
  row.appendChild(el("span", "dot" + (j.status === "done" ? " ok" : j.status === "failed" ? " bad" :
    j.status === "cancelled" ? " idle" : "")));
  var title = el("div", "title", j.title || String(j.movie_id));
  title.appendChild(el("small", "", langName(j.lang)));
  row.appendChild(title);
  var meta = el("div", "meta");
  row.appendChild(meta);
  meta.appendChild(el("div", "c", DUB_STATUS[status] || status));
  meta.appendChild(el("div", "c r span2", j.updated ? when(j.updated) : ""));
  if (dubActive(j.status) && status !== "cancelling") row.appendChild(dubCancel(j.movie_id, j.lang));
  frag.appendChild(row);

  var lines = [];
  var c = j.current;
  if (c && dubActive(j.status)) {
    lines.push(["Release: ", c.title, " (" + [human(c.size), c.indexer + (c.private ? ", private" : "")].filter(Boolean).join(", ") + ")"]);
    if (c.dub) lines.push(["Dub: ", c.dub, ""]);
  }
  if (j.reason && !(c && dubActive(j.status))) lines.push(["", j.reason, ""]);
  if (lines.length) {
    var more = el("div", "more open");
    lines.forEach(function (l) {
      var p = el("div");
      p.appendChild(document.createTextNode(l[0]));
      p.appendChild(el("b", "", l[1]));
      p.appendChild(document.createTextNode(l[2]));
      more.appendChild(p);
    });
    frag.appendChild(more);
  }
  return frag;
}

function plural(n, one, many) { return n + " " + (n === 1 ? one : many); }

function until(epoch) {
  var seconds = epoch - Date.now() / 1000;
  if (seconds < 60) return "in under a minute";
  if (seconds < 3600) return "in " + Math.round(seconds / 60) + " min";
  if (seconds < 86400) return "in " + Math.round(seconds / 3600) + " h";
  return "in " + Math.round(seconds / 86400) + " d";
}

// The availability the state holds is for the language it was asked for, which a pick may have changed since.
function availabilityOf(id) { return dub.lang === dubLang ? (dub.availability || {})[id] : undefined; }

function isAvailable(a) { return !!a && (a.named > 0 || a.own_site > 0); }

// What a film's last search means for the language, from the strongest sign down: a title naming it,
// a site in it, any release a probe could take.
function availabilitySummary(a, name) {
  if (!a.time) return ["warn", "Search failed: " + a.error];
  if (a.named) return ["pill yes", plural(a.named, "release names ", "releases name ") + name];
  if (a.own_site) return ["pill soft", a.own_site === 1 ? "1 from a " + name + "-language site" : a.own_site + " from " + name + "-language sites"];
  if (a.releases) return ["", "None named, " + a.releases + " to probe"];
  return ["", "Nothing found"];
}

var bestOpen = {};

// A search under way: a thin bar of the indexers that answered, and what they found so far.
function searchProgressLine(search) {
  var line = el("div", "check");
  if (search.status === "queued") {
    line.appendChild(el("span", "", "Waiting to search"));
    return line;
  }
  var bar = el("div", "bar"), fill = el("i");
  fill.style.width = (search.total ? Math.round(search.done / search.total * 100) : 0) + "%";
  bar.appendChild(fill);
  line.appendChild(bar);
  line.appendChild(el("span", "", search.total ?
    "Searching " + search.done + " of " + plural(search.total, "indexer", "indexers") + ", " +
    plural(search.releases || 0, "release", "releases") + " so far" : "Searching the indexers"));
  return line;
}

var searchTimer = null;

function searchesUnderway() { return Object.keys((dub && dub.searching) || {}).length > 0; }

// While a search runs its progress is asked for every second; once one ends, the whole state is, for its result.
function watchSearches() {
  if (searchTimer || !searchesUnderway()) return;
  searchTimer = setTimeout(function () {
    searchTimer = null;
    if (document.hidden) { watchSearches(); return; }
    fetch("/api/add-audio/checks").then(function (r) { return r.json(); }).then(function (d) {
      var now = d.searching || {};
      var ended = Object.keys(dub.searching || {}).some(function (id) { return !now[id]; });
      dub.searching = now;
      if (ended) { refresh(); return; }
      renderDub();
      watchSearches();
    }).catch(function () { setTimeout(watchSearches, 5000); });
  }, 1000);
}

function availabilityLine(id, a) {
  var line = el("div", "check");
  if (!a) {
    if (dub.lang !== dubLang) return null;
    line.appendChild(el("span", "", "Not searched yet"));
    return line;
  }
  var summary = availabilitySummary(a, langName(dubLang));
  line.appendChild(el("span", summary[0], summary[1]));
  if (a.time) {
    var age = "searched " + ago(a.time);
    if (a.indexers_failed) age += ", " + a.indexers_failed + " of " + plural(a.indexers_total, "indexer", "indexers") + " failed";
    line.appendChild(el("span", "age", age));
    if (a.error) line.appendChild(el("span", "warn", "a newer search failed"));
  }
  var key = dubKey(id, dubLang), best = a.best || [];
  if (best.length) {
    var toggle = el("button", "", (bestOpen[key] ? "Hide" : "Show") + " the best " + (best.length === 1 ? "one" : best.length));
    toggle.type = "button";
    toggle.setAttribute("aria-expanded", String(!!bestOpen[key]));
    toggle.onclick = function () { bestOpen[key] = !bestOpen[key]; renderDub(); };
    line.appendChild(toggle);
    if (bestOpen[key]) {
      var list = el("ul");
      best.forEach(function (b) {
        var li = el("li");
        li.appendChild(el("span", "t", b.title));
        li.appendChild(el("span", "s", [human(b.size), b.indexer + (b.private ? ", private" : "")].join(", ")));
        list.appendChild(li);
      });
      line.appendChild(list);
    }
  }
  return line;
}

function dubFilmRow(f, pending) {
  var frag = document.createDocumentFragment();
  var row = el("div", "row");
  var status = pending[dubKey(f.id, dubLang)];
  row.appendChild(el("span", status ? "dot" : "dot idle"));
  var title = el("div", "title", f.title);
  if (f.year) title.appendChild(el("small", "", String(f.year)));
  row.appendChild(title);
  var meta = el("div", "meta");
  row.appendChild(meta);
  meta.appendChild(el("div", "c", f.audio ? language(f.audio) + " audio" : ""));
  if (status) meta.appendChild(el("div", "c span2", DUB_STATUS[status] || status));
  var search = (dub.searching || {})[f.id], found = availabilityOf(f.id);
  var acts = el("div", status ? "acts narrow" : "acts");
  if (!status) {
    var again = el("button", "quiet", search ? "Searching…" : found ? "Search again" : "Search now");
    again.disabled = !!search;
    again.title = "Ask the indexers now what they have for this film, without downloading";
    again.onclick = function () { dubPost("/api/add-audio/check", f.id, dubLang, again); };
    acts.appendChild(again);
    var add = el("button", "primary", "Add " + langName(dubLang));
    add.onclick = function () { dubPost("/api/add-audio", f.id, dubLang, add); };
    acts.appendChild(add);
  } else if (status !== "cancelling") {
    acts.appendChild(dubCancel(f.id, dubLang));
  }
  if (acts.childNodes.length) row.appendChild(acts);
  frag.appendChild(row);
  var line = search ? searchProgressLine(search) : availabilityLine(f.id, found);
  if (line) frag.appendChild(line);
  return frag;
}

var LANG_KEY = "audio-manager.lang", RECENT_KEY = "audio-manager.recent-langs";

function stored(key) { try { return localStorage.getItem(key) || ""; } catch (e) { return ""; } }

function store(key, value) { try { localStorage.setItem(key, value); } catch (e) {} }

function recentLangs() { try { return JSON.parse(stored(RECENT_KEY) || "[]").slice(0, 3); } catch (e) { return []; } }

function missingCount(code) {
  return (dub.films || []).filter(function (f) { return (f.missing || []).indexOf(code) >= 0; }).length;
}

// The other ISO 639-2 spelling of each code, so "ger" finds deu and "fra" finds fre.
var ALIASES = { fre: "fra", ger: "deu", dut: "nld", chi: "zho", cze: "ces", gre: "ell", per: "fas", rum: "ron",
  slo: "slk", wel: "cym", arm: "hye", baq: "eus", geo: "kat", ice: "isl", mac: "mkd", may: "msa", alb: "sqi",
  bur: "mya", tib: "bod", mao: "mri" };
Object.keys(ALIASES).forEach(function (k) { ALIASES[ALIASES[k]] = k; });

function fold(text) { return String(text || "").normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase(); }

function langMatches(code, query) {
  return fold([langName(code), code, ALIASES[code] || "", language(code)].join(" ")).indexOf(query) >= 0;
}

var combo = { open: false, query: "", active: "" };

function comboEls() {
  return { box: document.getElementById("lang-combo"), input: document.getElementById("lang-input"),
    list: document.getElementById("lang-list"), count: document.getElementById("lang-count") };
}

// The options in display order: recent picks first (only before typing), then the rest with films to fix
// by name, then the languages every film already has.
function comboOptions(codes) {
  var query = fold(combo.query.trim());
  var counts = {};
  codes.forEach(function (c) { counts[c] = missingCount(c); });
  var byUse = function (a, b) { return (counts[b] > 0) - (counts[a] > 0) || langName(a).localeCompare(langName(b)); };
  if (query) return { counts: counts, groups: [{ codes: codes.filter(function (c) { return langMatches(c, query); }).sort(byUse) }] };
  var recent = recentLangs().filter(function (c) { return codes.indexOf(c) >= 0; });
  var rest = codes.filter(function (c) { return recent.indexOf(c) < 0; }).sort(byUse);
  return { counts: counts, groups: recent.length ? [{ head: "Recent", codes: recent }, { head: "All languages", codes: rest }] : [{ codes: rest }] };
}

function renderCombo(codes) {
  var e = comboEls();
  e.input.disabled = !codes.length;
  var missing = dubLang ? missingCount(dubLang) : 0;
  e.count.textContent = dubLang ? String(missing) : "";
  e.count.setAttribute("aria-label", plural(missing, "film", "films") + " without it");
  if (!combo.open) {
    var name = dubLang ? langName(dubLang) : "No languages yet";
    if (e.input.value !== name) e.input.value = name;
    return;
  }
  var options = comboOptions(codes), order = [];
  var scroll = e.list.scrollTop;
  e.list.textContent = "";
  options.groups.forEach(function (g) {
    if (g.head && g.codes.length) e.list.appendChild(el("li", "head", g.head)).setAttribute("role", "presentation");
    g.codes.forEach(function (code) {
      var n = options.counts[code];
      var li = el("li", n ? "" : "none");
      li.id = "lang-opt-" + code;
      li.setAttribute("role", "option");
      li.setAttribute("aria-selected", String(code === dubLang));
      li.appendChild(el("span", "name", langName(code)));
      li.appendChild(el("span", "code", code));
      var hits = (dub.available_counts || {})[code];
      if (hits) li.appendChild(el("span", "hits", hits + " available")).title = plural(hits, "film has", "films have") + " a release in it";
      li.appendChild(el("span", "n", String(n)));
      li.onclick = function () { chooseLang(code); };
      e.list.appendChild(li);
      order.push(code);
    });
  });
  if (!order.length) e.list.appendChild(el("li", "nothing", "No language matches."));
  if (order.indexOf(combo.active) < 0) combo.active = order[0] || "";
  combo.order = order;
  e.list.scrollTop = scroll;
  markActive(false);
}

// reveal scrolls the active option into view; a poll must not, or it undoes the user's own scrolling.
function markActive(reveal) {
  var e = comboEls();
  Array.prototype.forEach.call(e.list.querySelectorAll("li[role=option]"), function (li) {
    li.classList.toggle("active", li.id === "lang-opt-" + combo.active);
  });
  var active = combo.active && document.getElementById("lang-opt-" + combo.active);
  if (active) {
    e.input.setAttribute("aria-activedescendant", active.id);
    if (!reveal) return;
    var top = active.offsetTop, bottom = top + active.offsetHeight;
    if (top < e.list.scrollTop) e.list.scrollTop = top - 4;
    else if (bottom > e.list.scrollTop + e.list.clientHeight) e.list.scrollTop = bottom - e.list.clientHeight + 4;
  } else {
    e.input.removeAttribute("aria-activedescendant");
  }
}

function openCombo() {
  if (combo.open || !dub) return;
  var e = comboEls();
  combo.open = true;
  combo.query = "";
  combo.active = dubLang;
  e.box.setAttribute("data-open", "");
  e.input.setAttribute("aria-expanded", "true");
  e.list.hidden = false;
  e.list.scrollTop = 0;
  e.input.select();
  renderDub();
  markActive(true);
}

function closeCombo() {
  if (!combo.open) return;
  var e = comboEls();
  combo.open = false;
  combo.query = "";
  e.box.removeAttribute("data-open");
  e.input.setAttribute("aria-expanded", "false");
  e.input.removeAttribute("aria-activedescendant");
  e.list.hidden = true;
  renderDub();
}

function chooseLang(code) {
  dubLang = code;
  store(LANG_KEY, code);
  store(RECENT_KEY, JSON.stringify([code].concat(recentLangs().filter(function (c) { return c !== code; })).slice(0, 3)));
  closeCombo();
  renderDub();
  refresh();
}

(function () {
  var e = comboEls();
  e.input.addEventListener("focus", openCombo);
  e.input.addEventListener("click", openCombo);
  e.input.addEventListener("blur", closeCombo);
  // Keeps the focus in the input, so a click on an option or the list's scrollbar does not close it first.
  e.list.addEventListener("mousedown", function (ev) { ev.preventDefault(); });
  e.input.addEventListener("input", function () {
    if (!combo.open) openCombo();
    combo.query = e.input.value;
    combo.active = "";
    e.list.scrollTop = 0;
    renderDub();
  });
  e.input.addEventListener("keydown", function (ev) {
    var order = combo.order || [], i = order.indexOf(combo.active);
    var step = { ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: order.length - 1 }[ev.key];
    if (step !== undefined && (combo.open || ev.key.indexOf("Arrow") === 0)) {
      ev.preventDefault();
      if (!combo.open) { openCombo(); return; }
      if (!order.length) return;
      combo.active = order[Math.max(0, Math.min(order.length - 1, step))];
      markActive(true);
    } else if (ev.key === "Enter") {
      if (combo.open && combo.active) { ev.preventDefault(); chooseLang(combo.active); }
      else if (!combo.open) { ev.preventDefault(); openCombo(); }
    } else if (ev.key === "Escape") {
      if (combo.open) { ev.preventDefault(); closeCombo(); }
    } else if (ev.key === "Tab") {
      closeCombo();
    }
  });
})();

function renderDub() {
  if (!dub) return;
  var codes = Object.keys(dub.languages || {}).sort(function (a, b) { return langName(a).localeCompare(langName(b)); });
  if (codes.indexOf(dubLang) < 0) {
    var saved = stored(LANG_KEY);
    dubLang = codes.indexOf(saved) >= 0 ? saved : codes.indexOf("spa") >= 0 ? "spa" : (codes[0] || "");
  }
  renderCombo(codes);

  var pending = dubPending();
  var jobs = document.getElementById("dub-jobs"), list = dub.jobs || [];
  jobs.textContent = "";
  var running = list.filter(function (j) { return dubActive(j.status); }).length +
    (dub.requests || []).filter(function (r) { return !r.cancel; }).length;
  document.getElementById("dub-count").textContent = running ? running + " under way" : "";
  document.getElementById("badge-add").textContent = running ? String(running) : "";
  var requestedOnly = (dub.requests || []).filter(function (r) {
    return !r.cancel && !list.some(function (j) { return j.movie_id === r.movie_id && j.lang === r.lang && dubActive(j.status); });
  });
  requestedOnly.forEach(function (r) {
    var film = (dub.films || []).filter(function (f) { return f.id === r.movie_id; })[0];
    jobs.appendChild(dubJobRow({ movie_id: r.movie_id, lang: r.lang, status: "requested", updated: r.requested,
      title: film ? film.title + (film.year ? " (" + film.year + ")" : "") : "" }, pending));
  });
  list.forEach(function (j) { jobs.appendChild(dubJobRow(j, pending)); });
  document.getElementById("dub-jobs-group").hidden = !jobs.childNodes.length;

  var filter = document.getElementById("dub-filter");
  var needle = fold(filter.value.trim());
  var missing = (dub.films || []).filter(function (f) { return (f.missing || []).indexOf(dubLang) >= 0; });
  var availableCount = missing.filter(function (f) { return isAvailable(availabilityOf(f.id)); }).length;
  var onlyAvailable = showAvailable && dub.lang === dubLang;
  var shown = missing.filter(function (f) {
    return (!needle || fold(f.title).indexOf(needle) >= 0) && (!onlyAvailable || isAvailable(availabilityOf(f.id)));
  });
  // Named first, then from a site in the language, then the rest, each in Radarr's order.
  var rankOf = function (f) { var a = availabilityOf(f.id); return a && a.named ? 0 : a && a.own_site ? 1 : 2; };
  shown = shown.map(function (f, i) { return [rankOf(f), i, f]; })
    .sort(function (a, b) { return a[0] - b[0] || a[1] - b[1]; }).map(function (x) { return x[2]; });
  document.getElementById("show-available").lastChild.textContent = dub.lang === dubLang ? String(availableCount) : "";
  ["all", "available"].forEach(function (name) {
    document.getElementById("show-" + name).setAttribute("aria-pressed", String((name === "available") === showAvailable));
  });
  filter.placeholder = missing.length ? "Search " + plural(missing.length, "film", "films") + " without " + langName(dubLang) : "Search films";
  document.getElementById("dub-matches").textContent = (needle || onlyAvailable) && missing.length ? shown.length + " of " + missing.length + " shown" : "";
  document.getElementById("dub-when").textContent = dub.error ? dub.error : dub.time ? "listed " + when(dub.time) : "not listed yet";
  document.getElementById("dub-sweep").textContent = sweepLine(dub.sweep);
  var films = document.getElementById("dub-films");
  films.textContent = "";
  if (!shown.length) films.appendChild(el("p", "empty", !dub.time ? "Radarr has not listed the films yet." :
    needle ? "No title matches." : onlyAvailable && missing.length ? "No film has a release in " + langName(dubLang) + " yet." :
    "Every film has this language."));
  else shown.forEach(function (f) { films.appendChild(dubFilmRow(f, pending)); });
}

document.getElementById("dub-filter").oninput = renderDub;

var SHOW_KEY = "audio-manager.show", showAvailable = stored(SHOW_KEY) === "available";

["all", "available"].forEach(function (name) {
  document.getElementById("show-" + name).onclick = function () {
    showAvailable = name === "available";
    store(SHOW_KEY, name);
    renderDub();
  };
});

function sweepLine(sweep) {
  if (!sweep || !sweep.total) return "";
  var line = "Searched " + sweep.searched + " of " + plural(sweep.total, "film", "films");
  var sweeping = Object.keys(dub.searching || {}).filter(function (id) { return dub.searching[id].sweep; })[0];
  if (sweeping) return line + ", searching one now";
  if (sweep.next_at) line += ", next " + until(sweep.next_at);
  return line;
}

function route() {
  var view = location.hash === "#reencode" ? "reencode" : "add";
  ["add", "reencode"].forEach(function (name) { document.getElementById("view-" + name).hidden = name !== view; });
  Array.prototype.forEach.call(document.querySelectorAll(".tabs a"), function (a) {
    if (a.dataset.view === view) a.setAttribute("aria-current", "page"); else a.removeAttribute("aria-current");
  });
}

window.addEventListener("hashchange", route);
route();

var timer = null;

function refresh() {
  var lang = dubLang || stored(LANG_KEY);
  return fetch("/api/state" + (lang ? "?lang=" + encodeURIComponent(lang) : "")).then(function (r) { return r.json(); }).then(function (d) {
    if (d.error) say(d.error);
    targets = (d.library && d.library.targets) || {};
    defaultTarget = (d.library && d.library.default_target) || "ac3";
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
    document.getElementById("badge-reencode").textContent = todo ? String(todo) : "";
    if (!items.length) list.appendChild(el("p", "empty", "Nothing yet. A file shows up here when a client has its audio re-encoded."));
    else items.forEach(function (it) { list.appendChild(item(it, d)); });

    var lib = d.library || {}, libItems = lib.items || [];
    var libList = document.getElementById("library");
    libList.textContent = "";
    var pendingLib = libItems.filter(function (it) { return it.status !== "done" && it.status !== "missing"; }).length;
    document.getElementById("library-count").textContent = pendingLib ? pendingLib + " would be converted" : "";
    document.getElementById("library-when").textContent = lib.error ? lib.error :
      lib.time ? "checked " + when(lib.time) : "not checked yet";
    if (!libItems.length) libList.appendChild(el("p", "empty", lib.time ? "Every file in the library has audio the TV plays." :
      "The first check of the library runs when the app starts."));
    else libItems.forEach(function (it) { libList.appendChild(item(it, d)); });

    var hist = document.getElementById("history");
    hist.textContent = "";
    if (!d.history || !d.history.length) hist.appendChild(el("p", "empty", "Nothing converted yet."));
    else d.history.forEach(function (h) { hist.appendChild(historyRow(h)); });

    dub = d.add_audio || {};
    renderDub();
    // The first load asks before the page knows its language; ask again for the one it picked.
    if (dubLang && dub.lang !== dubLang) { refresh(); return; }
    watchSearches();

    clearTimeout(timer);
    timer = setTimeout(tick, d.running || (d.queue && d.queue.length) ? 1000 : 10000);
  }).catch(function (e) { say(String(e)); clearTimeout(timer); timer = setTimeout(tick, 5000); });
}

function tick() { if (document.hidden) { timer = setTimeout(tick, 2000); return; } refresh(); }

refresh();
