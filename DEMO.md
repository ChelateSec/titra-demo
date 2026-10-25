# Titra Enterprise: YC Demo Guide

## Overview

This demo showcases **Titra Enterprise**, Chelate's commercial runtime guard for agentic CI/CD. It demonstrates:

1. **Self-hosted dashboard** - Enterprise teams run Titra on their own infrastructure
2. **Real-time attack prevention** - Blocks prompt-injected AI agents before they cause damage
3. **Live visualization** - Shows exactly what happened, with risk scoring and timelines

## The Story

**Problem:** AI agents in CI/CD (Claude Code, Copilot, Gemini) can be prompt-injected by anyone who opens an issue or PR. One malicious input → the agent pushes code, approves its own PR, or exfiltrates secrets.

**Solution:** Titra guards every command the agent runs, blocks the dangerous ones in real-time, and shows everything in a dashboard your security team can watch live.

## Demo Flow (5 minutes)

### Part 1: The Dashboard (1 min)

```bash
# Build and start the server
go build -o titra .
./demo-yc.sh
```

**Show:**
- Clean, editorial dashboard at `http://localhost:8080`
- "Securing the agentic CI/CD" tagline
- Zero runs yet (fresh install)

**Say:**
> "This is Titra Enterprise. It's a single binary that enterprise security teams run on their own infrastructure. No data leaves their network. It aggregates every action across all their repos and workflows."

### Part 2: The Attack (2 min)

**The demo script simulates a prompt-injected agent.**

**Say:**
> "Here's what happens when an outside contributor opens an issue with prompt injection. The AI agent reads it, gets hijacked, and tries to:"
>
> 1. Push malicious code to `main`
> 2. Self-approve a PR
> 3. Trigger a release workflow (privilege escalation)
> 4. Exfiltrate the GitHub token
> 5. Publish a poisoned npm package
>
> "Watch the dashboard."

**Show:** (as the script runs)
- Actions appearing live in the feed
- Blocked count incrementing in red
- Risk score climbing

### Part 3: The Forensics (2 min)

**Click into the run on the dashboard.**

**Show:**
- Risk Score: 85+ (critical)
- Blocked: 5 actions
- Full timeline of what the agent tried
- Each blocked action with:
  - The exact command
  - Which rule caught it
  - Severity (critical/high)

**Say:**
> "This is what your security team sees the instant it happens. Not in a SIEM three days later—right now, with enough detail to understand the attack and prove it was stopped."
>
> "Traditional egress firewalls can't stop this. The attack isn't an outbound connection to a suspicious IP—it's `gh pr review --approve`, a totally legitimate GitHub API call. The agent is authorized to do it. Titra is the only thing that knows this approval is dangerous because it came from an untrusted trigger."

## Key Differentiators

### vs. StepSecurity Harden-Runner

| Feature | StepSecurity | Titra |
|---------|-------------|-------|
| **Network egress filtering** | ✓ | ✓ |
| **Blocks harmful GitHub API calls** | ✗ (blind to `gh` commands) | ✓ |
| **Agent-specific awareness** | ✗ | ✓ (hooks into Claude Code, etc.) |
| **Attack path graphing** | ✗ | ✓ (coming in Titra + Shrike integration) |
| **Dashboard** | GitHub Action only | Self-hosted enterprise option |

**The kill shot:**
> "GitHub is shipping native egress firewalls next month. That whole category is commoditizing. We're the only ones who understand the **agent-specific attack surface**—the fact that `gh pr merge` is fine when _you_ run it, but catastrophic when a prompt-injected agent does."

### Why Self-Hosted?

**For YC, emphasize this is a _choice_, not the only option:**

1. **Free tier (OSS repos):** Serverless GitHub Action, zero config, posts reports to public feed
2. **Pro tier ($99/repo/mo):** Same serverless action + private dashboard access
3. **Enterprise ($50k+/year):** Self-hosted option shown in this demo

**Why enterprise customers want on-prem:**
- **Compliance:** Banks, healthcare, defense can't send data to SaaS
- **Zero trust:** The dashboard sees every command, including secrets in arguments
- **Air-gapped deployments:** Some customers run CI in environments with no internet

## Pricing Model

| Tier | Price | What They Get |
|------|-------|---------------|
| **OSS** | Free | Action + public dashboard badge |
| **Pro** | $99/repo/month | Action + private dashboard |
| **Enterprise** | $50k-$500k/year | Self-hosted server, SSO, compliance reports, support SLA |

## The Ask

**Traction so far:**
- Shrike (OSS scanner): Launching this week
- Titra (runtime guard): v0.3.0, working product
- First design partner: [TBD - get one before YC interview]

**What we need Y Combinator for:**
1. **Credibility** - Security products need trust; YC badge helps
2. **Network** - Intros to CISOs at high-profile OSS projects (Vercel, Supabase, etc.)
3. **Go-to-market** - We're technical founders; need help with enterprise sales motion

**12-month goal:**
- 50 OSS repos using Titra (public social proof)
- 5 paying enterprise customers ($250k ARR)
- Attack incident report that goes viral (our "left-pad moment")

## Running the Demo

### Prerequisites

```bash
# Install Go 1.21+
# Clone the repo
git clone https://github.com/ChelateSec/titra
cd titra
```

### Quick Start

```bash
# One command demo
./demo-yc.sh
```

### Manual Run (if demo script fails)

```bash
# Build
go build -o titra .

# Start server
./titra serve --addr :8080 --data ./demo-data &

# Open dashboard
open http://localhost:8080

# Run simulated attack
export TITRA_SERVER="http://localhost:8080"
export TITRA_PROJECT="demo"
./titra install --server "$TITRA_SERVER" --mode enforce
# ... then run commands that will be blocked
```

## Video Recording Checklist

- [ ] Clean terminal (clear history, `clear`)
- [ ] Large font (18pt+, presenters can read from back of room)
- [ ] Dashboard open in browser before recording
- [ ] Run `demo-yc.sh` and let it pause at each step
- [ ] Narrate over the pauses
- [ ] Show the dashboard refreshing live as actions come in
- [ ] Zoom in on the risk score and blocked actions
- [ ] End with the full timeline visible

## Talking Points

**Opening (15 sec):**
> "AI agents are about to flood CI/CD pipelines. Every one of them is a prompt injection attack surface. We're the only company that can stop it."

**The Problem (30 sec):**
> "GitHub, GitLab, all the CI platforms—they're adding AI agents to triage issues and review PRs. But anyone can open an issue. One malicious input, the agent reads it, gets hijacked, and the attacker has write access to your repo. This isn't theoretical—we found it in 23,000 repos with Shrike, our open-source scanner."

**The Solution (30 sec):**
> "Titra sits between the agent and your repo. Every command the agent runs goes through our guard. Push to a protected branch? Blocked. Self-approve a PR? Blocked. Trigger another workflow to escalate privileges? Blocked. And your security team sees it all live, not in a SIEM days later."

**Why We Win (30 sec):**
> "Traditional security tools don't understand agents. They see network traffic, not intent. Titra hooks into the agent's tool calls and knows when `gh pr merge` is legitimate versus when it's a prompt-injected attack. We're the only ones who built for this threat model."

**The Business (30 sec):**
> "Open-source scanner (Shrike) finds the risk. Free tier (Titra) guards OSS repos. Enterprise tier sells to companies that can't use SaaS—banks, defense contractors, anyone with compliance requirements. The wedge is OSS: when Titra blocks an attack on a popular project, that's free marketing."

**The Team (15 sec):**
> "[Your background]. We've been in security and AI infrastructure for [X years]. We saw this coming before anyone else, and we shipped working code."

## Post-Demo Q&A Prep

**Q: Can't agents just bypass Titra?**

A: Two layers. First, we intercept the `git`, `gh`, `npm` commands via PATH shims. Second, we use Linux kernel audit to see _everything_ the job does, even if the agent calls `/usr/bin/git` directly to dodge the shims. The full product (what you see in this demo) catches both.

**Q: What if the agent doesn't use shell commands?**

A: We hook the agent's native tool-calling protocol. For Claude Code, we're a `PreToolUse` hook—we see the agent's intent before it even becomes a shell command. For other agents, we instrument their tool execution layer.

**Q: Why not just restrict agent permissions?**

A: You can't. The agent needs write access to post comments, update PRs, commit fixes. GitHub's permission model is coarse—either the agent can write or it can't. Titra gives you fine-grained control: "yes write, but no `git push`, no `gh pr merge`."

**Q: How do you make money if OSS is free?**

A: OSS is the funnel. When we block an attack on a popular repo, maintainers see it work and tell their employers. The enterprise sales cycle starts with "we already use Titra on our OSS projects, now we want it for private repos + the dashboard."

**Q: Isn't this a feature GitHub should build?**

A: Maybe in 5 years. Right now they're still figuring out Actions abuse (pwn-requests, expression injection). Agent security is too early for them. We'll be the de facto standard by the time they try, and enterprises will already be bought in.

---

## Next Steps After Demo

1. **Record the video** (5 min, narrated)
2. **Get one design partner** (ideally a YC company using AI in CI)
3. **Launch Shrike** (create launch post showing real vulns we found)
4. **Submit YC app** with video attached

---

**Good luck! 🚀**
