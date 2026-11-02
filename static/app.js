// Titra dashboard. Vanilla JS, no build step. Runs stay live over a
// Server-Sent Events stream; Coverage, Policy and Setup are per-org views.

const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const state = { me: null, account: "", view: "runs", repos: [], repo: "", only: undefined, runs: new Map(), selected: null, deep: false, es: null, covTimer: null, refreshTimer: null };

function esc(s) { return String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])); }
function cmd(a) { return [a.tool, ...(a.args || [])].join(" "); }
function ago(t) {
  const s = Math.max(0, (Date.now() - new Date(t)) / 1000);
  if (s < 60) return `${s | 0}s ago`;
  if (s < 3600) return `${(s / 60) | 0}m ago`;
  if (s < 86400) return `${(s / 3600) | 0}h ago`;
  return new Date(t).toLocaleDateString();
}
function plural(n, one, many) { return n === 1 ? one : many; }

async function api(method, url, body) {
  const opt = { method, headers: {} };
  if (method !== "GET") opt.headers["X-Titra"] = "1";
  if (body !== undefined) { opt.headers["Content-Type"] = "application/json"; opt.body = JSON.stringify(body); }
  const r = await fetch(url, opt);
  const text = await r.text();
  if (!r.ok) throw Object.assign(new Error(text.trim() || r.statusText), { status: r.status });
  return text ? JSON.parse(text) : null;
}
const getJSON = (u) => api("GET", u);
const acctQ = () => (state.account ? "?account=" + encodeURIComponent(state.account) : "");
const current = () => (state.me?.accounts || []).find(a => a.account.toLowerCase() === state.account);

// --- boot ---------------------------------------------------------------------

async function init() {
  try { state.me = await getJSON("/api/me"); } catch (_) { state.me = { accounts: [] }; }
  renderUser();
  const params = new URLSearchParams(location.search);
  const deepRun = params.get("run");
  const accts = state.me.accounts || [];
  state.account = (deepRun && deepRun.split("|")[0]) || localStorage.getItem("titra.acct") || (accts[0]?.account || "");
  state.account = state.account.toLowerCase();
  if (accts.length && !accts.some(a => a.account.toLowerCase() === state.account)) state.account = accts[0].account.toLowerCase();
  renderAccounts();

  $$("#tabs button").forEach(b => b.onclick = () => show(b.dataset.view));
  $("#acct").onchange = (e) => { state.account = e.target.value; try { localStorage.setItem("titra.acct", state.account); } catch (_) {} state.repo = ""; state.only = undefined; state.selected = null; renderAccounts(); show(state.view, true); };
  $("#scan").onclick = startScan;
  $("#pol-save").onclick = savePolicy;
  $$("#chips button").forEach(b => b.onclick = () => setFilter(b.dataset.only));
  $("#t-review-btn").onclick = () => { state.repo = "*"; setFilter("needs-review"); renderRepos(); };
  $("#repo-q").oninput = renderRepos;

  // ?run= opens that run (and its repo) whatever the filter; ?repo= a repo.
  if (deepRun) {
    state.selected = deepRun; state.deep = true; state.only = "";
    try { state.repo = (await getJSON("/api/runs/" + encodeURIComponent(deepRun))).run.repo || ""; } catch (_) {}
  } else if (params.get("repo")) state.repo = params.get("repo");
  show("runs", true);
  setInterval(() => { if (state.view === "runs") refreshTotals(); }, 15000);
}

function renderUser() {
  const me = state.me, el = $("#user");
  if (me.user) el.innerHTML = `<img src="${esc(me.user.avatar_url)}" alt=""> ${esc(me.user.login)} · <a href="/auth/logout">sign out</a>`;
  else if (me.app_configured) el.innerHTML = `<a href="/auth/login">sign in</a>`;
  else el.innerHTML = `<span class="muted">local mode</span>`;
}

function renderAccounts() {
  const accts = state.me.accounts || [];
  const wrap = $("#acct-wrap");
  wrap.hidden = accts.length === 0;
  $("#acct").innerHTML = accts.map(a => `<option value="${esc(a.account.toLowerCase())}">${esc(a.account)}</option>`).join("");
  $("#acct").value = state.account;
  const a = current();
  const plan = $("#plan");
  plan.textContent = a ? a.plan_status : "";
  plan.className = "plan " + (a ? (a.plan === "pro" ? "pro" : a.active ? "trial" : "ended") : "");
  $$("#tabs .paid").forEach(b => b.hidden = !a);

  const banner = $("#banner");
  if (state.me.app_configured && state.me.authed && accts.length === 0) {
    banner.hidden = false;
    banner.innerHTML = `Titra isn't installed on any org you belong to yet. <a href="${esc(state.me.install_url || "/github/app/install")}">Install the GitHub App →</a> then <a href="/auth/logout">sign in again</a>.`;
  } else if (a && !a.active) {
    banner.hidden = false;
    banner.innerHTML = `<b>${esc(a.account)}'s trial has ended.</b> Runs still stream here, but PR checks, org policy and coverage are paused. Upgrade to Titra Pro to turn them back on.`;
  } else banner.hidden = true;
}

function show(view, force) {
  if (state.view === view && !force) return;
  state.view = view;
  $$("#tabs button").forEach(b => b.classList.toggle("on", b.dataset.view === view));
  $$(".view").forEach(v => v.hidden = v.id !== "view-" + view);
  clearTimeout(state.covTimer);
  if (view === "runs") { refreshTotals(); refreshRuns(); connectStream(); }
  if (view === "coverage") loadCoverage();
  if (view === "policy") loadPolicy();
  if (view === "setup") loadSetup();
}

// --- runs ---------------------------------------------------------------------
//
// Three levels so a big org isn't a wall of runs: repos (needing attention
// first) → that repo's runs (filtered to "needs review" by default) → one run,
// where what Titra stopped comes first and the routine actions are folded.

const RUN_LIMIT = 200;

async function refreshTotals() {
  try {
    const t = await getJSON("/api/totals" + acctQ());
    $("#totals").hidden = false;
    $("#t-repos").textContent = t.repos;
    $("#t-runs").textContent = t.runs;
    $("#t-review").textContent = t.needs_review;
    $("#t-review").classList.toggle("block", t.needs_review > 0);
    $("#t-blocked").textContent = t.blocked_runs;
    $("#t-exposed").textContent = t.exposed;
  } catch (_) {}
}

// refreshRuns reloads repos and the visible run list. Called on boot, on
// account change and (throttled) when the live stream reports a new run.
async function refreshRuns() {
  let repos;
  try { repos = await getJSON("/api/repos" + acctQ()); } catch (_) { return; }
  state.repos = repos || [];
  if (state.repo && state.repo !== "*" && !state.repos.some(r => r.repo === state.repo)) state.repo = "";
  if (!state.repo) state.repo = state.repos.length > 1 ? "*" : (state.repos[0]?.repo || "*");
  if (state.only === undefined) state.only = defaultFilter();
  renderRepos();
  await loadRuns();
}

// With anything waiting on a maintainer, open on that; otherwise show all.
function defaultFilter() {
  const r = repoSummary();
  return r && r.needs_review > 0 ? "needs-review" : "";
}

function repoSummary() {
  return state.repo === "*" ? allRepos() : state.repos.find(r => r.repo === state.repo);
}

function renderRepos() {
  const ul = $("#repos");
  const q = ($("#repo-q").value || "").toLowerCase();
  const items = [];
  if (state.repos.length > 1 && !q) items.push({ ...allRepos(), all: true });
  for (const r of state.repos) if (!q || r.repo.toLowerCase().includes(q)) items.push(r);
  ul.innerHTML = "";
  if (!state.repos.length) {
    ul.innerHTML = `<li class="muted small none">No runs yet.</li>`;
    return;
  }
  for (const r of items) {
    const li = document.createElement("li");
    const key = r.all ? "*" : r.repo;
    li.className = "repo" + (key === state.repo ? " sel" : "") + (r.needs_review ? " attn" : "");
    li.tabIndex = 0;
    const name = r.all ? "All repos" : r.repo.split("/").pop();
    const owner = r.all ? `${state.repos.length} repos` : r.repo.split("/").slice(0, -1).join("/");
    const badge = r.needs_review
      ? `<span class="count block" title="runs that need review">${r.needs_review}</span>`
      : `<span class="count ok" title="nothing needs review">✓</span>`;
    li.innerHTML = `
      <div class="repo-top"><span class="repo-name" title="${esc(r.repo)}">${esc(name)}</span>${badge}</div>
      <div class="repo-meta muted small"><span>${esc(owner)}</span><span>${r.runs} ${plural(r.runs, "run", "runs")}${r.last_seen ? " · " + ago(r.last_seen) : ""}</span></div>`;
    li.onclick = () => pickRepo(key);
    li.onkeydown = e => { if (e.key === "Enter") pickRepo(key); };
    ul.appendChild(li);
  }
  const sel = $(".repo.sel", ul);
  if (sel) try { sel.scrollIntoView({ block: "nearest", inline: "nearest" }); } catch (_) {}
}

function allRepos() {
  const all = { repo: "*", runs: 0, needs_review: 0, last_seen: "" };
  for (const r of state.repos) {
    all.runs += r.runs; all.needs_review += r.needs_review;
    if (!all.last_seen || new Date(r.last_seen) > new Date(all.last_seen)) all.last_seen = r.last_seen;
  }
  return all;
}

function pickRepo(repo) {
  if (state.repo === repo) return;
  state.repo = repo;
  state.only = defaultFilter();
  state.selected = null;
  syncURL();
  renderRepos();
  loadRuns();
}

function setFilter(only) {
  state.only = only;
  state.selected = null;
  syncURL();
  loadRuns();
}

async function loadRuns() {
  const q = new URLSearchParams();
  if (state.account) q.set("account", state.account);
  if (state.repo && state.repo !== "*") q.set("repo", state.repo);
  if (state.only) q.set("only", state.only);
  q.set("limit", RUN_LIMIT);
  let runs;
  try { runs = await getJSON("/api/runs?" + q); } catch (_) { return; }
  state.runs.clear();
  (runs || []).forEach(r => state.runs.set(r.key, r));
  renderRuns();
  if (state.selected && state.runs.has(state.selected)) selectRun(state.selected);
  else if (state.selected && state.deep) selectRun(state.selected); // deep link to a run outside the filter
  else if (runs.length) selectRun(runs[0].key);
  else $("#detail").innerHTML = emptyDetail();
}

function emptyDetail() {
  if (!state.repos.length) return `<div class="empty"><p class="empty-mark">⌁</p><p>No runs yet.</p><p class="muted small">Add the Titra Action with this org's token — see Setup.</p></div>`;
  if (state.only === "needs-review") return `<div class="empty"><p class="empty-mark ok">✓</p><p>Nothing needs review here.</p><p class="muted small">Every blocked run has been approved or there were none. <a href="#" id="show-all">Show all runs</a></p></div>`;
  return `<div class="empty"><p class="empty-mark">⌁</p><p>No runs match this filter.</p></div>`;
}

function renderRuns() {
  const sum = repoSummary() || { runs: 0 };
  $("#runs-title").textContent = state.repo === "*" ? "All runs" : (state.repo || "Runs");
  $$("#chips button").forEach(b => b.classList.toggle("on", b.dataset.only === (state.only || "")));
  $('#chips button[data-only="needs-review"]').classList.toggle("attn", sum.needs_review > 0);

  const ul = $("#runs");
  ul.innerHTML = "";
  const runs = [...state.runs.values()].sort((a, b) => new Date(b.last_seen) - new Date(a.last_seen));
  let lastDay = "";
  for (const r of runs) {
    const day = dayLabel(r.last_seen);
    if (day !== lastDay) { const h = document.createElement("li"); h.className = "day"; h.textContent = day; ul.appendChild(h); lastDay = day; }
    ul.appendChild(runEl(r));
  }
  const shown = runs.length;
  $("#runs-note").textContent = shown >= RUN_LIMIT ? `Showing the latest ${RUN_LIMIT}. Narrow with a filter or pick a repo.` : "";
  if (!shown) ul.innerHTML = "";
  const link = $("#show-all"); if (link) link.onclick = e => { e.preventDefault(); setFilter(""); };
}

function dayLabel(t) {
  const d = new Date(t), now = new Date();
  const y = new Date(now); y.setDate(now.getDate() - 1);
  if (d.toDateString() === now.toDateString()) return "Today";
  if (d.toDateString() === y.toDateString()) return "Yesterday";
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric", year: d.getFullYear() === now.getFullYear() ? undefined : "numeric" });
}

// One run in one line of meaning: its verdict, what started it, when.
function runEl(r) {
  const li = document.createElement("li");
  li.className = "run" + (r.key === state.selected ? " sel" : "");
  li.dataset.key = r.key;
  li.tabIndex = 0;
  let verdict, cls;
  if (r.approved_by) { verdict = "approved re-run"; cls = "ok"; }
  else if (r.blocked) { verdict = `${r.blocked} blocked`; cls = "block"; }
  else if (r.would_block) { verdict = `${r.would_block} would block`; cls = "would"; }
  else if (r.policy_drift) { verdict = "policy drift"; cls = "would"; }
  else { verdict = "clean"; cls = "clean"; }
  // Dealt with: keep what happened visible, drop the alarm colour.
  if (r.reviewed_by && cls !== "ok" && cls !== "clean") { verdict += " · reviewed"; cls = "done"; }
  const tags = [];
  if (r.exposed) tags.push(`<span class="tag exposed">outsider</span>`);
  if (r.policy_drift && verdict !== "policy drift") tags.push(`<span class="tag drift">drift</span>`);
  const what = [r.workflow || "job", r.number ? "#" + r.number : "", r.actor ? "by " + r.actor : ""].filter(Boolean).join(" · ");
  li.innerHTML = `
    <div class="run-top"><span class="verdict-dot ${cls}">${esc(verdict)}</span><span class="muted small">${ago(r.last_seen)}</span></div>
    <div class="run-wf">${esc(what)}</div>
    ${state.repo === "*" ? `<div class="muted small run-repo">${esc(r.repo || "local")}</div>` : ""}
    ${tags.length ? `<div class="run-tags">${tags.join("")}</div>` : ""}`;
  li.onclick = () => selectRun(r.key);
  li.onkeydown = e => { if (e.key === "Enter") selectRun(r.key); };
  return li;
}

async function selectRun(key) {
  state.selected = key;
  syncURL();
  $$(".run").forEach(li => li.classList.toggle("sel", li.dataset.key === key));
  let data;
  try { data = await getJSON("/api/runs/" + encodeURIComponent(key)); } catch (_) { return; }
  if (state.selected !== key) return; // a later click won
  state.deep = false;
  renderDetail(data.run, data.actions || [], data.report);
}

function syncURL() {
  const q = new URLSearchParams();
  if (state.repo && state.repo !== "*") q.set("repo", state.repo);
  if (state.selected) q.set("run", state.selected);
  const url = location.pathname + (q.toString() ? "?" + q : "");
  try { history.replaceState(null, "", url); } catch (_) {}
}

// group splits a run's actions into what a maintainer must look at and what
// they can skip: stopped actions first, then the agent's own tool calls, then
// network, then the rest of the job (setup, installs, builds).
function group(actions) {
  const g = { stopped: [], agent: [], network: [], other: [] };
  for (const a of actions) {
    if (a.verdict === "block" || a.verdict === "would-block") g.stopped.push(a);
    else if (a.source === "network") g.network.push(a);
    else if (a.source && a.source !== "shim") g.agent.push(a);
    else g.other.push(a);
  }
  return g;
}

function renderDetail(run, actions, rep) {
  const story = rep?.story;
  if (!actions.length && story) actions = story.actions || [];
  // Network blocks come only with the final report, never the live stream.
  if (story) {
    const seen = new Set(actions.map(a => a.verdict + " " + cmd(a)));
    for (const a of [...(story.blocked || []), ...(story.would_block || [])]) {
      if (!seen.has(a.verdict + " " + cmd(a))) { actions = actions.concat(a); seen.add(a.verdict + " " + cmd(a)); }
    }
  }
  const g = group(actions);
  const blocked = g.stopped.filter(a => a.verdict === "block").length;
  const would = g.stopped.length - blocked;
  const ctx0 = c0(rep);

  let banner = `<div class="verdict clean">No risky actions in this run.</div>`;
  if (blocked) banner = `<div class="verdict blocked">${blocked} ${plural(blocked, "action", "actions")} blocked before running.</div>`;
  else if (would) banner = `<div class="verdict would">${would} ${plural(would, "action", "actions")} would be blocked — monitor mode.</div>`;
  if (ctx0.approved_by) banner = `<div class="verdict ok">Approved by ${esc(ctx0.approved_by)} with the <code>${esc(ctx0.approve_label || "titra:approve")}</code> label — Titra watched this run instead of blocking it.</div>`;
  const canApprove = blocked && !ctx0.approved_by && ctx0.number && state.me.app_configured;
  if (canApprove) banner += `<div class="approve"><button class="btn sm-btn" id="approve">Approve &amp; re-run</button>
    <span class="muted small">Adds <code>${esc(ctx0.approve_label || "titra:approve")}</code> to #${esc(ctx0.number)}. The workflow re-runs once with Titra watching, not blocking.</span></div>`;
  if (rep?.policy_drift) banner += `<div class="verdict blocked">This run did not apply the org's locked policy (used: ${esc(rep.policy_source || "its own")}).</div>`;
  if (run.reviewed_by) banner += `<div class="reviewed">✓ ${run.review_note ? esc(run.reviewed_by) + " " + esc(run.review_note) : "Reviewed by " + esc(run.reviewed_by)}${run.reviewed_at ? " · " + ago(run.reviewed_at) : ""} <button class="linkish" id="reopen">Reopen</button></div>`;
  else if (run.needs_review) banner += `<div class="approve"><button class="btn-ghost sm" id="review">Mark reviewed</button>
    <span class="muted small">The block was right, or you've checked it. Moves this run out of “needs review”; it stays in the history.</span></div>`;

  const c = story?.context || {};
  const ctx = story ? `
    <dl class="ctx">
      <dt>Triggered by</dt><dd>${esc(c.actor || "—")} ${c.trusted ? `<span class="tag ok">${esc(c.trust_note || "trusted")}</span>` : `<span class="tag exposed">${esc(c.trust_note || "untrusted")}</span>`}</dd>
      <dt>Event</dt><dd>${esc(c.event || "—")}${c.subject ? " · " + esc(c.subject) : ""}</dd>
      ${c.untrusted_input?.length ? `<dt>Untrusted input</dt><dd>${c.untrusted_input.map(esc).join(", ")}</dd>` : ""}
      <dt>Agents</dt><dd>${(c.agents || []).map(esc).join(", ") || "—"}</dd>
      <dt>Token can write</dt><dd>${writes(c)}</dd>
      ${rep.check_url ? `<dt>PR check</dt><dd><a href="${esc(rep.check_url)}" target="_blank" rel="noopener">View on GitHub →</a></dd>` : ""}
      ${c.run_url ? `<dt>Workflow run</dt><dd><a href="${esc(c.run_url)}" target="_blank" rel="noopener">Open in Actions →</a></dd>` : ""}
    </dl>` : "";

  const findings = story ? traceFindings(story) : "";
  const section = (id, title, list, open, note) => list.length ? `
    <details class="group" id="g-${id}" ${open ? "open" : ""}>
      <summary><span class="g-title">${title}</span><span class="g-count">${list.length}</span>${note ? `<span class="muted small g-note">${note}</span>` : ""}</summary>
      <div class="timeline"></div>
    </details>` : "";

  $("#detail").innerHTML = `
    <div class="detail-head">
      <div class="detail-title">
        <h3>${esc(run.repo || "local")}</h3>
        <span class="run-mode">${esc(run.mode || "")}</span>
      </div>
      <div class="detail-sub">${esc(run.workflow || "job")}${run.run_id ? " · run " + esc(run.run_id) : ""} · ${ago(run.last_seen)}</div>
      ${banner}
      ${ctx}
    </div>
    ${findings}
    ${section("stopped", "Stopped by Titra", g.stopped, true)}
    ${section("agent", "Agent tool calls", g.agent, !g.stopped.length && g.agent.length <= 15, "what the AI agent itself ran")}
    ${section("network", "Network", g.network, false)}
    ${section("other", "Rest of the job", g.other, false, "setup, installs, builds — allowed")}
    ${actions.length ? "" : `<p class="muted small pad">No actions recorded.</p>`}`;

  for (const [id, list] of Object.entries(g)) {
    const tl = $(`#g-${id} .timeline`);
    if (tl) for (const a of [...list].reverse()) tl.appendChild(rowEl(a));
  }

  const mark = (method) => async () => {
    try {
      await api(method, "/api/runs/" + encodeURIComponent(run.key) + "/review");
      // Triage flow: in "needs review", reviewing a run moves on to the next one.
      if (method === "POST" && state.only === "needs-review") state.selected = null;
      else state.deep = true; // keep this run open even if it now falls outside the filter
      await Promise.all([refreshTotals(), refreshRuns()]);
    } catch (e) { alert(e.message); }
  };
  const rv = $("#review"); if (rv) rv.onclick = mark("POST");
  const ro = $("#reopen"); if (ro) ro.onclick = mark("DELETE");

  const btn = $("#approve");
  if (btn) btn.onclick = async () => {
    if (!confirm(`Approve one re-run of ${run.repo}#${ctx0.number}? Titra will watch it instead of blocking.`)) return;
    btn.disabled = true;
    try {
      await api("POST", "/api/runs/" + encodeURIComponent(run.key) + "/approve");
      btn.textContent = "Approved — re-run starting";
      refreshTotals(); refreshRuns();
    } catch (e) { btn.disabled = false; alert(e.message); }
  };
}

function c0(rep) { return rep?.story?.context || {}; }

function writes(c) {
  const p = c.permissions || {};
  const w = Object.keys(p).filter(k => p[k] === "write");
  if (w.length) return `<span class="tag exposed">${w.map(esc).join(", ")}</span>`;
  if (!Object.keys(p).length) return `<span class="muted">${esc(c.permissions_note || "unknown")}</span>`;
  return "read-only";
}

function traceFindings(s) {
  const parts = [];
  const unexpected = (s.egress || []).filter(d => !d.expected);
  if (s.bypasses?.length) parts.push(`<h4>Ran around the guard</h4><ul>${s.bypasses.map(b => `<li><code>${esc(b.cmd || b.exe)}</code></li>`).join("")}</ul>`);
  if (unexpected.length) parts.push(`<h4>Unexpected destinations</h4><ul>${unexpected.map(d => `<li><code>${esc(d.host || d.ip)}:${d.port}</code> <span class="muted">by ${esc((d.procs || []).join(", "))}</span></li>`).join("")}</ul>`);
  if (s.credential_reads?.length) parts.push(`<h4>Credential reads</h4><ul>${s.credential_reads.map(f => `<li><code>${esc(f.path)}</code> <span class="muted">by ${esc(f.proc)}</span></li>`).join("")}</ul>`);
  return parts.length ? `<div class="findings">${parts.join("")}</div>` : "";
}

function rowEl(a) {
  const row = document.createElement("div");
  const v = a.verdict;
  row.className = "row" + (v === "block" ? " block" : v === "would-block" ? " would" : "");
  const sev = a.severity || "info";
  const tool = a.agent_tool ? a.agent_tool : a.source === "network" ? "net" : a.tool;
  row.innerHTML =
    `<span class="pill ${esc(v)}">${v === "would-block" ? "would" : esc(v)}</span>` +
    `<span class="r-tool">${esc(tool)}</span>` +
    `<span class="r-cmd" title="${esc(cmd(a))}">${esc(cmd(a))}</span>` +
    `<span class="r-rule">${esc(a.rule_id || "")}${a.rule_id ? `<span class="sev ${esc(sev)}">${esc(sev)}</span>` : ""}</span>` +
    (a.reason && (v === "block" || v === "would-block") ? `<span class="r-why">${esc(a.reason)}</span>` : "");
  return row;
}

function connectStream() {
  if (state.es) state.es.close();
  const live = $("#live");
  const es = new EventSource("/api/stream" + acctQ());
  state.es = es;
  es.onopen = () => live.classList.replace("off", "on");
  es.onerror = () => live.classList.replace("on", "off");
  es.addEventListener("action", e => {
    let a; try { a = JSON.parse(e.data); } catch (_) { return; }
    onAction(a);
  });
}

// A busy org streams thousands of actions. Only a stopped action or a new run
// changes what a maintainer sees, so routine actions just update the open run;
// list refreshes are batched to at most one every few seconds.
function onAction(a) {
  if (state.view !== "runs") return;
  const stopped = a.verdict === "block" || a.verdict === "would-block";
  const r = state.runs.get(a.key);
  if (r) {
    r.total++;
    if (a.verdict === "block") r.blocked++;
    else if (a.verdict === "would-block") r.would_block++;
    else r.allowed++;
    r.last_seen = a.received || new Date().toISOString();
  }
  if (!r || stopped) scheduleRefresh();
  if (a.key === state.selected) {
    const g = stopped ? "stopped" : a.source === "network" ? "network" : a.source && a.source !== "shim" ? "agent" : "other";
    const tl = $(`#g-${g} .timeline`);
    if (tl) {
      const row = rowEl(a); row.classList.add("flash"); tl.insertBefore(row, tl.firstChild);
      const n = $(`#g-${g} .g-count`); if (n) n.textContent = +n.textContent + 1;
    } else if (stopped) selectRun(a.key);
  }
}

function scheduleRefresh() {
  if (state.refreshTimer) return;
  state.refreshTimer = setTimeout(async () => {
    state.refreshTimer = null;
    await Promise.all([refreshTotals(), refreshRuns()]);
  }, 3000);
}

// --- coverage -------------------------------------------------------------------

async function loadCoverage() {
  const body = $("#cov-body");
  const a = current();
  $("#scan").disabled = !a?.active;
  let cov;
  try { cov = await getJSON(`/api/orgs/${encodeURIComponent(state.account)}/coverage`); }
  catch (e) { body.innerHTML = `<p class="muted">${esc(e.message)}</p>`; return; }
  if (cov.status === "none") {
    body.innerHTML = `<div class="empty-pane"><p>Not scanned yet.</p><p class="muted small">A scan reads the workflow files in every repo the GitHub App can see. Nothing runs.</p></div>`;
    return;
  }
  const running = cov.status === "running";
  $("#scan").textContent = running ? "Scanning…" : "Scan again";
  $("#scan").disabled = running || !a?.active;
  if (running) state.covTimer = setTimeout(loadCoverage, 2500);
  if (cov.status === "error") { body.innerHTML = `<p class="verdict blocked">Scan failed: ${esc(cov.error)}</p>`; return; }
  if (!cov.scanned_at && running) { body.innerHTML = `<div class="empty-pane"><p>Reading workflow files…</p></div>`; return; }

  const stats = `
    <section class="totals flat">
      <div class="stat"><div class="stat-n">${cov.repos_scanned}</div><div class="stat-l">repos scanned</div></div>
      <div class="stat"><div class="stat-n">${cov.repos_with_agents}</div><div class="stat-l">run agents in CI</div></div>
      <div class="stat"><div class="stat-n">${cov.agent_jobs}</div><div class="stat-l">agent jobs</div></div>
      <div class="stat"><div class="stat-n would">${cov.unguarded}</div><div class="stat-l">without Titra</div></div>
      <div class="stat"><div class="stat-n block">${cov.exposed_unguarded}</div><div class="stat-l">unguarded + outsider-triggerable</div></div>
    </section>
    <p class="muted small">Scanned ${ago(cov.scanned_at)}.</p>`;

  const rows = (cov.repos || []).flatMap(r => r.workflows.flatMap(wf => wf.jobs.map(j => `
    <tr class="${j.guarded ? "" : wf.outsider_triggerable ? "risk-hi" : "risk"}">
      <td><b>${esc(r.repo)}</b>${r.private ? ` <span class="muted small">private</span>` : ""}</td>
      <td><code>${esc(wf.path.replace(".github/workflows/", ""))}</code> · ${esc(j.name)}</td>
      <td>${j.agents.map(esc).join(", ")}</td>
      <td>${(wf.triggers || []).map(t => `<span class="tag ${["issues","issue_comment","pull_request_target","pull_request_review","pull_request_review_comment","discussion","discussion_comment"].includes(t) ? "exposed" : ""}">${esc(t)}</span>`).join(" ")}</td>
      <td>${j.guarded ? `<span class="tag ok">guarded</span>` : `<span class="tag exposed">no Titra</span>`}</td>
    </tr>`))).join("");

  body.innerHTML = stats + (rows ? `
    <table class="cov">
      <thead><tr><th>Repo</th><th>Workflow · job</th><th>Agent</th><th>Triggers</th><th>Titra</th></tr></thead>
      <tbody>${rows}</tbody>
    </table>` : `<div class="empty-pane"><p>No AI agents found in any workflow.</p></div>`);
}

async function startScan() {
  $("#scan").disabled = true;
  try { await api("POST", `/api/orgs/${encodeURIComponent(state.account)}/coverage`); }
  catch (e) { alertMsg("#cov-body", e.message); }
  loadCoverage();
}

// --- policy ----------------------------------------------------------------------

async function loadPolicy() {
  let o;
  try { o = await getJSON(`/api/orgs/${encodeURIComponent(state.account)}`); }
  catch (e) { $("#pol-msg").textContent = e.message; return; }
  $("#pol").value = o.policy || o.default_policy;
  $("#pol-lock").checked = !!o.policy_locked;
  const active = o.account.active;
  $("#pol").disabled = $("#pol-lock").disabled = $("#pol-save").disabled = !active;
  $("#pol-msg").textContent = o.policy ? "" : "No org policy yet — this is a starting point. Save it to apply it.";
  $("#pol-msg").className = "small muted";
  $("#pol-meta").textContent = o.policy_updated_by ? `Last saved by ${o.policy_updated_by}, ${ago(o.policy_updated_at)}.` : "";
}

async function savePolicy() {
  const msg = $("#pol-msg");
  try {
    await api("PUT", `/api/orgs/${encodeURIComponent(state.account)}/policy`, { policy: $("#pol").value, locked: $("#pol-lock").checked });
    const ok = $("#pol-lock").checked
      ? "Saved and locked. Every guarded job in this org uses it from its next run."
      : "Saved. Jobs without their own policy use it from their next run.";
    state.me = await getJSON("/api/me");
    renderAccounts();
    await loadPolicy();
    msg.textContent = ok;
    msg.className = "small ok-text";
  } catch (e) {
    msg.textContent = e.message;
    msg.className = "small err-text";
  }
}

// --- setup -----------------------------------------------------------------------

async function loadSetup() {
  const body = $("#setup-body");
  let o;
  try { o = await getJSON(`/api/orgs/${encodeURIComponent(state.account)}`); }
  catch (e) { body.innerHTML = `<p class="muted">${esc(e.message)}</p>`; return; }
  body.innerHTML = `
    <div class="steps">
      <div class="step">
        <h2>1 · Add the org secret</h2>
        <p>In <b>${esc(o.account.account)}</b> → Settings → Secrets and variables → Actions, add an organization secret named <code>TITRA_TOKEN</code>:</p>
        <div class="secret"><code id="tok">${"•".repeat(32)}</code>
          <button class="btn-ghost sm" id="tok-show">Reveal</button>
          <button class="btn-ghost sm" id="tok-copy">Copy</button>
          <button class="btn-ghost sm danger" id="tok-rotate">Rotate</button></div>
        <p class="muted small">This token only lets a job send reports and read the org policy. It can't read the dashboard. Rotating revokes the old one immediately.</p>
      </div>
      <div class="step">
        <h2>2 · Add the Action before your agent step</h2>
<pre><code>- name: Titra
  uses: ChelateSec/titra@v1
  with:
    mode: enforce
    server: ${esc(o.server)}
    token: \${{ secrets.TITRA_TOKEN }}</code></pre>
      </div>
      <div class="step">
        <h2>3 · Run it</h2>
        <p>Actions stream into <b>Runs</b> live. On pull requests, Titra posts a <b>Titra</b> check with the verdict. Use <b>Coverage</b> to find the agent workflows that still need it.</p>
      </div>
    </div>`;
  let token = o.token, shown = false;
  $("#tok-show").onclick = () => { shown = !shown; $("#tok").textContent = shown ? token : "•".repeat(32); $("#tok-show").textContent = shown ? "Hide" : "Reveal"; };
  $("#tok-copy").onclick = async () => { try { await navigator.clipboard.writeText(token); $("#tok-copy").textContent = "Copied"; } catch (_) {} };
  $("#tok-rotate").onclick = async () => {
    if (!confirm("Rotate the token? Jobs using the old one stop reporting until you update the secret.")) return;
    try { token = (await api("POST", `/api/orgs/${encodeURIComponent(state.account)}/token`)).token; shown = true; $("#tok").textContent = token; $("#tok-show").textContent = "Hide"; }
    catch (e) { alert(e.message); }
  };
}

function alertMsg(sel, m) { const el = $(sel); if (el) el.insertAdjacentHTML("afterbegin", `<p class="verdict blocked">${esc(m)}</p>`); }

init();
