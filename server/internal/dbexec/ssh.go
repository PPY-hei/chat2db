package dbexec

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/chy/chat2db/server/internal/config"
	cryptopkg "github.com/chy/chat2db/server/internal/crypto"
	"github.com/chy/chat2db/server/internal/model"
	"golang.org/x/crypto/ssh"
)

type sshTunnelEntry struct {
	listener  net.Listener
	client    *ssh.Client
	localPort int
	version   string
}

var (
	tunnelMu sync.Mutex
	tunnels  = map[uint]*sshTunnelEntry{}
	// tunnelCreating tracks in-flight tunnel creation to prevent duplicate work.
	// Key is connID; value is a channel that is closed when creation completes.
	tunnelCreating = map[uint]chan struct{}{}
)

func getSSHTunnel(c *model.Connection) (string, int, error) {
	if !c.SSHEnabled {
		return c.Host, c.Port, nil
	}
	version := c.UpdatedAt.String()

	for {
		tunnelMu.Lock()

		// 1. 已有有效隧道 → 检查健康后返回
		if entry, ok := tunnels[c.ID]; ok && entry.version == version {
			// 快速健康检查：尝试通过 SSH client 发送一个 keepalive 请求
			if isSSHAlive(entry.client) {
				port := entry.localPort
				tunnelMu.Unlock()
				return "127.0.0.1", port, nil
			}
			// SSH 连接已死，清理后重建
			entry.listener.Close()
			entry.client.Close()
			delete(tunnels, c.ID)
		}

		// 2. 存在旧版本隧道 → 清理
		if entry, ok := tunnels[c.ID]; ok {
			entry.listener.Close()
			entry.client.Close()
			delete(tunnels, c.ID)
		}

		// 3. 检查是否有其他 goroutine 正在创建
		if wait, creating := tunnelCreating[c.ID]; creating {
			tunnelMu.Unlock()
			// 等待另一个 goroutine 完成创建
			<-wait
			// 重新进入循环，从 map 中获取结果
			continue
		}

		// 4. 标记本 goroutine 正在创建
		done := make(chan struct{})
		tunnelCreating[c.ID] = done
		tunnelMu.Unlock()

		// 5. 在锁外执行耗时的 SSH 拨号和端口监听
		entry, err := createSSHTunnel(c, version)

		// 6. 重新加锁，写入结果，通知等待者
		tunnelMu.Lock()
		delete(tunnelCreating, c.ID)
		if err != nil {
			tunnelMu.Unlock()
			close(done)
			return "", 0, err
		}
		tunnels[c.ID] = entry
		tunnelMu.Unlock()
		close(done)

		return "127.0.0.1", entry.localPort, nil
	}
}

// createSSHTunnel performs the actual SSH dial + local listener setup.
// Must be called without holding tunnelMu.
func createSSHTunnel(c *model.Connection, version string) (*sshTunnelEntry, error) {
	sshConfig, err := buildSSHConfig(c)
	if err != nil {
		return nil, fmt.Errorf("ssh config: %w", err)
	}

	sshAddr := fmt.Sprintf("%s:%d", c.SSHHost, c.SSHPort)
	client, err := ssh.Dial("tcp", sshAddr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", sshAddr, err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("local listen: %w", err)
	}
	localPort := listener.Addr().(*net.TCPAddr).Port

	remoteAddr := fmt.Sprintf("%s:%d", c.Host, c.Port)
	go func() {
		for {
			local, err := listener.Accept()
			if err != nil {
				return // listener closed
			}
			remote, err := client.Dial("tcp", remoteAddr)
			if err != nil {
				local.Close()
				continue
			}
			go tunnel(local, remote)
		}
	}()

	return &sshTunnelEntry{
		listener:  listener,
		client:    client,
		localPort: localPort,
		version:   version,
	}, nil
}

// isSSHAlive performs a lightweight check on the SSH connection.
// Sends a global request that the server will reject but the round-trip
// confirms the transport is still alive.
func isSSHAlive(client *ssh.Client) bool {
	// SendRequest with a short deadline; "keepalive@openssh.com" is commonly used.
	_, _, err := client.Conn.SendRequest("keepalive@openssh.com", true, nil)
	// err == nil means server responded (even with wantReply=true, rejection is ok).
	// A non-nil error with io.EOF or similar means the connection is dead.
	// Note: some servers return (false, nil) for unknown requests — that's fine.
	if err != nil {
		return false
	}
	return true
}

func buildSSHConfig(c *model.Connection) (*ssh.ClientConfig, error) {
	key := config.Get().CredentialKey
	cfg := &ssh.ClientConfig{
		User:            c.SSHUser,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	switch c.SSHAuthMethod {
	case "privatekey":
		if c.SSHPrivateKeyEnc == "" {
			return nil, fmt.Errorf("ssh private key is empty")
		}
		pemData, err := cryptopkg.DecryptString(c.SSHPrivateKeyEnc, key)
		if err != nil {
			return nil, fmt.Errorf("decrypt ssh key: %w", err)
		}
		var signer ssh.Signer
		if c.SSHPassphraseEnc != "" {
			passphrase, err := cryptopkg.DecryptString(c.SSHPassphraseEnc, key)
			if err != nil {
				return nil, fmt.Errorf("decrypt ssh passphrase: %w", err)
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(pemData), []byte(passphrase))
			if err != nil {
				return nil, fmt.Errorf("parse ssh key with passphrase: %w", err)
			}
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(pemData))
			if err != nil {
				return nil, fmt.Errorf("parse ssh key: %w", err)
			}
		}
		cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	default:
		if c.SSHPasswordEnc == "" {
			return nil, fmt.Errorf("ssh password is empty")
		}
		pwd, err := cryptopkg.DecryptString(c.SSHPasswordEnc, key)
		if err != nil {
			return nil, fmt.Errorf("decrypt ssh password: %w", err)
		}
		cfg.Auth = []ssh.AuthMethod{ssh.Password(pwd)}
	}
	return cfg, nil
}

func tunnel(local, remote net.Conn) {
	defer local.Close()
	defer remote.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(local, remote); done <- struct{}{} }()
	go func() { io.Copy(remote, local); done <- struct{}{} }()
	<-done
}

func invalidateSSHTunnel(connID uint) {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	if entry, ok := tunnels[connID]; ok {
		entry.listener.Close()
		entry.client.Close()
		delete(tunnels, connID)
	}
}
