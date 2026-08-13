from __future__ import annotations

import argparse
import json
import sqlite3
from typing import Iterable

from .adapters import ClaudeAdapter, CodexAdapter
from .models import AgentSession
from .util import build_search_haystack, format_age


def _collect_sessions(agents: list[str]) -> list[AgentSession]:
    selected: list[AgentSession] = []
    if "all" in agents or "claude-code" in agents:
        selected.extend(ClaudeAdapter().list_sessions())
    if "all" in agents or "codex" in agents:
        selected.extend(CodexAdapter().list_sessions())
    return selected


def _parse_agents(raw: str | None) -> list[str]:
    if not raw:
        return ["all"]
    values = [item.strip().lower() for item in raw.split(",") if item.strip()]
    normalized: list[str] = []
    for value in values:
        if value in {"claude", "claude-code", "claudecode"}:
            normalized.append("claude-code")
        elif value in {"codex", "codex-cli", "agent-codex"}:
            normalized.append("codex")
        else:
            normalized.append(value)
    return normalized or ["all"]


def _search_text(s: AgentSession) -> str:
    return build_search_haystack(s)


def cmd_list(args: argparse.Namespace) -> None:
    sessions = _collect_sessions(_parse_agents(args.agent))
    if args.status:
        by_status = {}
        for session in sessions:
            by_status[session.status] = by_status.get(session.status, 0) + 1
        for state in sorted(by_status):
            print(f"{state:8} {by_status[state]}")
        return

    if args.json:
        print(json.dumps([session.to_dict() for session in sessions], indent=2))
        return

    sessions.sort(key=lambda session: session.last_activity_at, reverse=True)
    print(
        f"{'ID':34} {'AGENT':11} {'STATUS':8} {'AGE':8} {'BRANCH':16} {'TITLE'}"
    )
    print("-" * 120)
    for session in sessions:
        sid = session.id[:8]
        title = session.title or ""
        if len(title) > 62:
            title = title[:59] + "..."
        print(
            f"{session.agent}:{sid:8} {session.agent:11} {session.status:8} "
            f"{format_age(session.last_activity_at):8} "
            f"{(session.git_branch or '')[:16]:16} {title}"
        )


def _simple_score(haystack: str, query_tokens: Iterable[str]) -> int:
    return sum(haystack.count(token) for token in query_tokens)


def cmd_search(args: argparse.Namespace) -> None:
    sessions = _collect_sessions(_parse_agents(args.agent))
    query = args.query.lower().strip()
    if not query:
        print("query is required")
        return
    tokens = [part for part in query.split() if len(part) > 2]
    if not tokens:
        tokens = [query]

    matches: list[tuple[int, AgentSession]] = []
    by_id = {session.canonical_id(): session for session in sessions}

    try:
        conn = sqlite3.connect(":memory:")
        conn.execute(
            "CREATE VIRTUAL TABLE session_docs USING fts5(canonical_id, agent, haystack, tokenize='porter')"
        )
        for session in sessions:
            conn.execute(
                "INSERT INTO session_docs(canonical_id, agent, haystack) VALUES (?,?,?)",
                (session.canonical_id(), session.agent, _search_text(session)),
            )
        rows = conn.execute(
            "SELECT canonical_id, bm25(session_docs) AS score FROM session_docs WHERE haystack MATCH ? ORDER BY score LIMIT ?",
            (query, args.limit * 10),
        ).fetchall()
        for canonical_id, score in rows:
            lookup = by_id.get(canonical_id)
            if lookup is not None:
                matches.append((int(-score), lookup))
        conn.close()
    except sqlite3.OperationalError:
        for session in sessions:
            haystack = _search_text(session)
            score = _simple_score(haystack, tokens)
            if score > 0:
                matches.append((score, session))

    if not matches:
        print("No matches.")
        return

    matches.sort(key=lambda item: (item[0], item[1].last_activity_at), reverse=True)
    if args.json:
        print(
            json.dumps(
                [
                    {"score": score, "session": session.to_dict()}
                    for score, session in matches[: args.limit]
                ],
                indent=2,
            )
        )
        return

    print(f"{'SCORE':>5} {'ID':34} {'AGENT':11} {'STATUS':8} {'AGE':8} {'TITLE'}")
    print("-" * 120)
    for score, session in matches[: args.limit]:
        snippet = session.title or ""
        if len(snippet) > 70:
            snippet = snippet[:67] + "..."
        print(
            f"{score:5d} {session.agent}:{session.id[:8]:8} {session.agent:11} "
            f"{session.status:8} {format_age(session.last_activity_at):8} {snippet}"
        )


def _find_session(sessions: list[AgentSession], target: str) -> AgentSession:
    matches = [s for s in sessions if s.canonical_id() == target]
    if matches:
        return matches[0]

    if ":" in target:
        short = target.rsplit(":", 1)[1]
        matches = [session for session in sessions if session.canonical_id().endswith(f":{short}")]
        if len(matches) == 1:
            return matches[0]
        if len(matches) > 1:
            raise ValueError(f"session id '{target}' matches {len(matches)} sessions, use a longer id")

    exact = [s for s in sessions if s.id == target]
    if exact:
        return exact[0]

    by_prefix = [s for s in sessions if s.id.startswith(target)]
    if len(by_prefix) == 1:
        return by_prefix[0]

    raise ValueError(f"session id '{target}' is ambiguous or not found")


def cmd_inspect(args: argparse.Namespace) -> None:
    sessions = _collect_sessions(_parse_agents(args.agent))
    try:
        session = _find_session(sessions, args.session_id)
    except ValueError as err:
        print(f"error: {err}")
        return

    if args.json:
        print(json.dumps(session.to_dict(), indent=2))
        return

    print(f"session:      {session.canonical_id()}")
    print(f"agent:        {session.agent}")
    print(f"status:       {session.status}")
    if session.pid:
        print(f"pid:          {session.pid}")
    print(f"projectPath:  {session.project_path}")
    print(f"worktree:     {session.worktree}")
    print(f"branch:       {session.git_branch}")
    print(f"model:        {session.model}")
    print(f"startedAt:    {session.started_at.isoformat()}")
    print(f"lastActivity: {session.last_activity_at.isoformat()}")
    print(f"sourcePath:   {session.source_path}")
    print("title:")
    print(f"  {session.title}")
    print("latest messages:")
    for msg in session.messages[-args.messages :]:
        if not msg.text:
            continue
        when = msg.timestamp.isoformat() if msg.timestamp else "unknown"
        print(f"\n- {msg.role} @ {when}\n  {msg.text}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Agent Hub CLI")
    subparsers = parser.add_subparsers(dest="command")

    list_parser = subparsers.add_parser("list", help="List recent session snapshots")
    list_parser.add_argument("--agent", default="all", help="comma-separated filter: claude, codex, all")
    list_parser.add_argument("--json", action="store_true", help="output JSON")
    list_parser.add_argument("--status", action="store_true", help="print status counts only")
    list_parser.set_defaults(func=cmd_list)

    search_parser = subparsers.add_parser("search", help="Full-text search over sessions/messages")
    search_parser.add_argument("query", help="search query")
    search_parser.add_argument("--agent", default="all", help="comma-separated filter: claude, codex, all")
    search_parser.add_argument("--limit", type=int, default=20, help="max results")
    search_parser.add_argument("--json", action="store_true", help="output JSON")
    search_parser.set_defaults(func=cmd_search)

    inspect_parser = subparsers.add_parser("inspect", help="Inspect a session by id")
    inspect_parser.add_argument("session_id", help="session id (full id, prefix, or agent:id)")
    inspect_parser.add_argument("--agent", default="all", help="comma-separated filter: claude, codex, all")
    inspect_parser.add_argument("--messages", type=int, default=10, help="number of latest messages to show")
    inspect_parser.add_argument("--json", action="store_true", help="output JSON")
    inspect_parser.set_defaults(func=cmd_inspect)

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    if not getattr(args, "command", None):
        parser.print_help()
        return 2

    try:
        args.func(args)
        return 0
    except Exception as err:
        raise SystemExit(f"agent-hub failed: {err}")


if __name__ == "__main__":
    raise SystemExit(main())
