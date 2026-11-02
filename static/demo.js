// Static demo: answers the dashboard's API from data.json, read-only.
(function () {
  // Open on the headline attack: an AI triage agent hijacked by a GitHub issue.
  const q0 = new URLSearchParams(location.search);
  if (!q0.has("run") && !q0.has("repo")) {
    q0.set("run", "local|acme/payments-api|52130|Issue Triage");
    history.replaceState(null, "", location.pathname + "?" + q0);
  }
  const ISO = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/;
  let shift = 0;
  const walk = (v, f) => Array.isArray(v) ? v.map(x => walk(x, f))
    : v && typeof v === "object" ? Object.fromEntries(Object.entries(v).map(([k, x]) => [k, walk(x, f)]))
    : f(v);
  const ready = fetch("data.json").then(r => r.json()).then(d => {
    // Make the newest run "a few minutes ago" whenever the page is opened.
    let newest = 0;
    walk(d, v => { if (typeof v === "string" && ISO.test(v)) { const t = Date.parse(v); if (t > newest && t < 4e12) newest = t; } return v; });
    shift = Date.now() - 4 * 60 * 1000 - newest;
    d = walk(d, v => (typeof v === "string" && ISO.test(v) && !v.startsWith("0001")) ? new Date(Date.parse(v) + shift).toISOString() : v);
    // Spread runs over the last few days (they were generated in one go):
    // the attacks stay recent, routine runs fan out behind them.
    let seed = 7;
    const rnd = () => (seed = (seed * 16807) % 2147483647) / 2147483647;
    const flagged = d.runs.filter(r => r.blocked || r.would_block || r.approved_by || r.reviewed_by);
    const rest = d.runs.filter(r => !flagged.includes(r));
    const age = {};
    flagged.forEach((r, i) => { age[r.key] = (4 + i * 37) * 60e3; });
    rest.forEach(r => { age[r.key] = (60 + rnd() * 60 * 24 * 4) * 60e3; });
    const move = (v, k) => (typeof v === "string" && ISO.test(v) && !v.startsWith("0001")) ? new Date(Date.parse(v) - age[k]).toISOString() : v;
    d.runs.forEach(r => { r.first_seen = move(r.first_seen, r.key); r.last_seen = move(r.last_seen, r.key); });
    for (const [k, det] of Object.entries(d.detail)) d.detail[k] = walk(det, v => move(v, k));
    d.runs.sort((a, b) => Date.parse(b.last_seen) - Date.parse(a.last_seen));
    const last = {};
    for (const r of d.runs) if (!last[r.repo] || r.last_seen > last[r.repo]) last[r.repo] = r.last_seen;
    d.repos.forEach(r => { if (last[r.repo]) r.last_seen = last[r.repo]; });
    return d;
  });
  const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
  const realFetch = window.fetch.bind(window);
  window.fetch = async (input, opt = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, location.href);
    if (!url.pathname.includes("/api/")) return realFetch(input, opt);
    const d = await ready;
    const path = url.pathname.slice(url.pathname.indexOf("/api/"));
    const q = url.searchParams;
    if ((opt.method || "GET") !== "GET") return json("This is a read-only demo with sample data.", 403);
    if (path === "/api/me") return json({ accounts: [], app_configured: false, authed: true, demo: true });
    if (path === "/api/totals") return json(d.totals);
    if (path === "/api/repos") return json(d.repos);
    if (path === "/api/runs") {
      let rs = d.runs;
      if (q.get("repo")) rs = rs.filter(r => r.repo.toLowerCase() === q.get("repo").toLowerCase());
      const only = q.get("only");
      if (only === "needs-review") rs = rs.filter(r => r.needs_review);
      if (only === "blocked") rs = rs.filter(r => r.blocked > 0);
      if (only === "outsider") rs = rs.filter(r => r.exposed);
      return json(rs.slice(0, +q.get("limit") || 200));
    }
    if (path.startsWith("/api/runs/")) {
      const det = d.detail[decodeURIComponent(path.slice("/api/runs/".length))];
      return det ? json(det) : json("run not found", 404);
    }
    return json("not available in the demo", 404);
  };
  // No live stream in a static page: a quiet stand-in that reports "connected".
  window.EventSource = class { constructor() { setTimeout(() => this.onopen && this.onopen(), 50); } addEventListener() {} close() {} };
})();
