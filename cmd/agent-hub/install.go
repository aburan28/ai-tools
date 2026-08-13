package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func cmdInstall(args []string) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot determine home directory:", err)
		return 1
	}

	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	binDir := fs.String("bin-dir", filepath.Join(home, ".local", "bin"), "installation directory")
	force := fs.Bool("force", false, "replace existing agent-hub MCP registrations")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if len(fs.Args()) != 0 {
		fmt.Fprintln(os.Stderr, "install does not accept positional arguments")
		return 2
	}

	destination := filepath.Join(filepath.Clean(*binDir), "agent-hub")
	if err := installCurrentBinary(destination); err != nil {
		fmt.Fprintln(os.Stderr, "installation failed:", err)
		return 1
	}
	fmt.Printf("Installed %s\n", destination)

	failures := 0
	if err := installCodexMCP(destination, *force); err != nil {
		failures++
		fmt.Fprintln(os.Stderr, "Codex MCP registration:", err)
	}
	if err := installClaudeMCP(home, destination, *force); err != nil {
		failures++
		fmt.Fprintln(os.Stderr, "Claude Code MCP registration:", err)
	}
	if err := installOpenCodeMCP(home, destination, *force); err != nil {
		failures++
		fmt.Fprintln(os.Stderr, "OpenCode MCP registration:", err)
	}
	if failures > 0 {
		fmt.Fprintf(os.Stderr, "Installed binary, but %d MCP registration(s) need attention. Run with --force to replace existing agent-hub registrations.\n", failures)
		return 1
	}

	fmt.Println("MCP setup complete. Restart Codex, Claude Code, and OpenCode sessions to load agent-hub.")
	return 0
}

func installCurrentBinary(destination string) error {
	source, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running executable: %w", err)
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("inspect running executable: %w", err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("running executable is not a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create installation directory: %w", err)
	}

	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open running executable: %w", err)
	}
	defer input.Close()
	temp, err := os.CreateTemp(filepath.Dir(destination), ".agent-hub-*")
	if err != nil {
		return fmt.Errorf("create temporary executable: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := io.Copy(temp, input); err != nil {
		temp.Close()
		return fmt.Errorf("copy executable: %w", err)
	}
	if err := temp.Chmod(0o755); err != nil {
		temp.Close()
		return fmt.Errorf("mark executable: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary executable: %w", err)
	}
	if err := os.Rename(tempName, destination); err != nil {
		return fmt.Errorf("activate executable: %w", err)
	}
	return nil
}

func installCodexMCP(binaryPath string, force bool) error {
	if _, err := exec.LookPath("codex"); err != nil {
		return fmt.Errorf("codex CLI is not on PATH")
	}
	exists := runQuiet("codex", "mcp", "get", mcpServerName) == nil
	if exists && !force {
		fmt.Println("Codex MCP registration already exists; leaving it unchanged")
		return nil
	}
	if exists {
		if err := runCommand("codex", "mcp", "remove", mcpServerName); err != nil {
			return err
		}
	}
	if err := runCommand("codex", "mcp", "add", mcpServerName, "--", binaryPath, "mcp"); err != nil {
		return err
	}
	fmt.Println("Configured Codex MCP server")
	return nil
}

func installClaudeMCP(home, binaryPath string, force bool) error {
	path := filepath.Join(home, ".claude.json")
	return mergeMCPConfig(path, "mcpServers", map[string]any{
		"type":    "stdio",
		"command": binaryPath,
		"args":    []string{"mcp"},
	}, force, "Claude Code")
}

func installOpenCodeMCP(home, binaryPath string, force bool) error {
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	return mergeMCPConfig(path, "mcp", map[string]any{
		"type":    "local",
		"command": []string{binaryPath, "mcp"},
		"enabled": true,
	}, force, "OpenCode")
}

func mergeMCPConfig(path, section string, server map[string]any, force bool, clientName string) error {
	config := map[string]any{}
	contents, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(contents) > 0 {
		if err := json.Unmarshal(contents, &config); err != nil {
			return fmt.Errorf("%s is not valid JSON; refusing to rewrite it", path)
		}
	}

	servers, exists := config[section].(map[string]any)
	if existing, present := config[section]; present && (!exists || servers == nil) {
		return fmt.Errorf("%s has an unexpected %q section (%T); refusing to replace it", path, section, existing)
	}
	if !exists {
		servers = map[string]any{}
		config[section] = servers
	}
	if _, exists := servers[mcpServerName]; exists && !force {
		fmt.Printf("%s MCP registration already exists; leaving it unchanged\n", clientName)
		return nil
	}
	servers[mcpServerName] = server

	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s configuration: %w", clientName, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".agent-hub-config-*")
	if err != nil {
		return fmt.Errorf("create temporary configuration: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(append(encoded, '\n')); err != nil {
		temp.Close()
		return fmt.Errorf("write temporary configuration: %w", err)
	}
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("set configuration permissions: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary configuration: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("activate configuration: %w", err)
	}
	fmt.Printf("Configured %s MCP server\n", clientName)
	return nil
}

func runQuiet(name string, args ...string) error {
	command := exec.Command(name, args...)
	return command.Run()
}

func runCommand(name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
