from __future__ import annotations

import os
import re
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Iterable, List


def normalize_optional(value: Any) -> str | None:
    if value is None:
        return None
    if isinstance(value, str):
        value = value.strip()
        if value == "" or value.lower() in {"none", "null"}:
            return None
        return value
    return str(value)


def parse_time(value: Any) -> datetime | None:
    if value is None:
        return None
    try:
        if isinstance(value, datetime):
            if value.tzinfo is None:
                return value.replace(tzinfo=timezone.utc)
            return value

        if isinstance(value, (int, float)):
            # Heuristic: values above 10^12 are usually millis.
            return datetime.fromtimestamp(
                (value / 1000.0) if value > 10**12 else float(value),
                tz=timezone.utc,
            )

        if isinstance(value, str):
            value = value.strip()
            if value == "" or value.lower() in {"none", "null"}:
                return None
            if re.fullmatch(r"\d+(\.\d+)?", value):
                return parse_time(float(value))
            text = value.replace("Z", "+00:00")
            parsed = datetime.fromisoformat(text)
            if parsed.tzinfo is None:
                parsed = parsed.replace(tzinfo=timezone.utc)
            return parsed
    except (OSError, OverflowError, ValueError):
        return None
    return None


def extract_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, str):
        return value.strip()
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (int, float)):
        return str(value)
    if isinstance(value, list):
        return " ".join(part for part in (extract_text(v) for v in value) if part)
    if isinstance(value, dict):
        parts: List[str] = []
        if "text" in value and isinstance(value["text"], str):
            text_value = value["text"].strip()
            if text_value:
                parts.append(text_value)
        if "message" in value:
            message_text = extract_text(value["message"])
            if message_text:
                parts.append(message_text)
        if "input_text" in value:
            input_text = extract_text(value["input_text"])
            if input_text:
                parts.append(input_text)
        if "content" in value:
            content_text = extract_text(value["content"])
            if content_text:
                parts.append(content_text)
        return " ".join(parts)
    return ""


def is_pid_running(pid: Any) -> bool:
    if pid is None:
        return False
    try:
        os.kill(int(pid), 0)
    except OSError:
        return False
    except TypeError:
        return False
    return True


def format_age(ts: datetime | None) -> str:
    if ts is None:
        return "unknown"
    now = datetime.now(timezone.utc)
    delta = now - ts
    if delta < timedelta(minutes=1):
        return f"{int(delta.total_seconds())}s"
    if delta < timedelta(hours=1):
        return f"{int(delta.total_seconds() // 60)}m"
    if delta < timedelta(days=1):
        return f"{int(delta.total_seconds() // 3600)}h"
    if delta < timedelta(days=30):
        return f"{delta.days}d"
    return f"{delta.days // 30}mo"


def find_repo_root(path_text: str | None) -> str | None:
    if not path_text:
        return None
    path = Path(path_text)
    current = path
    tries = 0
    while True:
        if (current / ".git").exists():
            return str(current)
        parent = current.parent
        if parent == current or tries > 32:
            break
        current = parent
        tries += 1
    return None


def build_search_haystack(session: Any) -> str:
    values: Iterable[str] = [
        session.id,
        session.agent,
        session.title or "",
        session.project_path or "",
        session.worktree or "",
        session.git_branch or "",
        session.model or "",
        " ".join(message.text for message in session.messages[:12]),
    ]
    return " ".join(v.lower() for v in values)
