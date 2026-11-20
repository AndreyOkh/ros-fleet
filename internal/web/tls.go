package web

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
)

// GenerateSelfSignedCertificate generates a 10-year ECDSA (P-256) self-signed TLS certificate
// and private key, saving them to certPath (0644) and keyPath (0600).
func GenerateSelfSignedCertificate(certPath, keyPath string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate ECDSA private key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := notBefore.Add(10 * 365 * 24 * time.Hour)

	dnsNames := []string{"localhost"}
	if h, err := os.Hostname(); err == nil && h != "" && h != "localhost" {
		dnsNames = append(dnsNames, h)
	}

	ipSet := make(map[string]bool)
	var ipAddresses []net.IP

	addIP := func(ip net.IP) {
		if ip == nil {
			return
		}
		s := ip.String()
		if !ipSet[s] {
			ipSet[s] = true
			ipAddresses = append(ipAddresses, ip)
		}
	}

	addIP(net.ParseIP("127.0.0.1"))
	addIP(net.ParseIP("::1"))

	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 {
				continue
			}
			if addrs, err := iface.Addrs(); err == nil {
				for _, addr := range addrs {
					var ip net.IP
					switch v := addr.(type) {
					case *net.IPNet:
						ip = v.IP
					case *net.IPAddr:
						ip = v.IP
					}
					if ip != nil && !ip.IsLoopback() {
						addIP(ip)
					}
				}
			}
		}
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "ros-fleet",
			Organization: []string{"ros-fleet"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddresses,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return fmt.Errorf("failed to create directory for cert %q: %w", certPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
		return fmt.Errorf("failed to create directory for key %q: %w", keyPath, err)
	}

	certOut, err := os.OpenFile(certPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open %q for writing: %w", certPath, err)
	}
	defer certOut.Close()

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return fmt.Errorf("failed to write certificate to %q: %w", certPath, err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("failed to marshal ECDSA private key: %w", err)
	}

	keyOut, err := os.OpenFile(keyPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open %q for writing: %w", keyPath, err)
	}
	defer keyOut.Close()

	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		return fmt.Errorf("failed to write key to %q: %w", keyPath, err)
	}

	return nil
}

// EnsureCertificates checks if certificates are configured. If both paths are empty and SSL is enabled,
// it generates a self-signed ECDSA certificate in <config_dir>/certs, saves the paths back to config.json
// via cfgMgr, and logs the generation.
func EnsureCertificates(cfgMgr *config.Manager, logger *slog.Logger) (string, string, error) {
	cfg := cfgMgr.Get()
	if !cfg.IsSSLEnabled() {
		return "", "", nil
	}

	certFile := strings.TrimSpace(cfg.Web.SSLCertFile)
	keyFile := strings.TrimSpace(cfg.Web.SSLKeyFile)

	if certFile != "" && keyFile != "" {
		return certFile, keyFile, nil
	}

	configDir := filepath.Dir(cfgMgr.Path())
	certsDir := filepath.Join(configDir, "certs")
	certFile = filepath.Join(certsDir, "ros-fleet.crt")
	keyFile = filepath.Join(certsDir, "ros-fleet.key")

	if !fileExists(certFile) || !fileExists(keyFile) {
		if err := GenerateSelfSignedCertificate(certFile, keyFile); err != nil {
			return "", "", fmt.Errorf("failed to generate self-signed certificate: %w", err)
		}
		logger.Info("Generated self-signed TLS certificate",
			slog.String("cert_file", certFile),
			slog.String("key_file", keyFile),
		)
	} else {
		logger.Info("Reusing existing self-signed TLS certificate",
			slog.String("cert_file", certFile),
			slog.String("key_file", keyFile),
		)
	}

	if err := cfgMgr.UpdateWebCertFiles(certFile, keyFile); err != nil {
		return "", "", fmt.Errorf("failed to update certificate paths in configuration: %w", err)
	}

	return certFile, keyFile, nil
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Size() > 0
}

// NewTLSConfig loads certificate and private key, returning a secure tls.Config (TLS 1.2+ minimum).
func NewTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate and key: %w", err)
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}, nil
}

// bufferedConn wraps a net.Conn with a buffered reader to re-read peeked bytes.
type bufferedConn struct {
	net.Conn
	r io.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

// splitListener multiplexes incoming connections on the same port:
// - Byte 0x16 (TLS Handshake) -> passed to TLS handler
// - Plain HTTP request -> 308 Permanent Redirect to https://<Host><URI>
type splitListener struct {
	base        net.Listener
	connChan    chan net.Conn
	errChan     chan error
	closeOnce   sync.Once
	closed      chan struct{}
	defaultHost string
	logger      *slog.Logger
}

func newSplitListener(base net.Listener, defaultHost string, logger *slog.Logger) *splitListener {
	l := &splitListener{
		base:        base,
		connChan:    make(chan net.Conn),
		errChan:     make(chan error, 1),
		closed:      make(chan struct{}),
		defaultHost: defaultHost,
		logger:      logger,
	}
	go l.acceptLoop()
	return l
}

func (l *splitListener) acceptLoop() {
	for {
		conn, err := l.base.Accept()
		if err != nil {
			select {
			case l.errChan <- err:
			case <-l.closed:
			}
			return
		}
		go l.dispatch(conn)
	}
}

func (l *splitListener) dispatch(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	b, err := r.Peek(1)
	if err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	if b[0] == 0x16 {
		// TLS handshake
		bc := &bufferedConn{Conn: conn, r: r}
		select {
		case l.connChan <- bc:
		case <-l.closed:
			_ = conn.Close()
		}
		return
	}

	// Plain HTTP: redirect to HTTPS
	l.redirectHTTP(conn, r)
}

func (l *splitListener) redirectHTTP(conn net.Conn, r *bufio.Reader) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	req, err := http.ReadRequest(r)
	if err != nil {
		return
	}

	host := req.Host
	if host == "" {
		host = l.defaultHost
	}

	uri := req.RequestURI
	if uri == "" {
		uri = "/"
	}

	target := "https://" + host + uri
	body := fmt.Sprintf("<!DOCTYPE html><html><head><title>308 Permanent Redirect</title></head><body><h1>308 Permanent Redirect</h1><p>Location: <a href=\"%s\">%s</a></p></body></html>\n", target, target)
	resp := fmt.Sprintf("HTTP/1.1 308 Permanent Redirect\r\n"+
		"Location: %s\r\n"+
		"Connection: close\r\n"+
		"Content-Type: text/html; charset=utf-8\r\n"+
		"Content-Length: %d\r\n\r\n%s", target, len(body), body)

	_, _ = conn.Write([]byte(resp))
}

func (l *splitListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connChan:
		return conn, nil
	case err := <-l.errChan:
		return nil, err
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *splitListener) Close() error {
	var err error
	l.closeOnce.Do(func() {
		close(l.closed)
		err = l.base.Close()
	})
	return err
}

func (l *splitListener) Addr() net.Addr {
	return l.base.Addr()
}

// hstsMiddleware sets Strict-Transport-Security header on all responses.
func hstsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}
