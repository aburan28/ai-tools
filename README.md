# ai-tools

## Local Agent Hub

This repository includes an Agent Hub CLI that can discover and coordinate local
multi-agent sessions from:

- Claude Code (`~/.claude/projects/**/*.jsonl`)
- Codex (`~/.codex/state_5.sqlite` + transcripts)
- Opencode (`~/.local/share/opencode/opencode.db`)

The Go implementation now powers the default `./agent-hub` entrypoint.

### Quick start

From this directory:

```bash
./agent-hub list
./agent-hub search "stripe webhook"
./agent-hub inspect claude:019ff...
./agent-hub send --agent codex <session-id> "please continue from last step"
```

If you want a standalone binary:

```bash
go build -o ./agent-hub-bin ./cmd/agent-hub
./agent-hub-bin list
```

Python CLI compatibility is still available via:

```bash
python3 -m agent_hub list
```

### Supported commands

- `list`: list sessions from one or more agents
- `search`: full-text query across sessions and message text
- `inspect`: inspect a session by full id, prefix, or `agent:id`
- `open`: print best-effort reopen command for interactive terminal sessions
- `send`: send a message to a running Codex/Opencode session
- `shell`: interactive mode for coordinating multi-session work

### Storage inputs used

- Claude: `~/.claude/projects/**/*.jsonl` and running metadata from `~/.claude/sessions/*.json`
- Codex: `~/.codex/state_5.sqlite`, transcripts under `~/.codex/sessions/**/*.jsonl`,
  running processes via `~/.codex/process_manager/chat_processes.json`
- Opencode: `~/.local/share/opencode/opencode.db`

### Notes

- `open` exposes best-effort command hints. Claude sessions are read from local
  transcript metadata and may not always be directly reattachable from CLI.
- `send` is currently implemented for Codex and Opencode when their CLIs are
  available. Claude send is still unsupported in this release.
