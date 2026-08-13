package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agent-hub/internal/model"
	"agent-hub/internal/util"
)

type OpencodeAdapter struct {
	rootPath string
	dbPath   string
}

func NewOpencodeAdapter(root string, dbPath string) *OpencodeAdapter {
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".local", "share", "opencode")
	}
	root = util.HomeAwarePath(root)
	if dbPath == "" {
		dbPath = filepath.Join(root, "opencode.db")
	}
	dbPath = util.HomeAwarePath(dbPath)
	return &OpencodeAdapter{
		rootPath: root,
		dbPath:   dbPath,
	}
}

func (a *OpencodeAdapter) Name() string {
	return "opencode"
}

func (a *OpencodeAdapter) List(_ context.Context) ([]model.AgentSession, error) {
	if _, err := os.Stat(a.dbPath); err != nil {
		return nil, nil
	}

	query := `SELECT
id,
directory,
title,
version,
time_created,
time_updated,
time_archived,
path,
agent,
model
FROM session
WHERE time_archived IS NULL
ORDER BY time_updated DESC`

	rows, err := util.RunSQLiteJSON(a.dbPath, query)
	if err != nil {
		return nil, err
	}

	sessions := make([]model.AgentSession, 0, len(rows))
	for _, row := range rows {
		sid := util.NormalizeOptional(row["id"])
		if sid == "" {
			continue
		}

		title := util.NormalizeOptional(row["title"])
		if title == "" {
			continue
		}

		projectPath := util.NormalizeOptional(row["directory"])
		firstTs := util.ParseTime(row["time_created"])
		lastTs := util.ParseTime(row["time_updated"])
		if firstTs == nil {
			m := time.Now().UTC()
			firstTs = &m
		}
		if lastTs == nil {
			lastTs = firstTs
		}

		status := "finished"
		if time.Since(*lastTs) < 24*time.Hour {
			status = "idle"
		}

		messages, err := a.loadLatestMessages(sid, 20)
		if err != nil {
			messages = nil
		}

		sessions = append(sessions, model.AgentSession{
			ID:             sid,
			Agent:          "opencode",
			ProjectPath:    projectPath,
			Worktree:       util.FindRepoRoot(projectPath),
			PID:            0,
			Status:         status,
			StartedAt:      firstTs,
			LastActivityAt: lastTs,
			Title:          title,
			Model:          util.NormalizeOptional(row["model"]),
			Messages:       messages,
			SourcePath:     a.dbPath,
			Extra: map[string]any{
				"agent":   util.NormalizeOptional(row["agent"]),
				"version": util.NormalizeOptional(row["version"]),
				"path":    util.NormalizeOptional(row["path"]),
			},
		})
	}

	util.SortByTimeDesc(sessions)
	return sessions, nil
}

func (a *OpencodeAdapter) loadLatestMessages(sessionID string, limit int) ([]model.Message, error) {
	if sessionID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	query := fmt.Sprintf(`SELECT
	m.time_created AS ts,
	json_extract(m.data, '$.role') AS role,
	p.data AS part
FROM message AS m
JOIN part AS p ON m.id = p.message_id
WHERE m.session_id = %s
AND json_extract(p.data, '$.type') = 'text'
ORDER BY m.time_created DESC
LIMIT %d`, util.SQLiteStringLiteral(sessionID), limit)

	rows, err := util.RunSQLiteJSON(a.dbPath, query)
	if err != nil {
		return nil, err
	}
	messages := make([]model.Message, 0, len(rows))
	for _, row := range rows {
		role := util.NormalizeOptional(row["role"])
		if role != "user" && role != "assistant" {
			continue
		}
		ts := util.ParseTime(row["ts"])
		partText := util.NormalizeOptional(row["part"])
		text := ""
		if partText != "" {
			var part map[string]any
			if err := json.Unmarshal([]byte(partText), &part); err == nil {
				text = util.ExtractText(part)
			}
		}
		if text == "" {
			continue
		}
		messages = append(messages, model.Message{Role: role, Text: text, Timestamp: ts, Source: "part:" + sessionID})
	}

	// Re-sort into chronological order for inspect output.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}

func (a *OpencodeAdapter) Send(ctx context.Context, sessionID string, message string) error {
	if !util.CommandExists("opencode") {
		return ErrUnsupported
	}
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session id is required")
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("message is required")
	}

	cmd := exec.CommandContext(ctx, "opencode", "run", "--session", sessionID, "--continue", "--format", "json", strings.TrimSpace(message))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("opencode run failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (a *OpencodeAdapter) OpenCommand(session model.AgentSession) (string, error) {
	if strings.TrimSpace(session.ID) == "" {
		return "", ErrUnsupported
	}
	if !util.CommandExists("opencode") {
		return "", ErrUnsupported
	}
	return "opencode run --session " + session.ID + " --continue", nil
}
