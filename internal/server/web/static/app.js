// Titra dashboard. Vanilla JS, no build step. Loads runs over the JSON API and
// stays live over a Server-Sent Events stream, so blocked actions appear the
// instant the guard reports them.

const $ = (s, el = document) => el.querySelector(s);
const state = { runs: new Map(), selected: null };

function esc(s) { return (s ?? "").replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c])); }
function cmd(a) { return [a.tool, ...(a.args || [])].join(" "); }
function ago(t) {
  const s = Math.max(0, (Date.now() - new Date(t)) / 1000);
  if (s < 60) return `${s | 0}s ago`;
  if (s < 3600) return `${(s / 60) | 0}m ago`;
  if (s < 86400) return `${(s / 3600) | 0}h ago`;
  return new Date(t).toLocaleDateString();
}

async function getJSON(u) { const r = await fetch(u); if (!r.ok) throw new Error(r.status); return r.json(); }

async function init() {
  try {
    const me = await getJSON("/api/me");
    renderUser(me);
  } catch (_) {}
  await refreshTotals();
  await refreshRuns();
  connectStream();
  setInterval(refreshTotals, 15000);
}

function renderUser(me) {
  const el = $("#user");
  if (me.user) el.innerHTML = `<img src="${esc(me.user.avatar)}" alt=""> ${esc(me.user.login)} · <a href="/auth/logout">sign out</a>`;
  else if (me.app_configured) el.innerHTML = `<a href="/auth/login">sign in</a>`;
  else el.innerHTML = `<a href="/setup">set up github app</a>`;
}

async function refreshTotals() {
  try {
    const t = await getJSON("/api/totals");
    $("#totals").hidden = false;
    $("#t-runs").textContent = t.runs;
    $("#t-actions").textContent = t.actions;
    $("#t-blocked").textContent = t.blocked;
    $("#t-would").textContent = t.would_block;
  } catch (_) {}
}

async function refreshRuns() {
  let runs;
  try { runs = await getJSON("/api/runs"); } catch (_) { return; }
  state.runs.clear();
  runs.forEach(r => state.runs.set(r.key, r));
  renderRuns();
  if (!state.selected && runs.length) selectRun(runs[0].key);
}

function renderRuns() {
  const ul = $("#runs");
  ul.innerHTML = "";
  const runs = [...state.runs.values()].sort((a, b) => new Date(b.last_seen) - new Date(a.last_seen));
  const tmpl = $("#run-tmpl").content;
  for (const r of runs) {
    const li = tmpl.cloneNode(true).firstElementChild;
    li.dataset.key = r.key;
    if (r.key === state.selected) li.classList.add("sel");
    $(".run-repo", li).textContent = r.repo || "local";
    $(".run-mode", li).textContent = r.mode || "";
    $(".run-wf", li).textContent = r.workflow || r.run_id || "";
    bar($(".run-bar", li), r);
    $(".run-meta", li).innerHTML = `<span>${r.total} actions</span><span>${ago(r.last_seen)}</span>`;
    li.onclick = () => selectRun(r.key);
    li.onkeydown = e => { if (e.key === "Enter") selectRun(r.key); };
    ul.appendChild(li);
  }
}

function bar(el, r) {
  const tot = Math.max(1, r.total);
  el.innerHTML =
    `<span class="b-ok" style="width:${(r.allowed / tot) * 100}%"></span>` +
    `<span class="b-would" style="width:${(r.would_block / tot) * 100}%"></span>` +
    `<span class="b-block" style="width:${(r.blocked / tot) * 100}%"></span>`;
}

async function selectRun(key) {
  state.selected = key;
  for (const li of document.querySelectorAll(".run")) li.classList.toggle("sel", li.dataset.key === key);
  let data;
  try { data = await getJSON("/api/runs/" + encodeURIComponent(key)); } catch (_) { return; }
  renderDetail(data.run, data.actions || []);
}

function renderDetail(run, actions) {
  const d = $("#detail");
  const blocked = actions.filter(a => a.verdict === "block").length;
  const would = actions.filter(a => a.verdict === "would-block").length;
  const allowed = actions.length - blocked - would;

  let banner = `<div class="verdict clean">No risky actions in this run.</div>`;
  if (blocked) banner = `<div class="verdict blocked">${blocked} action${blocked > 1 ? "s" : ""} blocked before running.</div>`;
  else if (would) banner = `<div class="verdict would">${would} action${would > 1 ? "s" : ""} would be blocked — monitor mode.</div>`;

  d.innerHTML = `
    <div class="detail-head">
      <div class="detail-title">
        <h3>${esc(run.repo || "local")}</h3>
        <span class="run-mode">${esc(run.mode || "")}</span>
      </div>
      <div class="detail-sub">${esc(run.workflow || "job")}${run.actor ? " · triggered by " + esc(run.actor) : ""}${run.run_id ? " · run " + esc(run.run_id) : ""}</div>
      ${banner}
    </div>
    <div class="detail-stats">
      <div class="dstat"><b>${actions.length}</b><span>actions</span></div>
      <div class="dstat"><b>${allowed}</b><span>allowed</span></div>
      <div class="dstat"><b style="color:var(--terra)">${blocked}</b><span>blocked</span></div>
      <div class="dstat"><b style="color:var(--amber)">${would}</b><span>would-block</span></div>
    </div>
    <div class="timeline" id="timeline"></div>`;

  const tl = $("#timeline");
  // newest first
  for (const a of [...actions].reverse()) tl.appendChild(rowEl(a));
}

function rowEl(a) {
  const row = document.createElement("div");
  const v = a.verdict;
  row.className = "row" + (v === "block" ? " block" : v === "would-block" ? " would" : "");
  const sev = a.severity || "info";
  row.innerHTML =
    `<span class="pill ${v}">${v === "would-block" ? "would" : v}</span>` +
    `<span class="r-tool">${esc(a.tool)}</span>` +
    `<span class="r-cmd" title="${esc(cmd(a))}">${esc(cmd(a))}</span>` +
    `<span class="r-rule">${esc(a.rule_id || "")}${a.rule_id ? `<span class="sev ${sev}">${sev}</span>` : ""}</span>`;
  return row;
}

function connectStream() {
  const live = $("#live");
  const es = new EventSource("/api/stream");
  es.onopen = () => live.classList.replace("off", "on");
  es.onerror = () => live.classList.replace("on", "off");
  es.addEventListener("action", e => {
    let a; try { a = JSON.parse(e.data); } catch (_) { return; }
    onAction(a);
  });
}

function onAction(a) {
  // update the run aggregate
  let r = state.runs.get(a.key);
  if (!r) { refreshRuns(); return; }
  r.total++;
  if (a.verdict === "block") r.blocked++;
  else if (a.verdict === "would-block") r.would_block++;
  else r.allowed++;
  r.last_seen = a.received || new Date().toISOString();
  renderRuns();

  // totals
  const bump = (id, d = 1) => { const el = $(id); if (el) el.textContent = (+el.textContent) + d; };
  bump("#t-actions");
  if (a.verdict === "block") bump("#t-blocked");
  if (a.verdict === "would-block") bump("#t-would");

  // if the open run, prepend the row live
  if (a.key === state.selected) {
    const tl = $("#timeline");
    if (tl) {
      const row = rowEl(a);
      row.classList.add("flash");
      tl.insertBefore(row, tl.firstChild);
    }
  }
}

init();
