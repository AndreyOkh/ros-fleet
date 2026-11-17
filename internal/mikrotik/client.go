package mikrotik

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/AndreyOkh/ros-fleet/internal/config"
)

var (
	// fileNameExtractRegex finds name="..." in MikroTik `/file print` output.
	fileNameExtractRegex = regexp.MustCompile(`name="([^"]+)"`)
)

// DeviceInfo contains hardware and software telemetry retrieved from the router.
type DeviceInfo struct {
	Model       string `json:"model"`
	Version     string `json:"version"`
	Uptime      string `json:"uptime,omitempty"`
	BoardName   string `json:"board_name,omitempty"`
	CPU         string `json:"cpu,omitempty"`
	CPULoad     string `json:"cpu_load,omitempty"`
	FreeMemory  string `json:"free_memory,omitempty"`
	TotalMemory string `json:"total_memory,omitempty"`
}

// Client handles SSH and SFTP interactions with a MikroTik RouterOS device.
type Client struct {
	routerConfig config.RouterConfig
	sshClient    *ssh.Client
	sftpClient   *sftp.Client
	logger       *slog.Logger
}

// Connect establishes an SSH connection and an SFTP session to the router.
func Connect(ctx context.Context, router config.RouterConfig, timeout time.Duration, globalInsecureHostKey bool, logger *slog.Logger) (*Client, error) {
	log := logger.With(slog.String("router", router.Name), slog.String("host", router.Host))

	authMethods, err := buildAuthMethods(router)
	if err != nil {
		return nil, fmt.Errorf("auth configuration error for router %q: %w", router.Name, err)
	}

	hostKeyCallback, err := buildHostKeyCallback(router, globalInsecureHostKey)
	if err != nil {
		return nil, fmt.Errorf("host key verification config error for router %q: %w", router.Name, err)
	}

	sshConfig := &ssh.ClientConfig{
		User:            router.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         timeout,
		Config: ssh.Config{
			Ciphers: []string{
				"chacha20-poly1305@openssh.com",
				"aes128-gcm@openssh.com",
				"aes256-gcm@openssh.com",
				"aes128-ctr",
				"aes192-ctr",
				"aes256-ctr",
				"aes128-cbc",
				"aes256-cbc",
			},
			KeyExchanges: []string{
				"curve25519-sha256",
				"curve25519-sha256@libssh.org",
				"ecdh-sha2-nistp256",
				"ecdh-sha2-nistp384",
				"ecdh-sha2-nistp521",
				"diffie-hellman-group14-sha256",
				"diffie-hellman-group14-sha1",
				"diffie-hellman-group-exchange-sha256",
				"diffie-hellman-group-exchange-sha1",
			},
		},
	}

	addr := fmt.Sprintf("%s:%d", router.Host, router.Port)
	log.Debug("Dialing router via SSH", slog.String("addr", addr))

	dialer := &net.Dialer{Timeout: timeout}
	netConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("network dial error to %s: %w", addr, err)
	}

	type handshakeResult struct {
		client *ssh.Client
		err    error
	}
	sshConnChan := make(chan handshakeResult, 1)

	go func() {
		c, chans, reqs, hErr := ssh.NewClientConn(netConn, addr, sshConfig)
		if hErr != nil {
			_ = netConn.Close()
			sshConnChan <- handshakeResult{err: hErr}
			return
		}
		sshConnChan <- handshakeResult{client: ssh.NewClient(c, chans, reqs)}
	}()

	var sshClient *ssh.Client
	select {
	case <-ctx.Done():
		_ = netConn.Close()
		return nil, fmt.Errorf("connection to %s cancelled: %w", addr, ctx.Err())
	case res := <-sshConnChan:
		if res.err != nil {
			return nil, fmt.Errorf("ssh handshake failed with %s: %w", addr, res.err)
		}
		sshClient = res.client
	}

	// Initialize SFTP client
	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return nil, fmt.Errorf("failed to initialize SFTP subsystem on %s: %w", addr, err)
	}

	log.Debug("SSH and SFTP connection established successfully")
	return &Client{
		routerConfig: router,
		sshClient:    sshClient,
		sftpClient:   sftpClient,
		logger:       log,
	}, nil
}

// buildAuthMethods sets up password and/or private key authentication.
func buildAuthMethods(r config.RouterConfig) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	if r.Password != "" {
		methods = append(methods, ssh.Password(r.Password))
	}

	if r.PrivateKeyPath != "" {
		keyPath := filepath.Clean(r.PrivateKeyPath)
		keyBytes, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read private key %q: %w", keyPath, err)
		}

		var signer ssh.Signer
		if r.PrivateKeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(keyBytes, []byte(r.PrivateKeyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(keyBytes)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to parse private key %q: %w", keyPath, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	if len(methods) == 0 {
		return nil, errors.New("no authentication methods provided (neither password nor private key)")
	}

	return methods, nil
}

// buildHostKeyCallback configures host key checking.
func buildHostKeyCallback(r config.RouterConfig, globalInsecureHostKey bool) (ssh.HostKeyCallback, error) {
	if r.ShouldIgnoreHostKey(globalInsecureHostKey) {
		return ssh.InsecureIgnoreHostKey(), nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("could not determine user home directory for known_hosts: %w", err)
	}

	knownHostsFile := filepath.Join(homeDir, ".ssh", "known_hosts")
	cb, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read known_hosts file %q: %w (tip: enable 'insecure_ignore_host_key: true' if using self-signed router keys)", knownHostsFile, err)
	}

	return cb, nil
}

// RunCommand executes a command on the MikroTik router and returns combined stdout/stderr output.
func (c *Client) RunCommand(ctx context.Context, cmd string) (string, error) {
	c.logger.Debug("Executing RouterOS command", slog.String("cmd", cmd))

	session, err := c.sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	type cmdResult struct {
		output []byte
		err    error
	}

	done := make(chan cmdResult, 1)
	go func() {
		out, runErr := session.CombinedOutput(cmd)
		done <- cmdResult{output: out, err: runErr}
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		return "", fmt.Errorf("command execution cancelled: %w", ctx.Err())
	case res := <-done:
		outStr := strings.TrimSpace(string(res.output))
		if res.err != nil {
			return outStr, fmt.Errorf("command %q exited with error: %w (output: %q)", cmd, res.err, outStr)
		}
		return outStr, nil
	}
}

// FetchDeviceInfo queries system resource and routerboard information from the device.
func (c *Client) FetchDeviceInfo(ctx context.Context) (*DeviceInfo, error) {
	resourceOut, err := c.RunCommand(ctx, "/system resource print")
	if err != nil {
		return nil, fmt.Errorf("failed to query /system resource print: %w", err)
	}

	resourceMap := parseRouterOSKeyValues(resourceOut)

	// Query /system routerboard print (may fail on CHR / x86, which is normal)
	routerboardOut, _ := c.RunCommand(ctx, "/system routerboard print")
	rbMap := parseRouterOSKeyValues(routerboardOut)

	info := &DeviceInfo{
		Version:     resourceMap["version"],
		BoardName:   resourceMap["board-name"],
		Uptime:      resourceMap["uptime"],
		CPU:         resourceMap["cpu"],
		CPULoad:     resourceMap["cpu-load"],
		FreeMemory:  resourceMap["free-memory"],
		TotalMemory: resourceMap["total-memory"],
	}

	// Model selection: check routerboard model first, then board-name, then platform
	if rbModel := rbMap["model"]; rbModel != "" {
		info.Model = rbModel
	} else if info.BoardName != "" {
		info.Model = info.BoardName
	} else if platform := resourceMap["platform"]; platform != "" {
		info.Model = platform
	} else {
		info.Model = "RouterOS Device"
	}

	if info.Version == "" {
		info.Version = rbMap["current-firmware"]
	}
	if info.Version == "" {
		info.Version = "Unknown"
	}

	c.logger.Debug("Fetched device info",
		slog.String("model", info.Model),
		slog.String("version", info.Version),
		slog.String("uptime", info.Uptime),
	)

	return info, nil
}

// parseRouterOSKeyValues parses output lines with "key: value" into a lowercase map.
func parseRouterOSKeyValues(output string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.ToLower(strings.TrimSpace(line[:idx]))
			val := strings.TrimSpace(line[idx+1:])
			result[key] = val
		}
	}
	return result
}

// CreateExport generates a configuration export (.rsc) on the router.
func (c *Client) CreateExport(ctx context.Context, baseFilename string, exportSensitive bool) error {
	if exportSensitive {
		cmdWithSensitive := fmt.Sprintf(`/export show-sensitive file="%s"`, baseFilename)
		out, err := c.RunCommand(ctx, cmdWithSensitive)
		if err == nil {
			c.logger.Debug("Export completed with show-sensitive enabled")
			return nil
		}

		c.logger.Debug("show-sensitive export command failed (likely RouterOS v6), attempting fallback",
			slog.String("output", out),
			slog.Any("error", err),
		)
	}

	standardCmd := fmt.Sprintf(`/export file="%s"`, baseFilename)
	out, err := c.RunCommand(ctx, standardCmd)
	if err != nil {
		return fmt.Errorf("failed to generate export file: %w (output: %s)", err, out)
	}

	c.logger.Debug("Standard export command completed")
	return nil
}

// CreateBackup generates a binary backup (.backup) on the router.
func (c *Client) CreateBackup(ctx context.Context, baseFilename string, encrypt bool, password string) error {
	if encrypt && password != "" {
		escapedPass := strings.ReplaceAll(password, `"`, `\"`)
		cmd := fmt.Sprintf(`/system backup save name="%s" password="%s"`, baseFilename, escapedPass)
		out, err := c.RunCommand(ctx, cmd)
		if err != nil {
			return fmt.Errorf("failed to create encrypted backup: %w (output: %s)", err, out)
		}
		c.logger.Debug("Encrypted binary backup generated")
		return nil
	}

	v7Cmd := fmt.Sprintf(`/system backup save name="%s" dont-encrypt=yes`, baseFilename)
	out, err := c.RunCommand(ctx, v7Cmd)
	if err == nil {
		c.logger.Debug("Binary backup generated with dont-encrypt=yes")
		return nil
	}

	c.logger.Debug("dont-encrypt flag failed, falling back to legacy /system backup save",
		slog.String("output", out),
		slog.Any("error", err),
	)

	legacyCmd := fmt.Sprintf(`/system backup save name="%s"`, baseFilename)
	outLegacy, errLegacy := c.RunCommand(ctx, legacyCmd)
	if errLegacy != nil {
		return fmt.Errorf("failed to create binary backup: %w (output: %s)", errLegacy, outLegacy)
	}

	c.logger.Debug("Legacy binary backup command completed")
	return nil
}

// WaitForFile polls until the requested file appears on the router and has finished writing.
func (c *Client) WaitForFile(ctx context.Context, baseFilename, extension string, maxWait time.Duration) (string, int64, error) {
	deadline := time.Now().Add(maxWait)
	candidatePaths := []string{
		fmt.Sprintf("%s.%s", baseFilename, extension),
		fmt.Sprintf("flash/%s.%s", baseFilename, extension),
		fmt.Sprintf("/%s.%s", baseFilename, extension),
		fmt.Sprintf("/flash/%s.%s", baseFilename, extension),
	}

	var lastSize int64 = -1
	var foundPath string

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", 0, fmt.Errorf("timed out waiting for file %s.%s: %w", baseFilename, extension, ctx.Err())
		case <-ticker.C:
			if time.Now().After(deadline) {
				return "", 0, fmt.Errorf("timed out waiting for file %s.%s after %v", baseFilename, extension, maxWait)
			}

			if foundPath == "" {
				for _, candidate := range candidatePaths {
					info, err := c.sftpClient.Stat(candidate)
					if err == nil && info != nil && info.Size() > 0 {
						foundPath = candidate
						lastSize = info.Size()
						break
					}
				}

				if foundPath == "" {
					searchCmd := fmt.Sprintf(`/file print detail where name~"%s"`, baseFilename)
					out, err := c.RunCommand(ctx, searchCmd)
					if err == nil && out != "" {
						matches := fileNameExtractRegex.FindAllStringSubmatch(out, -1)
						for _, match := range matches {
							if len(match) > 1 && strings.HasSuffix(match[1], "."+extension) {
								foundPath = match[1]
								break
							}
						}
					}
				}
			}

			if foundPath != "" {
				info, err := c.sftpClient.Stat(foundPath)
				if err == nil && info != nil && info.Size() > 0 {
					currentSize := info.Size()
					if currentSize == lastSize {
						return foundPath, currentSize, nil
					}
					lastSize = currentSize
				}
			}
		}
	}
}

// OpenRemoteFile opens a remote file for reading via SFTP.
func (c *Client) OpenRemoteFile(remotePath string) (*sftp.File, error) {
	return c.sftpClient.Open(remotePath)
}

// DownloadFile reads the remote file via SFTP and copies it into writer.
func (c *Client) DownloadFile(ctx context.Context, remotePath string, writer io.Writer) (int64, error) {
	c.logger.Debug("Downloading file via SFTP", slog.String("remotePath", remotePath))

	remoteFile, err := c.sftpClient.Open(remotePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open remote file %q on router: %w", remotePath, err)
	}
	defer remoteFile.Close()

	type copyResult struct {
		written int64
		err     error
	}

	done := make(chan copyResult, 1)
	go func() {
		n, cErr := io.Copy(writer, remoteFile)
		done <- copyResult{written: n, err: cErr}
	}()

	select {
	case <-ctx.Done():
		return 0, fmt.Errorf("download of %q cancelled: %w", remotePath, ctx.Err())
	case res := <-done:
		if res.err != nil {
			return res.written, fmt.Errorf("error during download of %q: %w", remotePath, res.err)
		}
		return res.written, nil
	}
}

// RemoveFile deletes the temporary file from the MikroTik router.
func (c *Client) RemoveFile(ctx context.Context, remotePath string) error {
	c.logger.Debug("Removing temporary file from router", slog.String("remotePath", remotePath))

	cleanName := strings.TrimPrefix(remotePath, "/")
	removeCmd := fmt.Sprintf(`/file remove [find name="%s"]`, cleanName)
	if _, err := c.RunCommand(ctx, removeCmd); err != nil {
		c.logger.Warn("RouterOS CLI file removal returned warning",
			slog.String("cmd", removeCmd),
			slog.Any("error", err),
		)
	}

	_ = c.sftpClient.Remove(remotePath)
	if cleanName != remotePath {
		_ = c.sftpClient.Remove(cleanName)
	}

	return nil
}

// Close closes the SFTP and SSH connections.
func (c *Client) Close() error {
	var errs []error
	if c.sftpClient != nil {
		if err := c.sftpClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("sftp close error: %w", err))
		}
	}
	if c.sshClient != nil {
		if err := c.sshClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("ssh close error: %w", err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
