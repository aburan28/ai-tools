from __future__ import annotations

import json
import sqlite3
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

from ..models import AgentSession, Message
from ..util import extract_text, find_repo_root, is_pid_running, parse_time, normalize_optional
from .base import SessionAdapter


class CodexAdapter(SessionAdapter):
    def __init__(self, root: Path | None = None) -> None:
        root = root or Path.home() / ".codex"
        self.root = root
        self.state_db = root / "state_5.sqlite"
        self.process_manager = root / "process_manager" / "chat_processes.json"

    def _load_processes(self) -> dict[str, dict[str, Any]]:
        if not self.process_manager.exists():
            return {}
        try:
            raw = json.loads(self.process_manager.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return {}

        result: dict[str, dict[str, Any]] = {}
        for item in raw if isinstance(raw, list) else []:
            convo = normalize_optional(item.get("conversationId"))
            if not convo:
                continue
            pid = item.get("osPid")
            if pid is None:
                pid = item.get("processId")
            entry = {
                "pid": pid,
                "cwd": normalize_optional(item.get("cwd")),
                "updatedAtMs": parse_time(item.get("updatedAtMs")),
                "command": normalize_optional(item.get("command")),
            }
            current = result.get(convo)
            if not current:
                result[convo] = entry
                continue
            current_updated = current.get("updatedAtMs")
            entry_updated = entry.get("updatedAtMs")
            if isinstance(current_updated, datetime) and isinstance(entry_updated, datetime):
                if entry_updated > current_updated:
                    result[convo] = entry
            elif not current_updated and entry_updated:
                result[convo] = entry
        return result

    def _messages_from_transcript(self, transcript_path: Path) -> list[Message]:
        messages: list[Message] = []
        if not transcript_path.exists():
            return messages

        try:
            lines = transcript_path.read_text(encoding="utf-8").splitlines()
        except OSError:
            return messages

        for raw in lines:
            try:
                item = json.loads(raw)
            except json.JSONDecodeError:
                continue

            if normalize_optional(item.get("type")) == "response_item":
                payload = item.get("payload", {})
                role = normalize_optional(payload.get("role"))
                if role in {"user", "assistant"}:
                    text = extract_text(payload.get("content", []))
                    if text:
                        messages.append(
                            Message(
                                role=role,
                                text=text,
                                timestamp=parse_time(payload.get("timestamp") or item.get("timestamp")),
                                source="response_item",
                            )
                        )
                continue

            if normalize_optional(item.get("type")) == "event_msg":
                payload = item.get("payload", {})
                event_type = normalize_optional(payload.get("type"))
                if event_type == "user_message":
                    text = normalize_optional(payload.get("message", ""))
                    if text:
                        messages.append(
                            Message(
                                role="user",
                                text=text,
                                timestamp=parse_time(payload.get("timestamp", item.get("timestamp"))),
                                source="event_msg",
                            )
                        )

        return messages

    def list_sessions(self) -> list[AgentSession]:
        if not self.state_db.exists():
            return []

        conn = sqlite3.connect(self.state_db)
        conn.row_factory = sqlite3.Row
        query = """
            SELECT
              id,
              rollout_path,
              created_at,
              updated_at,
              created_at_ms,
              updated_at_ms,
              cwd,
              title,
              git_branch,
              git_sha,
              git_origin_url,
              model,
              agent_nickname,
              agent_role,
              first_user_message,
              preview
            FROM threads
            WHERE archived=0
            ORDER BY updated_at_ms DESC, updated_at DESC
        """

        rows = conn.execute(query).fetchall()
        conn.close()

        process_map = self._load_processes()
        sessions: list[AgentSession] = []
        for row in rows:
            sid = normalize_optional(row["id"])
            if not sid:
                continue

            rollout = normalize_optional(row["rollout_path"])
            transcript = Path(rollout) if rollout else None
            if transcript is None or not transcript.exists():
                transcript = None

            messages = self._messages_from_transcript(transcript) if transcript else []
            first_ts = parse_time(row["created_at_ms"] or row["created_at"]) or parse_time(row["updated_at_ms"] or row["updated_at"])
            last_ts = parse_time(row["updated_at_ms"] or row["updated_at"])

            if not messages and not row["title"] and not row["first_user_message"]:
                continue

            if first_ts is None:
                if messages:
                    first_message = min((m.timestamp for m in messages if m.timestamp), default=None)
                    if first_message:
                        first_ts = first_message
            if last_ts is None:
                if messages:
                    last_message = max((m.timestamp for m in messages if m.timestamp), default=None)
                    if last_message:
                        last_ts = last_message

            if first_ts is None:
                continue
            if last_ts is None:
                last_ts = first_ts

            runtime = process_map.get(sid, {})
            pid = runtime.get("pid")
            status = "finished"
            if pid and is_pid_running(pid):
                status = "running"
            elif last_ts and (datetime.now(timezone.utc) - last_ts) < timedelta(hours=24):
                status = "idle"

            project_path = normalize_optional(row["cwd"]) or normalize_optional(runtime.get("cwd"))
            title = (
                normalize_optional(row["title"])
                or normalize_optional(row["first_user_message"])
                or normalize_optional(row["preview"])
            )

            sessions.append(
                AgentSession(
                    id=sid,
                    agent="codex",
                    project_path=project_path,
                    worktree=find_repo_root(project_path),
                    git_branch=normalize_optional(row["git_branch"]),
                    pid=int(pid) if pid else None,
                    started_at=first_ts,
                    last_activity_at=last_ts,
                    status=status,
                    title=title,
                    model=normalize_optional(row["model"]),
                    messages=messages[-20:],
                    source_path=str(transcript) if transcript else None,
                    extra={
                        "git_origin_url": normalize_optional(row["git_origin_url"]),
                        "git_sha": normalize_optional(row["git_sha"]),
                        "agent_nickname": normalize_optional(row["agent_nickname"]),
                        "agent_role": normalize_optional(row["agent_role"]),
                        "rollout_path": rollout,
                    },
                )
            )

        return sessions
