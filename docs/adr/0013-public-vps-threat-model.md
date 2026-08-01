# ADR-0013：按公网 VPS 服务建立安全基线

CloudWisePod 默认按可能通过 HTTPS 暴露在公网的单 Owner 服务设计，不依赖“只有自己知道地址”或可信内网假设。所有改变状态的请求必须验证 CSRF，登录必须限流，会话 Cookie 必须在反向代理场景正确保持安全属性；RSS feed 与 Episode 音频下载共享限制响应体、限制重定向次数并逐跳校验目标地址的 SSRF 防护。

Go 应用不管理证书，生产 HTTPS 由 Caddy、Nginx 或平台受信任反向代理终止；CloudWisePod 依据显式配置的公开 URL 设置安全 Cookie，只在明确配置后信任指定代理的转发头，localhost 开发环境可以使用 HTTP。该决策接受额外安全代码与代理配置，以保持单二进制职责清晰，并避免单 Owner 被误解为低风险。
