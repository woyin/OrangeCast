package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnabled(t *testing.T) {
	var nilN *Notifier
	for name, n := range map[string]*Notifier{
		"nil": nilN, "empty": {}, "token only": {TelegramToken: "t"}, "chat only": {TelegramChatID: "c"},
	} {
		if n.Enabled() {
			t.Errorf("%s should be disabled", name)
		}
	}
	if !(&Notifier{WebhookURL: "http://x"}).Enabled() || !(&Notifier{TelegramToken: "t", TelegramChatID: "c"}).Enabled() {
		t.Error("configured channels should be enabled")
	}
	if err := nilN.Send(context.Background(), Event{}); err != nil {
		t.Errorf("disabled Send must be a no-op: %v", err)
	}
}

func TestSendWebhookAndTelegram(t *testing.T) {
	var hook map[string]string
	var tgPath string
	var tg map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/botTOKEN/") {
			tgPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&tg)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&hook)
	}))
	defer ts.Close()
	n := &Notifier{WebhookURL: ts.URL + "/hook", TelegramToken: "TOKEN", TelegramChatID: "42", TelegramEndpoint: ts.URL}
	ev := Event{Title: "好莱坞沟通课", Summary: strings.Repeat("长", 400), URL: "http://oc/sources/episode/e1"}
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if hook["event"] != "source.processed" || hook["title"] != "好莱坞沟通课" || hook["url"] != ev.URL || len([]rune(hook["summary"])) != summaryRunes+1 {
		t.Errorf("webhook payload: %+v", hook)
	}
	text, _ := tg["text"].(string)
	if tgPath != "/botTOKEN/sendMessage" || tg["chat_id"] != "42" || !strings.Contains(text, "好莱坞沟通课") || !strings.Contains(text, ev.URL) {
		t.Errorf("telegram: %s %+v", tgPath, tg)
	}
	// 没有摘要时不留空行。
	tg = nil
	if err := n.Send(context.Background(), Event{Title: "T", URL: "u"}); err != nil {
		t.Fatal(err)
	}
	if text, _ := tg["text"].(string); strings.Contains(text, "\n\n\n") {
		t.Errorf("empty summary leaves blank lines: %q", text)
	}
}

func TestSendErrorsAreCombinedAndTokenIsRedacted(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	n := &Notifier{WebhookURL: bad.URL, TelegramToken: "SECRET", TelegramChatID: "1", TelegramEndpoint: bad.URL}
	err := n.Send(context.Background(), Event{Title: "t"})
	if err == nil || !strings.Contains(err.Error(), "webhook: HTTP 500") || !strings.Contains(err.Error(), "telegram: HTTP 500") {
		t.Fatalf("both channels should report: %v", err)
	}
	bad.Close()
	err = n.Send(context.Background(), Event{Title: "t"})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("token must not leak, unreachable error: %v", err)
	}
	// 一个渠道失败不影响另一个：webhook 不可达，telegram 成功。
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	mixed := &Notifier{WebhookURL: bad.URL, TelegramToken: "T", TelegramChatID: "1", TelegramEndpoint: ok.URL}
	if err := mixed.Send(context.Background(), Event{Title: "t"}); err == nil || strings.Contains(err.Error(), "telegram") {
		t.Errorf("only webhook should fail: %v", err)
	}
	// 非法 URL。
	if err := (&Notifier{WebhookURL: "http://[::1"}).Send(context.Background(), Event{}); err == nil {
		t.Error("invalid URL should fail")
	}
}
