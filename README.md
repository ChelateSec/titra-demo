# Titra

**Watch what AI agents do in your CI job — and block the dangerous actions, in real time.**

Titra is [Chelate Sec](https://github.com/ChelateSec)'s runtime guard for agentic CI/CD. Teams drop AI agents (Claude Code, Gemini CLI, Copilot) into their pipelines to triage issues and review PRs. Those agents read whatever a stranger writes and act with the repo's credentials. One prompt-injected issue and the agent pushes code, approves its own PR, or leaks a token — and nobody sees it happen.

Titra is one line in your workflow. It watches the whole job, blocks the actions that turn a prompt injection into a supply-chain compromise, and posts a plain-language report on the run. The block decision is deterministic policy, **never a model** — a guard that asked an LLM whether to block could itself be prompt-injected.

> Securing the agentic CI/CD.

## Quick start

```yaml
      - name: Titra
        uses: ChelateSec/titra@v1
        with:
          mode: monitor      # watch only; switch to enforce when you trust it
```

Put it after `actions/checkout` and before your agent step. That's the whole install: no server, no app, no account. Titra watches the job, and when it ends it posts a report to the run's **Summary** tab by itself. Flip `mode: enforce` to start blocking.

## What it does

Three layers, from one line:

1. **Guards commands.** `git`, `gh`, `curl`, `npm` and friends are intercepted. A push to a protected branch, a self-approved PR, a package publish, a read of `.git/config` — refused before it runs.
2. **Watches the whole job.** Using the Linux kernel's audit subsystem, Titra sees *every* process, outbound connection and credential-file read — even a tool run by absolute path to dodge the shims, or an exfil over a raw socket from Python. Nothing in the job is invisible.
3. **Hooks the agent.** With Claude Code it reads every tool call the agent makes and blocks the dangerous ones at the source, so the report shows the agent's own intent.

The report ties it together into a story:

> `ext-user` (outside contributor) opened issue #42. Claude Code ran with **contents, pull-requests** write access and tried 3 dangerous actions — Titra stopped them before they ran.

...followed by the blocked actions, who triggered the run, the token's permissions, and everything else the job touched (unexpected destinations, stray credential reads, tools run around the guard).

## Modes

| Mode | What happens |
| --- | --- |
| `monitor` | Logs everything, blocks nothing. Start here. |
| `dry-run` | Logs and marks what *would* block, still allows it. |
| `enforce` | Blocks matching actions in real time and fails the check. |

## Inputs

| Input | Default | Meaning |
| --- | --- | --- |
| `mode` | `monitor` | `monitor` \| `dry-run` \| `enforce` |
| `policy` | *(built-in)* | Path to a policy file; omit for secure defaults |
| `tools` | *(built-in set)* | Comma-separated tools to guard |
| `trace` | `true` | Watch the whole job with the kernel (Linux + sudo) |
| `dns` | `true` | Resolve outbound connections to domain names |
| `allowed-domains` | — | Extra destinations this job is expected to reach |
| `report` | `true` | Post the report and set the check automatically |
| `fail-on` | `block` | Fail on `block`, `any`, or `off` |

## Policy

The built-in defaults block the actions that turned real 2026 incidents into compromises: pushing code, approving or merging PRs, dispatching other workflows, publishing packages, and reading credential stores. Write your own with `titra init`:

```yaml
version: 1
mode: enforce
default: allow
allowed_domains:
  - api.mycorp.com
  - "*.internal"
rules:
  - id: block-git-push
    description: Agent must not push code
    action: block
    severity: critical
    match:
      tool: git
      args_regex: '(^|\s)push(\s|$)'
```

Rules are first-match-wins. `match.tool` is the command name; `match.args_regex` is tested against the joined arguments.

## Claude Code hook

To record and gate the agent's own tool calls, add Titra as a `PreToolUse` hook (Titra's Action sets this up for you when it detects Claude Code):

```json
{ "hooks": { "PreToolUse": [ { "matcher": "*",
  "hooks": [ { "type": "command", "command": "titra hook claude" } ] } ] } }
```

## CLI

```
titra install   # set up the shims and emit the guard environment
titra trace start|stop|collect   # whole-job kernel tracing (needs root)
titra hook claude                # Claude Code PreToolUse hook
titra report --format md|text|json [--state DIR] [--fail-on block|any|off]
titra init      # write a starter policy
titra serve     # optional self-hosted dashboard (Titra Enterprise)
```

## Try it

Copy [`examples/.github/workflows/demo-blocked.yml`](examples/.github/workflows/demo-blocked.yml) into a repo and run it from the Actions tab. A stand-in agent tries seven attacks; Titra blocks them, catches the two that dodge the shims, and fails the check. Locally:

```
make demo          # command-level blocking, no root
sudo make demo-trace   # full product: tracing + report (Linux)
```

## Titra Enterprise — the org-wide view

The Action above is free and open source. For a security team running agents
across many repos, Titra rolls every run up into one organization dashboard and
audit log — no hosted service:

```
titra report --github-org acme --format html --out titra-org.html
```

It collects the report each run already uploads and produces one self-contained
HTML page: which repos run AI agents, which are **exposed** (an outsider can
trigger an agent that holds a write token), what was blocked this period, and a
full audit log of every blocked and flagged action across the org. Run it on a
schedule from an internal repo — see [`examples/.github/workflows/org-report.yml`](examples/.github/workflows/org-report.yml).

Enterprise adds, on top of the open-source guard:

- **One dashboard** across every repo, ranked by risk.
- **Org-wide policy** — one policy file enforced everywhere, so a team can't weaken its own guard.
- **Audit log** — a durable, exportable record of every agent action and block.
- Coming next: alerts (Slack/email on block) and ephemeral, scoped credentials for the agent.

A live hosted dashboard (`titra serve`) is available for teams that want it; the
serverless roll-up needs nothing to run.

## How it fits with Shrike

[Shrike](https://github.com/ChelateSec/shrike) finds which of your workflows an outsider could hijack. Titra guards the ones you can't lock down. Shrike tells you where the risk is; Titra contains it.

## License

Apache-2.0.
