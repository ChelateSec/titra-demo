// Titra GitHub Action — post step. Runs when the job ends (always), after every
// other step. It renders the run report into the job summary, drops a JSON copy
// for the org roll-up, and fails the check if something was blocked. This post
// hook is what lets Titra be a single `uses:` line with no report step to add.
const { spawnSync } = require("child_process");
const fs = require("fs");
const os = require("os");

const env = process.env;
if (env.TITRA_DO_REPORT && env.TITRA_DO_REPORT !== "true") process.exit(0);

const titra = env.TITRA_BIN || "titra";
const report = env.TITRA_REPORT || (env.RUNNER_TEMP || os.tmpdir()) + "/titra-report.md";
const jsonReport = report.replace(/\.md$/, "") + ".json";
const state = env.TITRA_STATE || "";
const failOn = (env.TITRA_FAIL_ON || "block").toLowerCase();

// Collecting kernel events needs root; rendering alone does not. Prefer sudo on
// Linux so the trace layer appears, but fall back without it.
function titraRun(args) {
  const useSudo = os.platform() === "linux" && state && fs.existsSync(state);
  const r = useSudo
    ? spawnSync("sudo", ["-E", titra, ...args], { env, encoding: "utf8" })
    : spawnSync(titra, args, { env, encoding: "utf8" });
  if (r.status === null && useSudo) return spawnSync(titra, args, { env, encoding: "utf8" });
  return r;
}

// 1. Markdown report -> job summary.
const md = titraRun(["report", "--format", "md", "--out", report]);
if (env.GITHUB_STEP_SUMMARY && md.stdout) {
  try { fs.appendFileSync(env.GITHUB_STEP_SUMMARY, md.stdout); } catch (_) {}
}
if (md.stdout) process.stdout.write(md.stdout);

// 2. JSON report -> artifact / org roll-up.
titraRun(["report", "--format", "json", "--out", jsonReport]);

// 3. Set the check result.
if (failOn === "off" || failOn === "none" || failOn === "") process.exit(0);
const gate = titraRun(["report", "--fail-on", failOn]);
if (gate.status && gate.status !== 0) {
  console.log("::error title=Titra::A dangerous agent action was blocked or flagged. See the job summary for the full report.");
  process.exit(1);
}
