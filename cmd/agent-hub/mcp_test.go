package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"agent-hub/internal/model"
)

func TestServeMCPInitializeAndToolsList(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n")
	var output bytes.Buffer

	if code := serveMCP(input, &output); code != 0 {
		t.Fatalf("serveMCP returned %d", code)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d: %s", len(lines), output.String())
	}

	var initializeResponse struct {
		ID     int `json:"id"`
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &initializeResponse); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if initializeResponse.ID != 1 {
		t.Fatalf("initialize response id = %d, want 1", initializeResponse.ID)
	}
	if initializeResponse.Result.ProtocolVersion != "2025-03-26" {
		t.Fatalf("protocol version = %q", initializeResponse.Result.ProtocolVersion)
	}
	if initializeResponse.Result.ServerInfo.Name != mcpServerName {
		t.Fatalf("server name = %q", initializeResponse.Result.ServerInfo.Name)
	}

	var toolsResponse struct {
		ID     int `json:"id"`
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &toolsResponse); err != nil {
		t.Fatalf("decode tools response: %v", err)
	}
	if toolsResponse.ID != 2 {
		t.Fatalf("tools response id = %d, want 2", toolsResponse.ID)
	}
	want := map[string]bool{
		"sessions.list":    false,
		"sessions.search":  false,
		"sessions.inspect": false,
		"sessions.open":    false,
		"sessions.send":    false,
	}
	for _, tool := range toolsResponse.Result.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q was not advertised", name)
		}
	}
}

func TestServeMCPRejectsMalformedRequests(t *testing.T) {
	input := strings.NewReader("not json\n")
	var output bytes.Buffer

	if code := serveMCP(input, &output); code != 0 {
		t.Fatalf("serveMCP returned %d", code)
	}

	var response struct {
		Error mcpError `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Code != -32700 {
		t.Fatalf("error code = %d, want -32700", response.Error.Code)
	}
}

func TestSessionsSendRequiresExplicitConfirmation(t *testing.T) {
	result, rpcErr := callMCPTool(json.RawMessage(`{
        "name": "sessions.send",
        "arguments": {"session_id": "codex:abc", "message": "continue"}
    }`))
	if rpcErr != nil {
		t.Fatalf("unexpected RPC error: %#v", rpcErr)
	}

	toolResult, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("tool response type = %T", result)
	}
	if isError, _ := toolResult["isError"].(bool); !isError {
		t.Fatalf("send without confirmation should return a tool error: %#v", toolResult)
	}
}

func TestSummarizeSessionStripsTranscriptMessages(t *testing.T) {
	session := model.AgentSession{
		ID:       "thread-1",
		Agent:    "codex",
		Messages: []model.Message{{Role: "user", Text: "untrusted transcript content"}},
	}

	summary := summarizeSession(session)
	if len(summary.Messages) != 0 {
		t.Fatalf("summary retained %d messages", len(summary.Messages))
	}
	if len(session.Messages) != 1 {
		t.Fatal("summarizeSession mutated the original session")
	}
}
