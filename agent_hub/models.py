from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, List, Optional


@dataclass
class Message:
    role: str
    text: str
    timestamp: Optional[datetime] = None
    source: Optional[str] = None


@dataclass
class AgentSession:
    id: str
    agent: str
    project_path: Optional[str]
    worktree: Optional[str]
    git_branch: Optional[str]
    pid: Optional[int]
    started_at: datetime
    last_activity_at: datetime
    status: str
    title: Optional[str] = None
    model: Optional[str] = None
    messages: List[Message] = field(default_factory=list)
    source_path: str | None = None
    extra: dict[str, Any] = field(default_factory=dict)

    def canonical_id(self) -> str:
        return f"{self.agent}:{self.id}"

    def short_id(self) -> str:
        return self.id[:8]

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "agent": self.agent,
            "canonical_id": self.canonical_id(),
            "projectPath": self.project_path,
            "worktree": self.worktree,
            "gitBranch": self.git_branch,
            "pid": self.pid,
            "status": self.status,
            "startedAt": self.started_at.isoformat(),
            "lastActivityAt": self.last_activity_at.isoformat(),
            "title": self.title,
            "model": self.model,
            "messages": [
                {
                    "role": msg.role,
                    "text": msg.text,
                    "timestamp": msg.timestamp.isoformat() if msg.timestamp else None,
                    "source": msg.source,
                }
                for msg in self.messages
            ],
            "sourcePath": self.source_path,
            "extra": self.extra,
        }
