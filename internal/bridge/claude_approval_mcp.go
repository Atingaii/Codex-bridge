package bridge

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Input     json.RawMessage `json:"input"`
}

func RunClaudeApprovalMCP(socketPath string, stdin io.Reader, stdout io.Writer) error {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return errors.New("approval socket is required")
	}
	reader := bufio.NewReaderSize(stdin, 64*1024)
	encoder := json.NewEncoder(stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.ID == nil {
			continue
		}
		res, err := handleClaudeApprovalMCPRequest(socketPath, req)
		if err != nil {
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]any{
					"code":    -32000,
					"message": err.Error(),
				},
			})
			continue
		}
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
	}
}

func handleClaudeApprovalMCPRequest(socketPath string, req mcpRequest) (any, error) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": "codex_bridge", "version": "dev"},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		}, nil
	case "tools/list":
		return map[string]any{
			"tools": []map[string]any{{
				"name":        "browser_approval",
				"description": "Ask the Codex Bridge browser to approve or deny a Claude Code permission prompt.",
				"inputSchema": map[string]any{
					"type":                 "object",
					"additionalProperties": true,
					"properties": map[string]any{
						"command": map[string]any{"type": "string"},
						"cwd":     map[string]any{"type": "string"},
						"reason":  map[string]any{"type": "string"},
					},
				},
			}},
		}, nil
	case "tools/call":
		return handleClaudeApprovalMCPToolCall(socketPath, req.Params)
	default:
		return map[string]any{}, nil
	}
}

func handleClaudeApprovalMCPToolCall(socketPath string, rawParams json.RawMessage) (any, error) {
	var params mcpToolCallParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, err
	}
	input := params.Arguments
	if len(input) == 0 {
		input = params.Input
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	socketReq := claudeApprovalSocketRequest{
		RequestID: fmt.Sprintf("claude_%d", time.Now().UnixNano()),
		Kind:      "claude.permission_prompt",
		ToolName:  params.Name,
		Input:     input,
		Command:   claudeApprovalCommand(params.Name, input),
		Reason:    claudeApprovalReason(input),
	}
	if cwd := claudeApprovalCWD(input); cwd != "" {
		socketReq.CWD = cwd
	}
	socketRes, err := callClaudeApprovalSocket(socketPath, socketReq)
	if err != nil {
		return nil, err
	}
	allow := socketRes.Decision == "accept"
	behavior := "deny"
	text := "Denied by browser approval."
	if allow {
		behavior = "allow"
		text = "Allowed by browser approval."
	}
	result := map[string]any{
		"content": []map[string]any{{
			"type": "text",
			"text": text,
		}},
		"structuredContent": map[string]any{
			"behavior":     behavior,
			"updatedInput": map[string]any{},
			"message":      text,
		},
		"behavior": behavior,
		"message":  text,
	}
	if allow {
		result["updatedInput"] = map[string]any{}
	} else {
		result["isError"] = true
	}
	return result, nil
}

func callClaudeApprovalSocket(socketPath string, req claudeApprovalSocketRequest) (claudeApprovalSocketResponse, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return claudeApprovalSocketResponse{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return claudeApprovalSocketResponse{}, err
	}
	var res claudeApprovalSocketResponse
	if err := json.NewDecoder(conn).Decode(&res); err != nil {
		return claudeApprovalSocketResponse{}, err
	}
	if res.Error != "" && res.Decision == "" {
		return res, errors.New(res.Error)
	}
	if res.Decision == "" {
		res.Decision = "cancel"
	}
	return res, nil
}

func claudeApprovalCWD(raw json.RawMessage) string {
	input := map[string]any{}
	_ = json.Unmarshal(raw, &input)
	return stringifyApprovalValue(input["cwd"])
}

func RunClaudeApprovalMCPFromEnv(socketPath string) error {
	return RunClaudeApprovalMCP(socketPath, os.Stdin, os.Stdout)
}
