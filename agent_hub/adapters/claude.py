from __future__ import annotations

import json
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

from ..models import AgentSession, Message
from ..util import extract_text, find_repo_root, is_pid_running, parse_time, normalize_optional
from .base import SessionAdapter


class ClaudeAdapter(SessionAdapter):
    def __init__(self, root: Path | None = None) -> None:
        root = root or Path.home() / ".claude"
        self.root = root
        self.projects_dir = root / "projects"
        self.sessions_dir = root / "sessions"

    def _load_running_sessions(self) -> dict[str, dict[str, Any]]:
        running: dict[str, dict[str, Any]] = {}
        if not self.sessions_dir.exists():
            return running

        for session_file in self.sessions_dir.glob("*.json"):
            try:
                payload = json.loads(session_file.read_text(encoding="utf-8"))
            except (OSError, json.JSONDecodeError):
                continue

            session_id = normalize_optional(payload.get("sessionId"))
            if not session_id:
                continue

            entry = {
                "pid": payload.get("pid"),
                "cwd": normalize_optional(payload.get("cwd")),
                "startedAt": parse_time(payload.get("startedAt")),
                "session_file": str(session_file),
                "name": normalize_optional(payload.get("name")),
            }
            running[session_id] = entry
        return running

    def _read_session(self, path: Path, running: dict[str, dict[str, Any]]) -> AgentSession | None:
        session_id = path.stem
        title: str | None = None
        project_path: str | None = None
        git_branch: str | None = None
        messages: list[Message] = []
        first_ts: datetime | None = None
        last_ts: datetime | None = None
        last_prompt: str | None = None

        try:
            text = path.read_text(encoding="utf-8").splitlines()
        except OSError:
            return None

        for raw in text:
            try:
                obj = json.loads(raw)
            except json.JSONDecodeError:
                continue

            record_type = normalize_optional(obj.get("type"))
            if record_type == "custom-title":
                candidate = normalize_optional(obj.get("customTitle"))
                if candidate:
                    title = candidate
                continue

            if record_type == "last-prompt":
                candidate = normalize_optional(obj.get("lastPrompt"))
                if candidate:
                    last_prompt = candidate
                continue

            if record_type in {"user", "assistant"}:
                message_obj = obj.get("message")
                text_value = extract_text(message_obj)
                if not text_value:
                    continue

                ts = parse_time(obj.get("timestamp"))
                if ts is None:
                    ts = first_ts

                if first_ts is None or (ts and ts < first_ts):
                    first_ts = ts
                if ts and (last_ts is None or ts > last_ts):
                    last_ts = ts
                if not project_path:
                    project_path = normalize_optional(obj.get("cwd"))
                if not git_branch:
                    git_branch = normalize_optional(obj.get("gitBranch"))

                messages.append(
                    Message(
                        role=record_type,
                        text=text_value,
                        timestamp=ts,
                        source=str(path),
                    )
                )

        if not messages and not title and not last_prompt:
            return None

        runtime = running.get(session_id)
        pid = runtime.get("pid") if isinstance(runtime, dict) else None
        running_state = "finished"
        if pid and is_pid_running(pid):
            running_state = "running"
        else:
            if last_ts and (datetime.now(timezone.utc) - last_ts) < timedelta(hours=24):
                running_state = "idle"

        if first_ts is None:
            first_ts = parse_time(path.stat().st_mtime)
        if last_ts is None:
            last_ts = parse_time(path.stat().st_mtime)
        if project_path is None:
            project_path = normalize_optional(runtime.get("cwd")) if isinstance(runtime, dict) else None

        final_title = title or last_prompt or (messages[-1].text if messages else session_id)
        worktree = find_repo_root(project_path)

        return AgentSession(
            id=session_id,
            agent="claude-code",
            project_path=project_path,
            worktree=worktree,
            git_branch=git_branch,
            pid=int(pid) if pid is not None else None,
            started_at=first_ts,
            last_activity_at=last_ts,
            status=running_state,
            title=final_title,
            messages=messages[-20:],
            source_path=str(path),
            extra={
                "raw_session_file": str(path),
                "session_index_name": normalize_optional(runtime.get("name")) if isinstance(runtime, dict) else None,
            },
        )

    def list_sessions(self) -> list[AgentSession]:
        if not self.projects_dir.exists():
            return []

        running = self._load_running_sessions()
        discovered: list[AgentSession] = []
        sessions_by_id: dict[str, AgentSession] = {}
        for session_path in self.projects_dir.rglob("*.jsonl"):
            if session_path.name.endswith(".meta.json"):
                continue

            session = self._read_session(session_path, running)
            if session is None:
                continue
            if session.id in sessions_by_id:
                existing = sessions_by_id[session.id]
                if session.last_activity_at > existing.last_activity_at:
                    sessions_by_id[session.id] = session
            else:
                sessions_by_id[session.id] = session

        discovered.extend(sessions_by_id.values())
        return sorted(discovered, key=lambda s: s.last_activity_at, reverse=True)
