package util

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"agent-hub/internal/model"
)

var nullLikePattern = regexp.MustCompile(`^(none|null)$`)

func NormalizeOptional(value any) string {
	if value == nil {
		return ""
	}

	switch typed := value.(type) {
	case string:
		value = strings.TrimSpace(typed)
		if value == "" || nullLikePattern.MatchString(strings.ToLower(value)) {
			return ""
		}
		return value
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}

func ParseTime(value any) *time.Time {
	if value == nil {
		return nil
	}

	var ts float64
	switch typed := value.(type) {
	case time.Time:
		result := typed
		if result.IsZero() {
			return nil
		}
		if result.Location() != time.UTC {
			result = result.UTC()
		}
		return &result
	case string:
		t := strings.TrimSpace(typed)
		if t == "" || nullLikePattern.MatchString(strings.ToLower(t)) {
			return nil
		}
		if num, err := strconv.ParseFloat(t, 64); err == nil {
			ts = num
			break
		}
		if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
		return nil
	case int:
		ts = float64(typed)
	case int64:
		ts = float64(typed)
	case float64:
		ts = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return nil
		}
		ts = parsed
	default:
		return nil
	}

	if ts == 0 {
		return nil
	}

	if ts > 1_000_000_000_0 {
		ts = ts / 1000
	}

	es := ts - float64(int64(ts))
	seconds := int64(ts)
	t := time.Unix(seconds, int64(es*1e9)).UTC()
	return &t
}

func ParseFloat(value string) (float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("empty")
	}
	return strconv.ParseFloat(value, 64)
}

func ParseInt(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("empty")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}
	return int(parsed), nil
}

func ExtractText(value any) string {
	if value == nil {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if piece := ExtractText(item); piece != "" {
				parts = append(parts, piece)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		parts := make([]string, 0, 6)
		for _, key := range []string{"text", "message", "input_text", "content"} {
			if valueValue, ok := typed[key]; ok {
				if text := ExtractText(valueValue); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func IsPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(0); err != nil {
		return false
	}
	return true
}

func FormatAge(ts *time.Time) string {
	if ts == nil {
		return "unknown"
	}
	now := time.Now().UTC()
	delta := now.Sub(*ts)
	if delta < time.Minute {
		return fmt.Sprintf("%ds", int(delta.Seconds()))
	}
	if delta < time.Hour {
		return fmt.Sprintf("%dm", int(delta.Minutes()))
	}
	if delta < 24*time.Hour {
		return fmt.Sprintf("%dh", int(delta.Hours()))
	}
	if delta < 30*24*time.Hour {
		return fmt.Sprintf("%dd", int(delta.Hours())/24)
	}
	return fmt.Sprintf("%dmo", int(delta.Hours())/24/30)
}

func FindRepoRoot(path string) string {
	if path == "" {
		return ""
	}

	current := filepath.Clean(path)
	for i := 0; i < 32; i++ {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return ""
}

func BuildSearchHaystack(session model.AgentSession) string {
	values := []string{
		session.ID,
		session.Agent,
		session.Title,
		session.ProjectPath,
		session.Worktree,
		session.GitBranch,
		session.Model,
	}
	for _, msg := range session.Messages {
		values = append(values, msg.Text)
	}
	return strings.ToLower(strings.Join(values, " "))
}

func CommandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func HomeAwarePath(value string) string {
	if strings.HasPrefix(value, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(value, "~"))
		}
	}
	return value
}

func RunSQLiteJSON(dbPath string, query string) ([]map[string]any, error) {
	cmd := exec.Command("sqlite3", "-json", dbPath, query)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sqlite query failed for %q: %w (%s)", dbPath, err, strings.TrimSpace(string(out)))
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return []map[string]any{}, nil
	}

	rows := make([]map[string]any, 0)
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("sqlite output parse failed for %q: %w", dbPath, err)
	}
	return rows, nil
}

func SQLiteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func SortByTimeDesc(items []model.AgentSession) {
	sort.Slice(items, func(i, j int) bool {
		a := items[i].LastActivityAt
		b := items[j].LastActivityAt
		if a == nil && b == nil {
			return items[i].ID < items[j].ID
		}
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		return a.After(*b)
	})
}

func ToInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	case string:
		parsed, err := ParseInt(typed)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}
