package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/woyin/orangecast/internal/config"
	"github.com/woyin/orangecast/internal/models"
)

func TestNotificationHook(t *testing.T) {
	if notificationHook(&config.Config{}) != nil {
		t.Fatal("没有任何渠道时应返回 nil")
	}
	configureNotifications(nil, &config.Config{NotifyWebhookURL: "http://x"}) // worker 为 nil 时不得 panic

	got := make(chan map[string]string, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]string
		_ = json.NewDecoder(r.Body).Decode(&m)
		got <- m
	}))
	defer ts.Close()
	hook := notificationHook(&config.Config{NotifyWebhookURL: ts.URL, PublicURL: "https://oc.example/"})
	if hook == nil {
		t.Fatal("配置了 Webhook 应返回回调")
	}
	hook(context.Background(), models.SourceEpisode, "e1", "好莱坞沟通课", "摘要")
	select {
	case m := <-got:
		if m["title"] != "好莱坞沟通课" || m["summary"] != "摘要" || m["url"] != "https://oc.example/sources/episode/e1" {
			t.Errorf("payload: %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("5 秒内没有收到通知")
	}

	// 目标不可达：回调不得阻塞或 panic，只记日志。
	ts.Close()
	hook(context.Background(), models.SourceUpload, "u1", "t", "")
}
