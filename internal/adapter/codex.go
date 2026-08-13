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

type CodexAdapter struct {
	rootPath     string
	stateDB      string
	processState string
}

func NewCodexAdapter(root string) *CodexAdapter {
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".codex")
	}
	root = util.HomeAwarePath(root)
	return &CodexAdapter{
		rootPath:     root,
		stateDB:      filepath.Join(root, "state_5.sqlite"),
		processState: filepath.Join(root, "process_manager", "chat_processes.json"),
	}
}

func (a *CodexAdapter) Name() string {
	return "codex"
}

func (a *CodexAdapter) List(ctx context.Context) ([]model.AgentSession, error) {
	if _, err := os.Stat(a.stateDB); err != nil {
		return nil, nil
	}

	query := `SELECT
		id,
		rollout_path,
		created_at,
		updated_at,
		created_at_ms,
		updated_at_ms,
		cwd,
		title,
		git_sha,
		git_branch,
		git_origin_url,
		model,
		agent_nickname,
		agent_role,
		first_user_message,
		preview
	FROM threads
	WHERE archived=0
	ORDER BY updated_at_ms DESC, updated_at DESC`

	rows, err := util.RunSQLiteJSON(a.stateDB, query)
	if err != nil {
		return nil, err
	}

	processes, err := a.loadRunningProcesses()
	if err != nil {
		processes = map[string]map[string]any{}
	}

	sessions := make([]model.AgentSession, 0, len(rows))
	for _, row := range rows {
		sessionID := util.NormalizeOptional(row["id"])
		if sessionID == "" {
			continue
		}

		rollout := util.NormalizeOptional(row["rollout_path"])
		messages := a.readMessages(rollout)

		title := util.NormalizeOptional(row["title"])
		if title == "" {
			title = util.NormalizeOptional(row["first_user_message"])
		}
		if title == "" {
			title = util.NormalizeOptional(row["preview"])
		}
		if title == "" && len(messages) > 0 {
			title = messages[len(messages)-1].Text
		}
		if title == "" {
			continue
		}

		firstTs := util.ParseTime(firstPresent(row, "created_at_ms", "created_at"))
		if firstTs == nil && len(messages) > 0 {
			firstTs = messages[0].Timestamp
		}
		lastTs := util.ParseTime(firstPresent(row, "updated_at_ms", "updated_at"))
		if lastTs == nil && len(messages) > 0 {
			lastTs = messages[len(messages)-1].Timestamp
		}
		if firstTs == nil {
			if lastTs != nil {
				firstTs = lastTs
			}
		}
		if lastTs == nil {
			lastTs = firstTs
		}
		if firstTs == nil || lastTs == nil {
			continue
		}

		projectPath := util.NormalizeOptional(row["cwd"])
		runtime := processes[sessionID]
		if runtime != nil {
			if runtimePath := util.NormalizeOptional(runtime["cwd"]); runtimePath != "" {
				projectPath = runtimePath
			}
		}

		status := "finished"
		pid := pidFromProcess(runtime)
		if pid > 0 && util.IsPIDAlive(pid) {
			status = "running"
		} else if time.Since(*lastTs) < 24*time.Hour {
			status = "idle"
		}

		session := model.AgentSession{
			ID:             sessionID,
			Agent:          "codex",
			ProjectPath:    projectPath,
			Worktree:       util.FindRepoRoot(projectPath),
			GitBranch:      util.NormalizeOptional(row["git_branch"]),
			PID:            pid,
			Status:         status,
			StartedAt:      firstTs,
			LastActivityAt: lastTs,
			Title:          title,
			Model:          util.NormalizeOptional(row["model"]),
			Messages:       messages,
			SourcePath:     rollout,
			Extra: map[string]any{
				"git_origin_url":     util.NormalizeOptional(row["git_origin_url"]),
				"git_sha":            util.NormalizeOptional(row["git_sha"]),
				"agent_nickname":     util.NormalizeOptional(row["agent_nickname"]),
				"agent_role":         util.NormalizeOptional(row["agent_role"]),
				"rollout_path":       rollout,
				"first_user_message": util.NormalizeOptional(row["first_user_message"]),
			},
		}
		sessions = append(sessions, session)
	}

	util.SortByTimeDesc(sessions)
	return sessions, nil
}

func firstPresent(row map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := row[key]; ok {
			if util.NormalizeOptional(value) != "" {
				return value
			}
		}
	}
	return nil
}

func pidFromProcess(process map[string]any) int {
	if process == nil {
		return 0
	}
	if pid, ok := util.ToInt(process["pid"]); ok {
		return pid
	}
	return 0
}

func (a *CodexAdapter) loadRunningProcesses() (map[string]map[string]any, error) {
	result := map[string]map[string]any{}
	if _, err := os.Stat(a.processState); err != nil {
		return result, nil
	}

	raw, err := os.ReadFile(a.processState)
	if err != nil {
		return result, nil
	}
	entries := []any{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return result, nil
	}

	for _, entry := range entries {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		cid := util.NormalizeOptional(obj["conversationId"])
		if cid == "" {
			continue
		}

		pid, hasPID := util.ToInt(obj["osPid"])
		if !hasPID {
			pid, _ = util.ToInt(obj["processId"])
		}

		process := map[string]any{
			"pid":     pid,
			"cwd":     util.NormalizeOptional(obj["cwd"]),
			"updated": obj["updatedAtMs"],
		}
		existing, ok := result[cid]
		if !ok {
			result[cid] = process
			continue
		}
		if util.ParseTime(process["updated"]) == nil {
			result[cid] = process
			continue
		}
		if util.ParseTime(existing["updated"]) == nil || util.ParseTime(process["updated"]).After(*util.ParseTime(existing["updated"])) {
			result[cid] = process
		}
	}
	return result, nil
}

func (a *CodexAdapter) readMessages(transcriptPath string) []model.Message {
	if transcriptPath == "" {
		return nil
	}
	file, err := os.Open(transcriptPath)
	if err != nil {
		return nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	messages := make([]model.Message, 0, 40)

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
		case "response_item":
			payload, ok := record["payload"].(map[string]any)
			if !ok {
				continue
			}
			role := util.NormalizeOptional(payload["role"])
			if role != "user" && role != "assistant" {
				continue
			}
			text := util.ExtractText(payload["content"])
			if text == "" {
				continue
			}
			ts := util.ParseTime(payload["timestamp"])
			if ts == nil {
				ts = util.ParseTime(record["timestamp"])
			}
			messages = append(messages, model.Message{Role: role, Text: text, Timestamp: ts, Source: transcriptPath})
		case "event_msg":
			payload, ok := record["payload"].(map[string]any)
			if !ok {
				continue
			}
			if util.NormalizeOptional(payload["type"]) != "user_message" {
				continue
			}
			text := util.ExtractText(payload)
			if text == "" {
				continue
			}
			ts := util.ParseTime(payload["timestamp"])
			if ts == nil {
				ts = util.ParseTime(record["timestamp"])
			}
			messages = append(messages, model.Message{Role: "user", Text: text, Timestamp: ts, Source: transcriptPath})
		}
	}

	if len(messages) > 20 {
		messages = messages[len(messages)-20:]
	}
	return messages
}

func (a *CodexAdapter) Send(ctx context.Context, sessionID string, message string) error {
	if !util.CommandExists("codex") {
		return ErrUnsupported
	}
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session id is required")
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("message is required")
	}

	cmd := exec.CommandContext(ctx, "codex", "exec", "resume", sessionID, "--json", strings.TrimSpace(message))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("codex resume failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (a *CodexAdapter) OpenCommand(session model.AgentSession) (string, error) {
	if session.Agent != a.Name() {
		return "", ErrUnsupported
	}
	if strings.TrimSpace(session.ID) == "" {
		return "", ErrUnsupported
	}
	return "codex resume " + session.ID, nil
}
