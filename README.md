# ctk — Context Token Killer

A single static binary that slashes the tokens Claude Code spends on tool output.
Inspired by [rtk](https://github.com/rtk-ai/rtk), with **broader scope**: where rtk
rewrites *Bash commands*, ctk compresses the *output of every tool* — `Bash`,
`Grep`, `Read`, and **all `mcp__*` calls** — which is where the tokens actually go
in MCP-heavy agent sessions (OpenSearch, debuggers, Jira/Confluence dumps).

```
ctk gain
  tokens in        16,050
  tokens out        1,335
  tokens saved     14,715  (92% reduction)
  compressions          4
```

- **One binary, zero runtime deps** (~3.7 MB), ~single-digit-ms startup — it runs on
  every tool call, so it's Go, not a script.
- **Lossy with recovery.** Full output is teed to `.ctk/cache/`; the compressed
  result carries the path so the model can re-read on demand.
- **Never breaks a session.** Any error → original output is kept, exit 0.

## How it works

Claude Code's `PostToolUse` hook hands ctk the tool result on stdin; ctk returns
`hookSpecificOutput.updatedToolOutput`, which replaces what the model sees before
it enters the context window. The full output is consumed locally (free); only the
compressed version is billed on the next turn.

```
tool runs ──► PostToolUse ──► ctk hook ──► { updatedToolOutput } ──► model context
                                  ├ content-aware filter (JSON / OpenSearch / grep / text)
                                  ├ tee full output → .ctk/cache/<sha>.txt
                                  └ log savings → .ctk/stats.jsonl   (ctk gain)
```

Filters (deterministic, free): OpenSearch/ES responses → top-K hits + `_source`
projection; generic JSON → cap arrays, drop empty fields, truncate strings;
`file:line:match` dumps → group by file; logs → run-length dedupe + head/tail.

## Install

```bash
brew trust abhayshalghar/tap           # one-time: approve the tap (required for casks)
brew install AbhayShalghar/tap/ctk     # install the binary (macOS)
ctk init --global                      # turn it on for every project
# then fully quit & reopen Claude Code, and check: ctk gain --global
```

> **`brew trust` is required.** Homebrew refuses to load casks from third-party
> taps until you approve them once. Skip it and you'll see
> *"Refusing to load cask … from untrusted tap"*.
>
> **Linux:** casks are macOS-only — grab `ctk_linux_*.tar.gz` from the
> [Releases](https://github.com/AbhayShalghar/ctk/releases) page and put `ctk` on your PATH.
>
> **Each person runs `ctk init --global` once** — installing the binary doesn't
> enable the hook by itself.

Per-repo instead of global:

```bash
cd your/repo && ctk init               # writes ./.claude/settings.json
```

Because ctk is on your `PATH`, the hook command is simply `ctk hook` — no paths,
nothing copied into your repos, no coupling to any project.

## Commands

```
ctk hook                 run the compressor (stdin = PostToolUse event); used by the hook
ctk init [path]          wire into a repo's .claude/settings.json
ctk init --global        wire into ~/.claude/settings.json
ctk init --with-rtk      drop Bash from the matcher so rtk owns shell commands
ctk init --matcher S     custom tool matcher (default: Bash|Grep|Read|mcp__.*)
ctk uninstall [path]     remove the ctk hook entry (leaves other hooks intact)
ctk gain [--history]     token-savings report (per repo); --reset to clear
ctk version
```

## Running alongside rtk

Safe together — different hook events, no collision:

| | rtk | ctk |
|---|---|---|
| event | `PreToolUse` (rewrites the command) | `PostToolUse` (rewrites the output) |
| scope | Bash only | Bash, Grep, Read, all `mcp__*` |

Run `ctk init --with-rtk` to let rtk own Bash and ctk own everything rtk can't see.

## Configuration

Optional `ctk.config.json` in a repo root overrides defaults:

```json
{ "disabledTools": ["Read"], "hitsKeep": 8, "minGain": 0.2,
  "sourceFields": ["id", "status", "type", "createdAt"] }
```

`sourceFields` is the domain hook: list the `_source` keys worth keeping per repo
and OpenSearch hits get projected down to just those.

## Build & test

```bash
go test ./...        # filter parity suite
go build -o ctk .    # local binary
```

## Releasing (Homebrew)

The tap `AbhayShalghar/homebrew-tap` already exists. To cut a release:

1. `git tag vX.Y.Z && git push origin vX.Y.Z`
2. `GITHUB_TOKEN=$(gh auth token) goreleaser release --clean`

That cross-compiles darwin/linux × amd64/arm64, publishes the GitHub release, and
commits a Homebrew **cask** to the tap under `Casks/`. macOS users then
`brew install AbhayShalghar/tap/ctk`. Casks are macOS-only, so Linux users install
by downloading the `ctk_linux_*.tar.gz` asset from the release. The cask's
post-install hook strips the Gatekeeper quarantine so the unsigned binary runs.

## Roadmap

1. Domain plugin — auto-project `_source`/gRPC fields from service schemas.
2. Semantic tier — summarize huge outputs with a cheap model above a size threshold.
3. Cross-call cache & diff — on a repeat read, return only the delta.
4. `gain --global` — savings aggregated across all repos.
```
