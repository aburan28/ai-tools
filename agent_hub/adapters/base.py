from __future__ import annotations

from abc import ABC, abstractmethod

from ..models import AgentSession


class SessionAdapter(ABC):
    @abstractmethod
    def list_sessions(self) -> list[AgentSession]:
        raise NotImplementedError
