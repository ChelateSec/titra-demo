# Titra

**Watch what AI agents do in your CI pipelines — and block the dangerous actions, in real time.**

Titra is [Chelate Sec](https://github.com/ChelateSec)'s runtime guard for agentic CI/CD. Teams drop AI agents (Claude Code, Gemini CLI, Copilot) into their pipelines to triage issues and review PRs. These agents read whatever a stranger writes and act with the repo's credentials. One prompt-injected issue and the agent pushes code, approves its own PR, or leaks a secret — and nobody sees it happen.

Titra has two parts:

- **The guard** runs inside each CI job. It sits between the agent and the tools it uses, **logs every action** and **blocks the dangerous ones** (push, PR approval, publish, credential read) before they run.
- **The dashboard + GitHub App** is the control plane. Orgs install Titra in one click; every guarded job streams its actions to a live web dashboard; Titra posts a Check on the PR.

> Titra is the runtime half of Chelate. [Shrike](https://github.com/ChelateSec/shrike), the open-source scanner, *finds* the risky workflows; Titra *holds* them at runtime.

---

## See it in 60 seconds

```bash
make demo-live      # builds Titra, starts the dashboard, runs a guarded agent,
                    # and opens your browser — watch 4 actions get blocked live
```

No GitHub, no secrets, no signup. A simulated prompt-injected agent tries to push code, self-approve a PR, trigger a release, and exfiltrate the token — Titra blocks each one and it appears on the dashboard in real time.

## How the guard works

Titra installs itself on `PATH` under the names of the tools it guards (`git`, `gh`, `curl`, `npm`, ...), busybox-style. When the agent runs `git push`, it's Titra that runs: it checks the command against a policy, records it, then execs the real tool or refuses.

```
agent runs:  git push origin main
                  │
             ┌────▼─────┐   evaluate against policy (deterministic, never an LLM)
             │  titra   │── log locally ─────► actions.jsonl
             └────┬─────┘── stream ──────────► dashboard (live)
          allow?  │  block?
        exec real │  refuse, exit 1
            git ◄─┘
```

**The block decision is deterministic policy matching — never a model.** A guard that asked an LLM whether to block could itself be prompt-injected, reintroducing the exact risk Titra removes.

## The product: dashboard + GitHub App

Run the control plane:

```bash
make serve          # dashboard on http://localhost:8080
```

Two modes, same binary:

- **Local / dev** — no GitHub App configured. The dashboard is open and ingest needs no token. This is the zero-setup demo.
- **Product** — register a GitHub App (one click), and the dashboard requires GitHub login while ingest requires a per-org token.

### One-click GitHub App install

Open `http://localhost:8080/setup` (or your deployed URL) and click **Create the GitHub App**. Titra uses GitHub's app-manifest flow: GitHub creates the App and hands the credentials straight back to the server — you copy nothing. Then:

1. **Install** the App on your org.
2. Add two repo secrets: `TITRA_SERVER` (your dashboard URL) and `TITRA_TOKEN` (shown on the setup page).
3. Add the Action to any workflow that runs an agent:

```yaml
- name: Titra guard
  uses: ChelateSec/titra@v1
  with:
    mode: enforce                      # monitor | dry-run | enforce
    server: ${{ secrets.TITRA_SERVER }}
    token:  ${{ secrets.TITRA_TOKEN }}
```

Every guarded job now streams live to your dashboard, and Titra can post a Check Run on the PR.

> OAuth and the GitHub App need a public URL. For a local demo without deploying, the dashboard + live ingest work on `localhost`; expose it with a tunnel (`cloudflared tunnel --url http://localhost:8080`) only when you want the GitHub App install + Checks.

## Deploy

```bash
docker compose up --build            # local container → http://localhost:8080
# or
fly launch --copy-config --now       # Fly.io; then:
fly secrets set TITRA_PUBLIC_URL=https://titra.your-org.dev
```

The server is a single static binary with no third-party dependencies; data persists under `/data` (a JSONL action log + a config file).

## Modes

| Mode | Logs | Blocks | Use it to |
| --- | --- | --- | --- |
| `monitor` | ✅ | — | See what your agents do, break nothing. **Start here.** |
| `dry-run` | ✅ | marks *would-block* | Preview what enforcement would stop. |
| `enforce` | ✅ | ✅ | Block dangerous actions in real time. |

## Policy

Built-in defaults block the actions that turned 2026's incidents into supply-chain compromises: code push, PR approval/merge, workflow-dispatch pivots, package/release publish, and credential-store reads. Generate an editable policy:

```bash
titra init --mode monitor       # writes .titra/policy.yml
```

Rules are evaluated in order, **first match wins**. A rule matches when its `tool` matches (empty = any) and its `args_regex` matches the arguments.

```yaml
version: 1
mode: enforce
default: allow
rules:
  - id: block-git-push
    description: Agent must not push code to the repository
    action: block
    severity: critical
    match: { tool: git, args_regex: '(^|\s)push(\s|$)' }
```

Full example: [`examples/policy.yml`](examples/policy.yml). Point the Action at yours with `policy: .titra/policy.yml`.

## CLI

```
titra serve      # run the dashboard / GitHub App control plane
titra install    # create the tool shims and emit the guarding environment
titra guard …    # evaluate and run a single tool invocation (shims call this)
titra report     # summarize the local action log (text | md; --fail-on block gates CI)
titra init       # write a starter policy
titra version
```

## What it is honest about

- **An in-runner guard is bypassable by root** — an agent that calls a tool by absolute path (`/usr/bin/git`) slips past. Titra is action-level policy on the agent's *intent* — the thing network egress monitoring and firewalls can't see (the dangerous action is often a normal-looking call to GitHub itself). It is not unbypassable containment, and pairs with, rather than replaces, out-of-runner controls.
- **The shim sees commands looked up by name** — that covers how agents run tools today. The durable version is an MCP proxy that inspects structured tool calls instead of shell strings — on the roadmap.
- **Shells aren't guarded by default** — their inner commands already are.

## Layout

```
main.go                 entrypoint — guard + serve/install/report/init
internal/policy/        policy model + loader (YAML/JSON)
internal/engine/        deterministic decision engine
internal/guard/         enforcement point (resolve real tool, log, stream, exec/block)
internal/reporter/      guard → server live stream
internal/audit/         append-only JSONL action record (on-disk == on-the-wire)
internal/report/        terminal + Markdown summaries
internal/install/       shim install + environment wiring
internal/server/        control plane: dashboard, ingest, SSE, GitHub App, OAuth
  web/                  embedded dashboard (HTML/CSS/JS, no build step)
internal/miniyaml/      tiny zero-dependency YAML subset parser
action.yml              GitHub composite Action
Dockerfile · fly.toml · docker-compose.yml
examples/               guarded workflow, demo workflow, sample policy
scripts/                demo.sh (local block demo), demo-live.sh (dashboard demo)
```

**Zero third-party dependencies** — guard and server alike. Fitting for a tool whose job is reducing the untrusted code in your pipeline.

---

**Securing the agentic workflows.** — [Chelate Sec](https://github.com/ChelateSec)
