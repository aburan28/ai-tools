package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"agent-hub/internal/adapter"
	"agent-hub/internal/model"
	"agent-hub/internal/util"
)

func main() {
	if code := run(os.Args[1:]); code != 0 {
		os.Exit(code)
	}
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}

	command := args[0]
	subArgs := args[1:]
	switch command {
	case "install":
		return cmdInstall(subArgs)
	case "mcp":
		return cmdMCP(subArgs)
	case "list":
		return cmdList(subArgs)
	case "search":
		return cmdSearch(subArgs)
	case "inspect":
		return cmdInspect(subArgs)
	case "open":
		return cmdOpen(subArgs)
	case "send":
		return cmdSend(subArgs)
	case "shell":
		return cmdShell(subArgs)
	case "help", "-h", "--help":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", command)
		printUsage()
		return 2
	}
}

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agentFilter := fs.String("agent", "all", "filter: claude, codex, opencode, all")
	showJSON := fs.Bool("json", false, "json output")
	statusOnly := fs.Bool("status", false, "print status counts")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	sessions, err := collectSessions(*agentFilter)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list failed:", err)
		return 1
	}
	if *statusOnly {
		counts := map[string]int{}
		for _, session := range sessions {
			counts[session.Status]++
		}
		keys := make([]string, 0, len(counts))
		for key := range counts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Printf("%-8s %d\n", key, counts[key])
		}
		return 0
	}

	if *showJSON {
		encoded, err := json.MarshalIndent(sessions, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "json encode failed:", err)
			return 1
		}
		fmt.Println(string(encoded))
		return 0
	}

	fmt.Printf("%-12s %-11s %-9s %-8s %-18s TITLE\n", "ID", "AGENT", "STATUS", "AGE", "BRANCH")
	fmt.Println("----------------------------------------------------------------------------------------")
	for _, session := range sessions {
		title := session.Title
		if len(title) > 90 {
			title = title[:90]
		}
		fmt.Printf("%-12s %-11s %-9s %-8s %-18s %s\n",
			session.ShortID(),
			session.Agent,
			session.Status,
			util.FormatAge(session.LastActivityAt),
			session.GitBranch,
			title,
		)
	}
	return 0
}

func cmdSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agentFilter := fs.String("agent", "all", "filter: claude, codex, opencode, all")
	limit := fs.Int("limit", 20, "max results")
	showJSON := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "search requires a query")
		return 2
	}
	query := strings.ToLower(strings.TrimSpace(strings.Join(rest, " ")))
	if query == "" {
		fmt.Fprintln(os.Stderr, "search query cannot be empty")
		return 2
	}

	sessions, err := collectSessions(*agentFilter)
	if err != nil {
		fmt.Fprintln(os.Stderr, "search failed:", err)
		return 1
	}

	tokens := []string{}
	for _, token := range strings.Fields(query) {
		if len(token) > 0 {
			tokens = append(tokens, token)
		}
	}
	if len(tokens) == 0 {
		tokens = append(tokens, query)
	}

	type scored struct {
		score   int
		session model.AgentSession
	}
	matches := make([]scored, 0)
	for _, session := range sessions {
		haystack := util.BuildSearchHaystack(session)
		score := 0
		for _, token := range tokens {
			score += strings.Count(haystack, token)
		}
		if score > 0 {
			matches = append(matches, scored{score: score, session: session})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			if matches[i].session.LastActivityAt == nil {
				return false
			}
			if matches[j].session.LastActivityAt == nil {
				return true
			}
			return matches[i].session.LastActivityAt.After(*matches[j].session.LastActivityAt)
		}
		return matches[i].score > matches[j].score
	})

	if len(matches) > *limit {
		matches = matches[:*limit]
	}

	if *showJSON {
		out := make([]any, 0, len(matches))
		for _, item := range matches {
			out = append(out, map[string]any{"score": item.score, "session": item.session})
		}
		encoded, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "json encode failed:", err)
			return 1
		}
		fmt.Println(string(encoded))
		return 0
	}

	fmt.Printf("%-8s %-12s %-11s %-9s %-8s %-90s\n", "SCORE", "ID", "AGENT", "STATUS", "AGE", "TITLE")
	fmt.Println("---------------------------------------------------------------------------------------------------------")
	for _, item := range matches {
		title := item.session.Title
		if len(title) > 90 {
			title = title[:90]
		}
		fmt.Printf("%-8d %-12s %-11s %-9s %-8s %s\n",
			item.score,
			item.session.ShortID(),
			item.session.Agent,
			item.session.Status,
			util.FormatAge(item.session.LastActivityAt),
			title,
		)
	}
	if len(matches) == 0 {
		fmt.Println("No matches.")
	}
	return 0
}

func cmdInspect(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agentFilter := fs.String("agent", "all", "filter: claude, codex, opencode, all")
	messageCount := fs.Int("messages", 10, "messages to show")
	showJSON := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "inspect requires session id")
		return 2
	}

	sessions, err := collectSessions(*agentFilter)
	if err != nil {
		fmt.Fprintln(os.Stderr, "inspect failed:", err)
		return 1
	}

	session, err := resolveSession(sessions, rest[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if *showJSON {
		encoded, err := json.MarshalIndent(session, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "json encode failed:", err)
			return 1
		}
		fmt.Println(string(encoded))
		return 0
	}

	fmt.Printf("session:      %s\n", session.CanonicalID())
	fmt.Printf("agent:        %s\n", session.Agent)
	fmt.Printf("status:       %s\n", session.Status)
	if session.PID > 0 {
		fmt.Printf("pid:          %d\n", session.PID)
	}
	fmt.Printf("projectPath:  %s\n", session.ProjectPath)
	fmt.Printf("worktree:     %s\n", session.Worktree)
	fmt.Printf("branch:       %s\n", session.GitBranch)
	fmt.Printf("model:        %s\n", session.Model)
	if session.StartedAt != nil {
		fmt.Printf("startedAt:    %s\n", session.StartedAt.Format(time.RFC3339))
	}
	if session.LastActivityAt != nil {
		fmt.Printf("lastActivity: %s\n", session.LastActivityAt.Format(time.RFC3339))
	}
	if session.SourcePath != "" {
		fmt.Printf("sourcePath:   %s\n", session.SourcePath)
	}
	fmt.Printf("title:        %s\n", session.Title)
	fmt.Printf("messages:\n")

	if len(session.Messages) == 0 {
		fmt.Println("  (no messages)")
		return 0
	}
	start := 0
	if len(session.Messages) > *messageCount {
		start = len(session.Messages) - *messageCount
	}
	for _, message := range session.Messages[start:] {
		timestamp := "unknown"
		if message.Timestamp != nil {
			timestamp = message.Timestamp.Format(time.RFC3339)
		}
		fmt.Printf("- %s @ %s\n  %s\n", message.Role, timestamp, message.Text)
	}
	return 0
}

func cmdOpen(args []string) int {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agentFilter := fs.String("agent", "all", "filter: claude, codex, opencode, all")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "open requires session id")
		return 2
	}
	adapters, err := collectAdapters(*agentFilter)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open failed:", err)
		return 1
	}

	sessions, err := collectSessionsFromAdapters(adapters)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open failed:", err)
		return 1
	}
	session, err := resolveSession(sessions, rest[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	for _, a := range adapters {
		if a.Name() != session.Agent {
			continue
		}
		command, err := a.OpenCommand(session)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open not supported for %s: %v\n", session.Agent, err)
			return 1
		}
		fmt.Println(command)
		return 0
	}

	fmt.Fprintln(os.Stderr, "open command not available")
	return 1
}

func cmdSend(args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agentFilter := fs.String("agent", "all", "filter: claude, codex, opencode, all")
	yes := fs.Bool("yes", false, "skip confirmation")
	messageText := fs.String("message", "", "explicit message text")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "send requires session id")
		return 2
	}
	message := strings.TrimSpace(*messageText)
	if message == "" {
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "send requires message text")
			return 2
		}
		message = strings.TrimSpace(strings.Join(rest[1:], " "))
	}

	target := rest[0]
	adapters, err := collectAdapters(*agentFilter)
	if err != nil {
		fmt.Fprintln(os.Stderr, "send failed:", err)
		return 1
	}
	sessions, err := collectSessionsFromAdapters(adapters)
	if err != nil {
		fmt.Fprintln(os.Stderr, "send failed:", err)
		return 1
	}
	session, err := resolveSession(sessions, target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	for _, a := range adapters {
		if a.Name() != session.Agent {
			continue
		}
		if !*yes {
			fmt.Printf("Send message to %s? [y/N]: ", session.CanonicalID())
			reader := bufio.NewReader(os.Stdin)
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(strings.ToLower(response))
			if response != "y" && response != "yes" {
				fmt.Println("aborted")
				return 0
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := a.Send(ctx, session.ID, message); err != nil {
			fmt.Fprintln(os.Stderr, "send failed:", err)
			return 1
		}
		fmt.Printf("message sent to %s (%s)\n", session.CanonicalID(), session.Agent)
		return 0
	}

	fmt.Fprintln(os.Stderr, "no matching adapter for session")
	return 1
}

func cmdShell(args []string) int {
	fs := flag.NewFlagSet("shell", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agentFilter := fs.String("agent", "all", "filter: claude, codex, opencode, all")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	adapters, err := collectAdapters(*agentFilter)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shell startup failed:", err)
		return 1
	}

	reader := bufio.NewScanner(os.Stdin)
	fmt.Println("agent-hub shell")
	fmt.Println("type 'help' for commands")

	var selected string
	for {
		fmt.Print("agent-hub> ")
		if !reader.Scan() {
			return 0
		}
		line := strings.TrimSpace(reader.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		command := strings.ToLower(parts[0])
		rest := parts[1:]

		switch command {
		case "quit", "exit", "q":
			return 0
		case "help":
			fmt.Println("commands:")
			fmt.Println("  list")
			fmt.Println("  search <query>")
			fmt.Println("  inspect <session>")
			fmt.Println("  connect <session>")
			fmt.Println("  send <message>")
			fmt.Println("  open [session]")
			fmt.Println("  shell")
			fmt.Println("  help, quit")
		case "list":
			_ = cmdList([]string{"--agent", *agentFilter})
		case "search":
			if len(rest) == 0 {
				fmt.Println("search requires a query")
				break
			}
			_ = cmdSearch(append([]string{"--agent", *agentFilter}, strings.Join(rest, " ")))
		case "inspect":
			if len(rest) != 1 {
				fmt.Println("inspect requires session")
				break
			}
			_ = cmdInspect([]string{"--agent", *agentFilter, rest[0]})
		case "connect":
			if len(rest) != 1 {
				fmt.Println("connect requires session")
				break
			}
			resolved, err := getSessionByQuery(*agentFilter, rest[0])
			if err != nil {
				fmt.Println(err)
				selected = ""
				break
			}
			selected = resolved.CanonicalID()
			fmt.Println("selected", selected)
		case "send":
			if selected == "" {
				fmt.Println("connect a session first")
				break
			}
			if len(rest) == 0 {
				fmt.Println("send requires message")
				break
			}
			message := strings.Join(rest, " ")
			session, err := getSessionByQuery(*agentFilter, selected)
			if err != nil {
				fmt.Println(err)
				break
			}
			adapter := findAdapterByAgent(adapters, session.Agent)
			if adapter == nil {
				fmt.Println("adapter unavailable")
				break
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			if err := adapter.Send(ctx, session.ID, message); err != nil {
				cancel()
				fmt.Println(err)
				break
			}
			cancel()
			fmt.Println("message sent")
		case "open":
			if len(rest) > 0 {
				selected = rest[0]
			}
			if selected == "" {
				fmt.Println("select a session first")
				break
			}
			_ = cmdOpen([]string{"--agent", *agentFilter, selected})
		case "shell":
			fmt.Println("already in shell")
		default:
			fmt.Println("unknown command; try help")
		}
	}
}

func collectSessions(agentFilter string) ([]model.AgentSession, error) {
	adapters, err := collectAdapters(agentFilter)
	if err != nil {
		return nil, err
	}
	return collectSessionsFromAdapters(adapters)
}

func collectAdapters(agentFilter string) ([]adapter.SessionAdapter, error) {
	claudeRoot := os.Getenv("CLAUDE_HOME")
	codexRoot := os.Getenv("CODEX_HOME")
	opencodeRoot := os.Getenv("OPENCODE_HOME")
	opencodeDB := os.Getenv("OPENCODE_DB_PATH")

	allowed := normalizeAgentFilter(agentFilter)
	adapters := make([]adapter.SessionAdapter, 0, 3)
	if containsAgent(allowed, "all") || containsAgent(allowed, "claude-code") {
		adapters = append(adapters, adapter.NewClaudeAdapter(claudeRoot))
	}
	if containsAgent(allowed, "all") || containsAgent(allowed, "codex") {
		adapters = append(adapters, adapter.NewCodexAdapter(codexRoot))
	}
	if containsAgent(allowed, "all") || containsAgent(allowed, "opencode") {
		adapters = append(adapters, adapter.NewOpencodeAdapter(opencodeRoot, opencodeDB))
	}
	return adapters, nil
}

func collectSessionsFromAdapters(adapters []adapter.SessionAdapter) ([]model.AgentSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sessions := make([]model.AgentSession, 0)
	for _, a := range adapters {
		rows, err := a.List(ctx)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, rows...)
	}
	util.SortByTimeDesc(sessions)
	return sessions, nil
}

func getSessionByQuery(agentFilter string, target string) (model.AgentSession, error) {
	sessions, err := collectSessions(agentFilter)
	if err != nil {
		return model.AgentSession{}, err
	}
	return resolveSession(sessions, target)
}

func resolveSession(sessions []model.AgentSession, target string) (model.AgentSession, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return model.AgentSession{}, fmt.Errorf("session id required")
	}

	var candidates []model.AgentSession

	if strings.Contains(target, ":") {
		parts := strings.SplitN(target, ":", 2)
		agent := normalizeAgentName(parts[0])
		idPart := parts[1]
		if agent == "" {
			return model.AgentSession{}, fmt.Errorf("unknown agent %q", parts[0])
		}
		for _, session := range sessions {
			if session.Agent != agent {
				continue
			}
			if session.ID == idPart || strings.HasPrefix(session.ID, idPart) || session.ShortID() == idPart {
				candidates = append(candidates, session)
			}
		}
		if len(candidates) == 1 {
			return candidates[0], nil
		}
		if len(candidates) == 0 {
			return model.AgentSession{}, fmt.Errorf("no match for %q", target)
		}
		return model.AgentSession{}, fmt.Errorf("%d sessions match %q; include full id or narrower prefix", len(candidates), target)
	}

	for _, session := range sessions {
		if session.CanonicalID() == target {
			return session, nil
		}
		if session.ID == target || strings.HasPrefix(session.ID, target) || session.ShortID() == target {
			candidates = append(candidates, session)
		}
	}

	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) == 0 {
		return model.AgentSession{}, fmt.Errorf("no match for %q", target)
	}
	return model.AgentSession{}, fmt.Errorf("%d sessions match %q; include agent prefix", len(candidates), target)
}

func normalizeAgentFilter(raw string) map[string]bool {
	parsed := map[string]bool{}
	parts := strings.Split(raw, ",")
	for _, part := range parts {
		if v := normalizeAgentName(part); v != "" {
			parsed[v] = true
		}
	}
	if len(parsed) == 0 {
		parsed["all"] = true
	}
	return parsed
}

func normalizeAgentName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "all", "":
		return "all"
	case "claude", "claude-code", "claude_code", "claude code", "claudecode", "claudeb":
		return "claude-code"
	case "codex", "codex-code":
		return "codex"
	case "opencode", "opencodeai", "open-code":
		return "opencode"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

func containsAgent(allowed map[string]bool, target string) bool {
	if allowed[target] {
		return true
	}
	if allowed["all"] {
		return true
	}
	return false
}

func findAdapterByAgent(adapters []adapter.SessionAdapter, name string) adapter.SessionAdapter {
	for _, a := range adapters {
		if a.Name() == name {
			return a
		}
	}
	return nil
}

func printUsage() {
	fmt.Println("agent-hub <command> [options]")
	fmt.Println("commands:")
	fmt.Println("  install [--force] [--bin-dir DIR] install binary and configure MCP clients")
	fmt.Println("  mcp     run the stdio MCP server (normally launched by a client)")
	fmt.Println("  list    --agent [claude|codex|opencode|all] --json --status")
	fmt.Println("  search  [--agent ...] <query> [--limit N] [--json]")
	fmt.Println("  inspect [--agent ...] <session-id>")
	fmt.Println("  open    [--agent ...] <session-id>")
	fmt.Println("  send    [--agent ...] [--yes] [--message \"text\"] <session-id> <text>")
	fmt.Println("  shell   [--agent ...] interactive mode")
}
