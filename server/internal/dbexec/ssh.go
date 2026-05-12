package dbexec

import (
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/chy/chat2db/server/internal/config"
	cryptopkg "github.com/chy/chat2db/server/internal/crypto"
	"github.com/chy/chat2db/server/internal/model"
	"golang.org/x/crypto/ssh"
)

type sshTunnelEntry struct {
	listener net.Listener
	client   *ssh.Client
	localPort int
	version  string
}

var (
	tunnelMu sync.Mutex
	tunnels  = map[uint]*sshTunnelEntry{}
)

func getSSHTunnel(c *model.Connection) (string, int, error) {
	if !c.SSHEnabled {
		return c.Host, c.Port, nil
	}
	version := c.UpdatedAt.String()
	tunnelMu.Lock()
	if entry, ok := tunnels[c.ID]; ok && entry.version == version {
		tunnelMu.Unlock()
		return "127.0.0.1", entry.localPort, nil
	}
	if entry, ok := tunnels[c.ID]; ok {
		entry.listener.Close()
		entry.client.Close()
		delete(tunnels, c.ID)
	}
	tunnelMu.Unlock()

	sshConfig, err := buildSSHConfig(c)
	if err != nil {
		return "", 0, fmt.Errorf("ssh config: %w", err)
	}

	sshAddr := fmt.Sprintf("%s:%d", c.SSHHost, c.SSHPort)
	client, err := ssh.Dial("tcp", sshAddr, sshConfig)
	if err != nil {
		return "", 0, fmt.Errorf("ssh dial %s: %w", sshAddr, err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		return "", 0, fmt.Errorf("local listen: %w", err)
	}
	localPort := listener.Addr().(*net.TCPAddr).Port

	remoteAddr := fmt.Sprintf("%s:%d", c.Host, c.Port)
	go func() {
		for {
			local, err := listener.Accept()
			if err != nil {
				return
			}
			remote, err := client.Dial("tcp", remoteAddr)
			if err != nil {
				local.Close()
				continue
			}
			go tunnel(local, remote)
		}
	}()

	tunnelMu.Lock()
	tunnels[c.ID] = &sshTunnelEntry{
		listener:  listener,
		client:    client,
		localPort: localPort,
		version:   version,
	}
	tunnelMu.Unlock()

	return "127.0.0.1", localPort, nil
}

func buildSSHConfig(c *model.Connection) (*ssh.ClientConfig, error) {
	key := config.Get().CredentialKey
	cfg := &ssh.ClientConfig{
		User:            c.SSHUser,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * 1e9,
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
