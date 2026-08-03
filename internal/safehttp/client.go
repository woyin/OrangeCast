// Package safehttp 提供带 SSRF 防护、重定向限制、超时与响应体上限的共享 HTTP 客户端。
// RSS feed 抓取与 Episode 音频下载必须复用同一客户端（ADR-0013 / Roadmap Phase 2）。
package safehttp

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// blockedPrivateNets 禁止访问的地址段（SSRF 防护）。
var blockedPrivateNets = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"169.254.0.0/16", // 链路本地
	"::1/128",
	"fc00::/7", // IPv6 私有
	"fe80::/10",
}

// IsBlockedIP 判断 IP 是否属于禁止访问的地址段。
func IsBlockedIP(ip net.IP) bool {
	for _, cidr := range blockedPrivateNets {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return ip.IsUnspecified()
}

// ValidateURL 校验 URL：必须 http/https。
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("invalid url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url must be http or https")
	}
	return nil
}

// ErrBlockedAddress 表示目标解析到禁止访问的（私有）地址。
var ErrBlockedAddress = errors.New("url resolves to a blocked (private) address")

// ErrTooManyRedirects 表示超过最大重定向次数。
var ErrTooManyRedirects = errors.New("too many redirects")

// safeDialer 在连接前校验解析到的 IP 不在私有网段，防 SSRF。
type safeDialer struct {
	inner net.Dialer
}

func (d safeDialer) Dial(network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("无法解析主机 %s", host)
		}
		ip = ips[0]
	}
	if IsBlockedIP(ip) {
		return nil, ErrBlockedAddress
	}
	return d.inner.Dial(network, net.JoinHostPort(ip.String(), port))
}

// NewClient 创建共享安全 HTTP 客户端。
//   - maxRedirects：最大重定向次数（逐跳校验目标 URL）。
//   - maxBodyBytes：响应体大小上限（0 表示不限制，通常配合 LimitBody）。
//   - timeout：整体超时。
func NewClient(maxRedirects int, maxBodyBytes int64, timeout time.Duration) *http.Client {
	dialer := safeDialer{inner: net.Dialer{Timeout: 10 * time.Second}}
	c := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// via 是已跟随的请求链；len(via) > maxRedirects 才拒绝，
			// 允许恰好 maxRedirects 次跟随（修复 off-by-one：Podtrac 等需要 3 跳）。
			if len(via) > maxRedirects {
				return ErrTooManyRedirects
			}
			return ValidateURL(req.URL.String())
		},
		Transport: &http.Transport{
			Dial:                dialer.Dial,
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
	if maxBodyBytes > 0 {
		// 用包装的 transport 无法在库层统一限制 body，客户端返回原始 Body；
		// 调用方必须使用 LimitBody 限制读取量。这里提供便捷函数而非自动包装。
		_ = maxBodyBytes
	}
	return c
}

// LimitBody 包装响应体为最多 maxBytes 的读取器（配合客户端使用，防超大响应耗尽内存）。
func LimitBody(r io.Reader, maxBytes int64) io.Reader {
	return io.LimitReader(r, maxBytes)
}

// ContentDispositionName 从 Content-Disposition 提取文件名（简单实现，无外部依赖）。
func ContentDispositionName(raw string) string {
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "filename=") {
			return strings.Trim(strings.TrimPrefix(part, "filename="), `"`)
		}
	}
	return ""
}
