package dbexec

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/chy/chat2db/server/internal/config"
	cryptopkg "github.com/chy/chat2db/server/internal/crypto"
	"github.com/chy/chat2db/server/internal/model"
	"golang.org/x/net/proxy"
)

// proxyDialTimeout 是经代理建立到目标的整体拨号超时。
const proxyDialTimeout = 10 * time.Second

// proxyEnabled 判断本连接是否应通过代理拨号。
// 代理与 SSH 隧道互斥：SSH 开启时优先走隧道，代理被忽略。
func proxyEnabled(c *model.Connection) bool {
	return c.ProxyEnabled && !c.SSHEnabled
}

// proxyCreds 解密代理认证凭据。用户名为空视为无认证（密码一并忽略）。
func proxyCreds(c *model.Connection) (user, pass string, err error) {
	if c.ProxyUsername == "" {
		return "", "", nil
	}
	user = c.ProxyUsername
	if c.ProxyPasswordEnc != "" {
		pass, err = cryptopkg.DecryptString(c.ProxyPasswordEnc, config.Get().CredentialKey)
		if err != nil {
			return "", "", fmt.Errorf("decrypt proxy password: %w", err)
		}
	}
	return user, pass, nil
}

// proxyDialContext 通过连接配置的代理建立到 targetAddr 的 TCP 连接。
// 按 ProxyType 分发到 SOCKS5 或 HTTP CONNECT，默认 HTTP。
func proxyDialContext(ctx context.Context, c *model.Connection, targetAddr string) (net.Conn, error) {
	if c.ProxyHost == "" {
		return nil, fmt.Errorf("proxy host is empty")
	}
	proxyAddr := net.JoinHostPort(c.ProxyHost, fmt.Sprintf("%d", c.ProxyPort))
	user, pass, err := proxyCreds(c)
	if err != nil {
		return nil, err
	}

	switch c.ProxyType {
	case "socks5":
		var auth *proxy.Auth
		if user != "" {
			auth = &proxy.Auth{User: user, Password: pass}
		}
		base := &net.Dialer{Timeout: proxyDialTimeout}
		d, err := proxy.SOCKS5("tcp", proxyAddr, auth, base)
		if err != nil {
			return nil, fmt.Errorf("socks5 proxy %s: %w", proxyAddr, err)
		}
		cd, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("socks5 dialer does not support context")
		}
		return cd.DialContext(ctx, "tcp", targetAddr)
	default: // "http" 或空
		return httpConnectDial(ctx, proxyAddr, targetAddr, user, pass)
	}
}

// httpConnectDial 通过 HTTP CONNECT 代理建立到 targetAddr 的隧道。
func httpConnectDial(ctx context.Context, proxyAddr, targetAddr, user, pass string) (net.Conn, error) {
	d := &net.Dialer{Timeout: proxyDialTimeout}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("dial http proxy %s: %w", proxyAddr, err)
	}

	// 用 ctx 的 deadline 约束 CONNECT 握手；ctx 无 deadline 时回落到固定超时。
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(proxyDialTimeout)
	}
	_ = conn.SetDeadline(deadline)

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", targetAddr, targetAddr)
	if user != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		req += "Proxy-Authorization: Basic " + cred + "\r\n"
	}
	req += "\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write CONNECT to proxy: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "CONNECT"})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("proxy CONNECT failed: %s", resp.Status)
	}

	// 握手成功，清除 deadline，交还给后续协议层管理。
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}
