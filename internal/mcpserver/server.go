// Package mcpserver 实现一个最小的 MCP（Model Context Protocol）服务端：
// 通过 stdio 收发按行分隔的 JSON-RPC 2.0 消息，向 AI 客户端（Claude Desktop 等）暴露只读工具。
package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

const defaultProtocolVersion = "2024-11-05"

// Tool 是一个可被客户端调用的只读工具。
type Tool struct {
	Name        string
	Description string
	// Schema 是参数的 JSON Schema（type=object）。
	Schema map[string]any
	// Run 返回给模型看的文本；返回 error 时以工具错误（isError）回给客户端，而不是协议错误。
	Run func(ctx context.Context, args json.RawMessage) (string, error)
}

// Server 处理一条 stdio 会话。
type Server struct {
	Name, Version string
	Tools         []Tool
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve 逐行读取请求直到输入结束或 ctx 取消。通知（没有 id）不回复。
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := enc.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}); err != nil {
				return err
			}
			continue
		}
		if resp := s.handle(ctx, req); resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

func (s *Server) handle(ctx context.Context, req request) *response {
	isNotification := len(req.ID) == 0
	reply := func(result any, rerr *rpcError) *response {
		if isNotification {
			return nil
		}
		return &response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr}
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := p.ProtocolVersion
		if version == "" {
			version = defaultProtocolVersion
		}
		return reply(map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": s.Name, "version": s.Version},
		}, nil)
	case "ping":
		return reply(map[string]any{}, nil)
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.Tools))
		for _, t := range s.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
		}
		return reply(map[string]any{"tools": tools}, nil)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return reply(nil, &rpcError{-32602, "invalid params"})
		}
		for _, t := range s.Tools {
			if t.Name != p.Name {
				continue
			}
			text, err := t.Run(ctx, p.Arguments)
			isErr := err != nil
			if isErr {
				text = err.Error()
			}
			return reply(map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": isErr}, nil)
		}
		return reply(nil, &rpcError{-32602, fmt.Sprintf("unknown tool: %s", p.Name)})
	default:
		if isNotification { // notifications/initialized 等：忽略
			return nil
		}
		return reply(nil, &rpcError{-32601, "method not found: " + req.Method})
	}
}
