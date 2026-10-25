// Titra GitHub Action — main entry. Builds the guard from the action's own
// checkout, starts whole-job tracing (best effort), and installs the command
// shims onto PATH for the rest of the job. The report runs automatically in
// post.js when the job ends.
//
// Zero npm dependencies on purpose: a security tool should add as little
// untrusted code to your pipeline as possible. We talk to the runner only
// through the files GitHub documents (GITHUB_ENV, GITHUB_PATH, GITHUB_OUTPUT)
// and the ::workflow:: command protocol on stdout.
const { execFileSync, spawnSync } = require("child_process");
const fs = require("fs");
const path = require("path");
const os = require("os");

const inp = (name, def = "") =>
  (process.env["INPUT_" + name.toUpperCase().replace(/ /g, "_").replace(/-/g, "_")] ?? def).trim();

const env = process.env;
const RUNNER_TEMP = env.RUNNER_TEMP || os.tmpdir();
const ACTION_PATH = env.GITHUB_ACTION_PATH || __dirname.replace(/\/action$/, "");

function setEnv(k, v) { if (env.GITHUB_ENV) fs.appendFileSync(env.GITHUB_ENV, `${k}=${v}\n`); env[k] = v; }
function addPath(d) { if (env.GITHUB_PATH) fs.appendFileSync(env.GITHUB_PATH, d + "\n"); }
function setOut(k, v) { if (env.GITHUB_OUTPUT) fs.appendFileSync(env.GITHUB_OUTPUT, `${k}=${v}\n`); }
function group(name, fn) { console.log("::group::" + name); try { return fn(); } finally { console.log("::endgroup::"); } }
function notice(m) { console.log("titra: " + m); }

main().catch((e) => {
  console.log(`::error title=Titra::failed to start: ${e.message}`);
  process.exit(1);
});

async function main() {
  const binDir = path.join(RUNNER_TEMP, "titra");
  const stateDir = path.join(RUNNER_TEMP, "titra-trace");
  const log = path.join(RUNNER_TEMP, "titra-actions.jsonl");
  const report = path.join(RUNNER_TEMP, "titra-report.md");
  fs.mkdirSync(binDir, { recursive: true });
  fs.writeFileSync(log, "");

  const titra = path.join(binDir, "titra");
  const goVersion = inp("go-version", "1.22");

  console.log("::group::Titra — installing the guard");
  // Prefer a prebuilt release binary (fast, no toolchain). Fall back to
  // building from the action's own source if that isn't available.
  if (!(await tryDownloadBinary(titra))) {
    notice("no prebuilt binary for this version; building from source.");
    ensureGo(goVersion);
    run("go", ["build", "-ldflags", "-X main.version=action", "-o", titra, "."], { cwd: ACTION_PATH });
  }
  fs.chmodSync(titra, 0o755);
  console.log("::endgroup::");

  const mode = inp("mode", "monitor");
  const tools = inp("tools");
  const allowed = inp("allowed-domains");
  const doTrace = inp("trace", "true") === "true";
  const dns = inp("dns", "true") === "true";

  // Share config with post.js and later steps.
  setEnv("TITRA_LOG", log);
  setEnv("TITRA_STATE", stateDir);
  setEnv("TITRA_MODE", mode);
  setEnv("TITRA_BIN", titra);
  setEnv("TITRA_REPORT", report);
  setEnv("TITRA_FAIL_ON", inp("fail-on", "block"));
  setEnv("TITRA_DO_REPORT", inp("report", "true"));
  if (tools) setEnv("TITRA_TOOLS", tools);
  if (allowed) setEnv("TITRA_ALLOWED_DOMAINS", allowed);

  // Whole-job tracing — best effort. Never fail the build over it.
  if (doTrace && os.platform() === "linux") {
    group("Titra — starting whole-job tracing", () => {
      const r = spawnSync("sudo", ["-E", titra, "trace", "start", "--state", stateDir, `--dns=${dns}`, "--lock"],
        { stdio: "inherit", env });
      if (r.status !== 0) notice("tracing unavailable; command-level guarding still active.");
    });
  }

  // Install the command shims and wire them into the job's PATH/env.
  const args = ["install", "--dir", path.join(binDir, "shims"), "--log", log, "--mode", mode, "--github-env"];
  if (inp("policy")) args.push("--policy", inp("policy"));
  if (tools) args.push("--tools", tools);
  run(titra, args, { stdio: ["ignore", "ignore", "inherit"] });

  addPath(binDir); // expose `titra` for later steps and for post.js
  setOut("log", log);
  setOut("report", report);
  notice(`guarding this job — mode: ${mode}`);
}

function run(cmd, args, opts = {}) {
  execFileSync(cmd, args, { stdio: "inherit", env, ...opts });
}

// tryDownloadBinary fetches the release asset matching this action's ref, e.g.
// titra_linux_amd64 from .../releases/download/v1/. Returns true on success.
async function tryDownloadBinary(dest) {
  const repo = env.GITHUB_ACTION_REPOSITORY; // owner/repo of this action
  const ref = env.GITHUB_ACTION_REF;         // v1, v1.2.3, a sha, ...
  if (!repo || !ref) return false;
  const goos = os.platform() === "win32" ? "windows" : os.platform();
  const arch = { x64: "amd64", arm64: "arm64" }[os.arch()];
  if (!arch) return false;
  const asset = `titra_${goos}_${arch}${goos === "windows" ? ".exe" : ""}`;
  const url = `https://github.com/${repo}/releases/download/${ref}/${asset}`;
  try {
    await fetchTo(url, dest);
    notice(`installed prebuilt ${asset} (${ref}).`);
    return true;
  } catch (_) {
    try { fs.unlinkSync(dest); } catch (_) {}
    return false;
  }
}

// fetchTo downloads url to file, following redirects, using only node built-ins.
function fetchTo(url, file, depth = 0) {
  if (depth > 5) throw new Error("too many redirects");
  const https = require("https");
  return new Promise((resolve, reject) => {
    const req = https.get(url, { headers: { "User-Agent": "titra-action" } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        res.resume();
        fetchTo(res.headers.location, file, depth + 1).then(resolve, reject);
        return;
      }
      if (res.statusCode !== 200) { res.resume(); reject(new Error("HTTP " + res.statusCode)); return; }
      const out = fs.createWriteStream(file);
      res.pipe(out);
      out.on("finish", () => out.close(resolve));
      out.on("error", reject);
    });
    req.on("error", reject);
    req.setTimeout(20000, () => req.destroy(new Error("timeout")));
  });
}

// ensureGo makes `go` runnable. If it's already on PATH, done. Otherwise try a
// couple of standard install locations setup-go uses.
function ensureGo(version, binDir) {
  if (spawnSync("go", ["version"], { env }).status === 0) return;
  const candidates = [
    "/usr/local/go/bin",
    path.join(env.HOME || "", "go", "bin"),
    path.join(env.RUNNER_TOOL_CACHE || "", "go", version, "x64", "bin"),
  ];
  for (const c of candidates) {
    if (c && fs.existsSync(path.join(c, "go"))) {
      setEnv("PATH", c + path.delimiter + env.PATH);
      env.PATH = c + path.delimiter + env.PATH;
      if (spawnSync("go", ["version"], { env }).status === 0) return;
    }
  }
  throw new Error("Go toolchain not found. Add `- uses: actions/setup-go@v5` before Titra, or set go-version.");
}
