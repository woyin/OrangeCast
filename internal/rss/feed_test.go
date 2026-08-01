package rss

import (
	"net"
	"testing"

	"github.com/woyin/orangecast/internal/safehttp"
)

func TestValidateFeedURL(t *testing.T) {
	good := []string{"https://feed.example.com/x.xml", "http://localhost/feed"}
	for _, u := range good {
		if safehttp.ValidateURL(u) != nil {
			t.Errorf("%q 应通过校验", u)
		}
	}
	bad := []string{"ftp://x.com/feed", "file:///etc/passwd", "javascript:alert(1)", "://no-scheme"}
	for _, u := range bad {
		if safehttp.ValidateURL(u) == nil {
			t.Errorf("%q 应被拒绝", u)
		}
	}
}

func TestIsBlockedIP_PrivateAddresses(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.1", "172.16.5.5", "192.168.1.1", "169.254.1.1", "::1", "fc00::1", "fe80::1", "0.0.0.0"}
	for _, ip := range blocked {
		if !safehttp.IsBlockedIP(net.ParseIP(ip)) {
			t.Errorf("%s 应被 SSRF 防护拦截", ip)
		}
	}
}

func TestIsBlockedIP_PublicAllowed(t *testing.T) {
	public := []string{"8.8.8.8", "1.1.1.1", "203.0.113.5"}
	for _, ip := range public {
		if safehttp.IsBlockedIP(net.ParseIP(ip)) {
			t.Errorf("%s 是公网 IP，不应被拦截", ip)
		}
	}
}
