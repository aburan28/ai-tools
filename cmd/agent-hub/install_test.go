package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeMCPConfigCreatesRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	server := map[string]any{
		"type":    "stdio",
		"command": "/tmp/agent-hub",
		"args":    []string{"mcp"},
	}

	if err := mergeMCPConfig(path, "mcpServers", server, false, "test"); err != nil {
		t.Fatalf("mergeMCPConfig returned error: %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(contents, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	servers := config["mcpServers"].(map[string]any)
	registration := servers[mcpServerName].(map[string]any)
	if registration["command"] != "/tmp/agent-hub" {
		t.Fatalf("command = %q", registration["command"])
	}
}

func TestMergeMCPConfigPreservesExistingRegistrationWithoutForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"mcp":{"agent-hub":{"command":["old","mcp"]},"other":{"enabled":true}}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := mergeMCPConfig(path, "mcp", map[string]any{"command": []string{"new", "mcp"}}, false, "test"); err != nil {
		t.Fatalf("mergeMCPConfig returned error: %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(contents, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	servers := config["mcp"].(map[string]any)
	registration := servers[mcpServerName].(map[string]any)
	command := registration["command"].([]any)
	if command[0] != "old" {
		t.Fatalf("existing registration was replaced: %#v", registration)
	}
	if _, ok := servers["other"]; !ok {
		t.Fatal("unrelated registration was removed")
	}
}

func TestMergeMCPConfigReplacesExistingRegistrationWithForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mcp":{"agent-hub":{"command":["old","mcp"]}}}`), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := mergeMCPConfig(path, "mcp", map[string]any{"command": []string{"new", "mcp"}}, true, "test"); err != nil {
		t.Fatalf("mergeMCPConfig returned error: %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(contents, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	servers := config["mcp"].(map[string]any)
	registration := servers[mcpServerName].(map[string]any)
	command := registration["command"].([]any)
	if command[0] != "new" {
		t.Fatalf("registration was not replaced: %#v", registration)
	}
}

func TestMergeMCPConfigRejectsUnexpectedSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"mcp":"not an object"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	err := mergeMCPConfig(path, "mcp", map[string]any{"command": []string{"new", "mcp"}}, false, "test")
	if err == nil {
		t.Fatal("expected invalid section error")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(contents) != string(original) {
		t.Fatalf("invalid configuration was changed: %s", contents)
	}
}
