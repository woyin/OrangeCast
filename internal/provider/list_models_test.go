package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListModels(t *testing.T) {
	var path, auth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		switch {
		case strings.HasPrefix(r.URL.Path, "/bad"):
			w.WriteHeader(http.StatusUnauthorized)
		case strings.HasPrefix(r.URL.Path, "/junk"):
			_, _ = w.Write([]byte("not json"))
		default:
			_, _ = w.Write([]byte(`{"data":[{"id":"b"},{"id":""},{"id":"a"},{"id":"a"}]}`))
		}
	}))
	defer ts.Close()
	ctx := context.Background()

	ids, err := ListModels(ctx, "groq", ts.URL+"/v1/", "k")
	if err != nil || strings.Join(ids, ",") != "a,b" {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	if path != "/v1/models" || auth != "Bearer k" {
		t.Errorf("path=%q auth=%q", path, auth)
	}
	for _, c := range []struct{ name, prov, base, key string }{
		{"no key", "groq", ts.URL, ""},
		{"unknown provider", "nope", "", "k"},
		{"http error", "groq", ts.URL + "/bad", "k"},
		{"bad json", "groq", ts.URL + "/junk", "k"},
		{"bad url", "groq", "http://[::1", "k"},
		{"unreachable", "groq", "http://127.0.0.1:1", "k"},
	} {
		if _, err := ListModels(ctx, c.prov, c.base, c.key); err == nil {
			t.Errorf("%s: want error", c.name)
		}
	}
	// 默认地址分支：不可达时只验证报错而非 unknown provider。
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	for _, p := range []string{"groq", "openai"} {
		if _, err := ListModels(cctx, p, "", "k"); err == nil || strings.Contains(err.Error(), "未知") {
			t.Errorf("%s default base: %v", p, err)
		}
	}
}

func TestSelectorCredentials(t *testing.T) {
	sel := NewSelector("g", "o")
	sel.ApplySettings("", "https://g.example", "", "https://o.example")
	if k, u := sel.Credentials("groq"); k != "g" || u != "https://g.example" {
		t.Errorf("groq: %q %q", k, u)
	}
	if k, u := sel.Credentials("openai"); k != "o" || u != "https://o.example" {
		t.Errorf("openai: %q %q", k, u)
	}
	if k, u := sel.Credentials("x"); k != "" || u != "" {
		t.Errorf("unknown: %q %q", k, u)
	}
}
