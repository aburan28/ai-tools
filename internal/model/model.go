package model

import "time"

type Message struct {
	Role      string     `json:"role"`
	Text      string     `json:"text"`
	Timestamp *time.Time `json:"timestamp,omitempty"`
	Source    string     `json:"source,omitempty"`
}

type AgentSession struct {
	ID             string         `json:"id"`
	Agent          string         `json:"agent"`
	ProjectPath    string         `json:"projectPath,omitempty"`
	Worktree       string         `json:"worktree,omitempty"`
	GitBranch      string         `json:"gitBranch,omitempty"`
	PID            int            `json:"pid,omitempty"`
	Status         string         `json:"status"`
	StartedAt      *time.Time     `json:"startedAt"`
	LastActivityAt *time.Time     `json:"lastActivityAt"`
	Title          string         `json:"title,omitempty"`
	Model          string         `json:"model,omitempty"`
	Messages       []Message      `json:"messages"`
	SourcePath     string         `json:"sourcePath,omitempty"`
	Extra          map[string]any `json:"extra"`
}

func (s AgentSession) CanonicalID() string {
	return s.Agent + ":" + s.ID
}

func (s AgentSession) ShortID() string {
	if len(s.ID) <= 8 {
		return s.ID
	}
	return s.ID[:8]
}
