package safehttp

import (
	"net"
	"testing"
)

func TestValidateURL(t *testing.T) {
	good := []string{"https://example.com/feed.xml", "http://localhost:8080/audio.mp3"}
	for _, u := range good {
		if err := ValidateURL(u); err != nil {
			t.Errorf("%q 应通过校验，实际 %v", u, err)
		}
	}
	bad := []string{"ftp://example.com", "file:///etc/passwd", "javascript:alert(1)", "", "://no-scheme"}
	for _, u := range bad {
		if err := ValidateURL(u); err == nil {
			t.Errorf("%q 应被拒绝", u)
		}
	}
}

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.1", "172.16.5.5", "192.168.1.1", "169.254.1.1", "::1", "fc00::1", "fe80::1", "0.0.0.0"}
	for _, ip := range blocked {
		if !IsBlockedIP(net.ParseIP(ip)) {
			t.Errorf("%s 应被拦截", ip)
		}
	}
	public := []string{"8.8.8.8", "1.1.1.1", "203.0.113.5", "2606:4700:4700::1111"}
	for _, ip := range public {
		if IsBlockedIP(net.ParseIP(ip)) {
			t.Errorf("%s 是公网 IP，不应拦截", ip)
		}
	}
}

func TestNewClient_NilCheckRedirect(t *testing.T) {
	// NewClient 不应 panic
	c := NewClient(5, 1024, 0)
	if c == nil {
		t.Fatal("NewClient 不应返回 nil")
	}
}

func TestContentDispositionName(t *testing.T) {
	cases := map[string]string{
		`attachment; filename="audio.mp3"`:     "audio.mp3",
		`attachment; filename=report.pdf`:      "report.pdf",
		`inline`:                               "",
		`attachment; filename*="utf-8''x.mp3"`: "",
	}
	for raw, want := range cases {
		got := ContentDispositionName(raw)
		if got != want {
			t.Errorf("ContentDispositionName(%q)=%q want %q", raw, got, want)
		}
	}
}

func TestLimitBody(t *testing.T) {
	data := []byte("hello world")
	r := LimitBody(bytesReader(data), 5)
	buf := make([]byte, 10)
	n, _ := r.Read(buf)
	if n != 5 {
		t.Errorf("LimitBody 应限制读取 5 字节，实际 %d", n)
	}
}

// bytesReader 避免引入 bytes 包到测试里（safehttp 已有 io）。
func bytesReader(b []byte) interface{ Read([]byte) (int, error) } {
	return &simpleReader{data: b}
}

type simpleReader struct {
	data []byte
	pos  int
}

func (r *simpleReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, errEOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

var errEOF = netClosed("EOF")

type netClosed string

func (e netClosed) Error() string { return string(e) }

// TestSafeDialer_BlockedIP 验证 Dial 拒绝私网地址（SSRF 防护）。
func TestSafeDialer_BlockedIP(t *testing.T) {
	d := safeDialer{}
	// 127.0.0.1 是私网，应被拦截
	if _, err := d.Dial("tcp", "127.0.0.1:8080"); err != ErrBlockedAddress {
		t.Errorf("私网地址应 ErrBlockedAddress，实际 %v", err)
	}
	// 非法地址 → 报错
	if _, err := d.Dial("tcp", "not-an-addr"); err == nil {
		t.Fatal("非法地址应报错")
	}
}
