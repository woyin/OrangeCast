package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// run 把若干行请求喂给服务，返回按行解析的响应。
func run(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var res []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad response line %q: %v", l, err)
		}
		res = append(res, m)
	}
	return res
}

func testServer() *Server {
	return &Server{Name: "orangecast", Version: "test", Tools: []Tool{
		{Name: "echo", Description: "echo", Schema: map[string]any{"type": "object"}, Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Text string }
			_ = json.Unmarshal(raw, &a)
			if a.Text == "fail" {
				return "", errors.New("tool failed")
			}
			return "echo:" + a.Text, nil
		}},
	}}
}

func TestProtocol(t *testing.T) {
	res := run(t, testServer(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"echo","arguments":{"text":"fail"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nope"}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":"bad"}`,
		`{"jsonrpc":"2.0","id":8,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","method":"unknown/notification"}`,
		``,
		`not json`,
	)
	// 8 个带 id 的请求各回复一条，外加 1 条解析错误；通知与空行不回复。
	if len(res) != 9 {
		t.Fatalf("响应数 %d: %+v", len(res), res)
	}
	byID := map[string]map[string]any{}
	for _, r := range res {
		byID[fmtID(r["id"])] = r
	}
	init := byID["1"]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" || init["serverInfo"].(map[string]any)["name"] != "orangecast" {
		t.Errorf("initialize: %+v", init)
	}
	tools := byID["3"]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "echo" {
		t.Errorf("tools/list: %+v", tools)
	}
	text := func(id string) (string, bool) {
		r := byID[id]["result"].(map[string]any)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"].(bool)
	}
	if txt, isErr := text("4"); txt != "echo:hi" || isErr {
		t.Errorf("call ok: %q %v", txt, isErr)
	}
	if txt, isErr := text("5"); txt != "tool failed" || !isErr {
		t.Errorf("call error should be a tool error: %q %v", txt, isErr)
	}
	for id, code := range map[string]float64{"6": -32602, "7": -32602, "8": -32601, "null": -32700} {
		if got := byID[id]["error"].(map[string]any)["code"]; got != code {
			t.Errorf("id %s error code %v, want %v", id, got, code)
		}
	}
	// 缺省协议版本。
	r := run(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if r[0]["result"].(map[string]any)["protocolVersion"] != defaultProtocolVersion {
		t.Errorf("default version: %+v", r[0])
	}
}

func fmtID(v any) string {
	if v == nil {
		return "null"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func TestServeStopsOnCancelAndWriteError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := testServer().Serve(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx: %v", err)
	}
	if err := testServer().Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), failWriter{}); err == nil {
		t.Error("write error should surface")
	}
	if err := testServer().Serve(context.Background(), strings.NewReader("bad\n"), failWriter{}); err == nil {
		t.Error("write error on parse-error reply should surface")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }
