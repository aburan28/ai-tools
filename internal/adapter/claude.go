package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent-hub/internal/model"
	"agent-hub/internal/util"
)

type ClaudeAdapter struct {
	rootPath     string
	projectsPath string
	sessionsPath string
}

func NewClaudeAdapter(root string) *ClaudeAdapter {
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".claude")
	}
	root = util.HomeAwarePath(root)
	return &ClaudeAdapter{
		rootPath:     root,
		projectsPath: filepath.Join(root, "projects"),
		sessionsPath: filepath.Join(root, "sessions"),
	}
}

func (a *ClaudeAdapter) Name() string {
	return "claude-code"
}

func (a *ClaudeAdapter) List(ctx context.Context) ([]model.AgentSession, error) {
	if _, err := os.Stat(a.projectsPath); err != nil {
		return nil, nil
	}

	running := a.loadRunningSessions()
	collected := make(map[string]model.AgentSession)

	err := filepath.WalkDir(a.projectsPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if strings.HasSuffix(path, ".meta.jsonl") {
			return nil
		}
		session, ok := a.readSession(path, running)
		if !ok {
			return nil
		}
		if existing, exists := collected[session.ID]; !exists || (existing.LastActivityAt != nil && session.LastActivityAt != nil && session.LastActivityAt.After(*existing.LastActivityAt)) {
			collected[session.ID] = session
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sessions := make([]model.AgentSession, 0, len(collected))
	for _, session := range collected {
		sessions = append(sessions, session)
	}
	util.SortByTimeDesc(sessions)
	return sessions, nil
}

func (a *ClaudeAdapter) loadRunningSessions() map[string]map[string]any {
	running := make(map[string]map[string]any)
	if _, err := os.Stat(a.sessionsPath); err != nil {
		return running
	}

	entries, err := os.ReadDir(a.sessionsPath)
	if err != nil {
		return running
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(a.sessionsPath, entry.Name()))
		if err != nil {
			continue
		}
		payload := map[string]any{}
		if err := json.Unmarshal(raw, &payload); err != nil {
			continue
		}
		sessionID := util.NormalizeOptional(payload["sessionId"])
		if sessionID == "" {
			continue
		}
		running[sessionID] = map[string]any{
			"pid":    payload["pid"],
			"cwd":    util.NormalizeOptional(payload["cwd"]),
			"socket": util.NormalizeOptional(payload["messagingSocketPath"]),
			"name":   util.NormalizeOptional(payload["name"]),
		}
	}
	return running
}

func (a *ClaudeAdapter) readSession(path string, running map[string]map[string]any) (model.AgentSession, bool) {
	file, err := os.Open(path)
	if err != nil {
		return model.AgentSession{}, false
	}
	defer file.Close()

	sessionID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(filepath.Base(path)))
	title, lastPrompt, projectPath, gitBranch := "", "", "", ""
	messages := make([]model.Message, 0)
	var firstTS, lastTS *time.Time

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		record := map[string]any{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		recordType := util.NormalizeOptional(record["type"])
		switch recordType {
		case "custom-title":
			if candidate := util.NormalizeOptional(record["customTitle"]); candidate != "" {
				title = candidate
			}
			continue
		case "last-prompt":
			if candidate := util.NormalizeOptional(record["lastPrompt"]); candidate != "" {
				lastPrompt = candidate
			}
			continue
		}

		if recordType != "user" && recordType != "assistant" {
			continue
		}

		text := util.ExtractText(record["message"])
		if text == "" {
			continue
		}

		ts := util.ParseTime(record["timestamp"])
		if ts == nil {
			ts = util.ParseTime(record["time"])
		}
		if firstTS == nil || (ts != nil && ts.Before(*firstTS)) {
			firstTS = ts
		}
		if lastTS == nil || (ts != nil && ts.After(*lastTS)) {
			lastTS = ts
		}
		if projectPath == "" {
			projectPath = util.NormalizeOptional(record["cwd"])
		}
		if gitBranch == "" {
			gitBranch = util.NormalizeOptional(record["gitBranch"])
		}

		messages = append(messages, model.Message{
			Role:      recordType,
			Text:      text,
			Timestamp: ts,
			Source:    path,
		})
	}

	if len(messages) == 0 && title == "" && lastPrompt == "" {
		return model.AgentSession{}, false
	}

	if len(messages) > 0 {
		if firstTS == nil && messages[0].Timestamp != nil {
			firstTS = messages[0].Timestamp
		}
		if lastTS == nil && messages[len(messages)-1].Timestamp != nil {
			lastTS = messages[len(messages)-1].Timestamp
		}
	}

	runtime, hasRuntime := running[sessionID]
	pid := 0
	socket := ""
	if hasRuntime {
		if candidate, ok := util.ToInt(runtime["pid"]); ok {
			pid = candidate
		}
		socket = util.NormalizeOptional(runtime["socket"])
		if projectPath == "" {
			projectPath = util.NormalizeOptional(runtime["cwd"])
		}
	}

	status := "finished"
	if pid > 0 && util.IsPIDAlive(pid) {
		status = "running"
	} else if lastTS != nil {
		if time.Since(*lastTS) < 24*time.Hour {
			status = "idle"
		}
	}

	if firstTS == nil {
		if stat, err := os.Stat(path); err == nil {
			m := stat.ModTime().UTC()
			firstTS = &m
			if lastTS == nil {
				lastTS = &m
			}
		}
	}
	if lastTS == nil && firstTS != nil {
		lastTS = firstTS
	}
	if firstTS == nil || lastTS == nil {
		return model.AgentSession{}, false
	}

	computedTitle := title
	if computedTitle == "" {
		computedTitle = lastPrompt
	}
	if computedTitle == "" && len(messages) > 0 {
		computedTitle = messages[len(messages)-1].Text
	}

	if len(computedTitle) > 140 {
		computedTitle = computedTitle[:140]
	}

	extra := map[string]any{
		"root": a.rootPath,
	}
	if hasRuntime {
		extra["runtime_name"] = runtime["name"]
		extra["runtime_socket"] = socket
	}

	return model.AgentSession{
		ID:             sessionID,
		Agent:          "claude-code",
		ProjectPath:    projectPath,
		Worktree:       util.FindRepoRoot(projectPath),
		GitBranch:      gitBranch,
		PID:            pid,
		Status:         status,
		StartedAt:      firstTS,
		LastActivityAt: lastTS,
		Title:          computedTitle,
		Messages:       messages,
		SourcePath:     path,
		Extra:          extra,
	}, true
}

func (a *ClaudeAdapter) Send(ctx context.Context, sessionID string, message string) error {
	return ErrUnsupported
}

func (a *ClaudeAdapter) OpenCommand(session model.AgentSession) (string, error) {
	if session.Agent != a.Name() {
		return "", ErrUnsupported
	}
	if session.PID <= 0 {
		return "", ErrUnsupported
	}
	socket, ok := session.Extra["runtime_socket"].(string)
	if ok && strings.TrimSpace(socket) != "" {
		return fmt.Sprintf("No CLI attach support yet; Claude session has runtime socket %s", socket), nil
	}
	return "", ErrUnsupported
}
