package web

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
)

func TestGenerateSelfSignedCertificate(t *testing.T) {
	tempDir := t.TempDir()
	certPath := filepath.Join(tempDir, "certs", "test.crt")
	keyPath := filepath.Join(tempDir, "certs", "test.key")

	if err := GenerateSelfSignedCertificate(certPath, keyPath); err != nil {
		t.Fatalf("GenerateSelfSignedCertificate failed: %v", err)
	}

	// 1. Verify file permissions
	certFi, err := os.Stat(certPath)
	if err != nil {
		t.Fatalf("cert file not found: %v", err)
	}
	if certFi.Mode().Perm() != 0o644 {
		t.Errorf("expected cert permissions 0644, got %o", certFi.Mode().Perm())
	}

	keyFi, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("key file not found: %v", err)
	}
	if keyFi.Mode().Perm() != 0o600 {
		t.Errorf("expected key permissions 0600, got %o", keyFi.Mode().Perm())
	}

	// 2. Parse and verify certificate content
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse x509 certificate: %v", err)
	}

	if cert.Subject.CommonName != "ros-fleet" {
		t.Errorf("expected CommonName 'ros-fleet', got %q", cert.Subject.CommonName)
	}

	// Check validity duration ~10 years
	validityDays := cert.NotAfter.Sub(cert.NotBefore).Hours() / 24
	if validityDays < 3600 {
		t.Errorf("expected validity of approx 10 years (3650 days), got %.1f days", validityDays)
	}

	// Check algorithm
	if cert.PublicKeyAlgorithm != x509.ECDSA {
		t.Errorf("expected ECDSA public key algorithm, got %v", cert.PublicKeyAlgorithm)
	}

	// Check SAN
	hasLocalhost := false
	for _, name := range cert.DNSNames {
		if name == "localhost" {
			hasLocalhost = true
			break
		}
	}
	if !hasLocalhost {
		t.Errorf("expected SAN to include 'localhost', got %v", cert.DNSNames)
	}

	hasIPv4Loopback := false
	hasIPv6Loopback := false
	for _, ip := range cert.IPAddresses {
		if ip.Equal(net.ParseIP("127.0.0.1")) {
			hasIPv4Loopback = true
		}
		if ip.Equal(net.ParseIP("::1")) {
			hasIPv6Loopback = true
		}
	}
	if !hasIPv4Loopback || !hasIPv6Loopback {
		t.Errorf("expected SAN to include 127.0.0.1 and ::1, got %v", cert.IPAddresses)
	}

	// 3. Verify key pair can be loaded by crypto/tls
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Fatalf("tls.LoadX509KeyPair failed on generated pair: %v", err)
	}
}

func TestEnsureCertificates(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")

	cfgJSON := `{
  "config_version": 3,
  "binary_backup_dir": "./bin",
  "export_dir": "./exp",
  "state_file": "./state.json",
  "defaults": {"user": "admin", "password": "123"},
  "web": {
    "enabled": true,
    "ssl_enabled": true
  },
  "routers": [{"name": "r1", "host": "10.0.0.1"}]
}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr, err := config.NewManager(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.Default()

	// 1. EnsureCertificates when certs are empty -> generates and saves paths
	certPath, keyPath, err := EnsureCertificates(mgr, logger)
	if err != nil {
		t.Fatalf("EnsureCertificates failed: %v", err)
	}

	expectedCert := filepath.Join(tempDir, "certs", "ros-fleet.crt")
	expectedKey := filepath.Join(tempDir, "certs", "ros-fleet.key")

	if certPath != expectedCert || keyPath != expectedKey {
		t.Errorf("unexpected cert paths: cert=%q, key=%q", certPath, keyPath)
	}

	if !fileExists(certPath) || !fileExists(keyPath) {
		t.Fatalf("certificate files were not created on disk")
	}

	// Check in-memory update
	updatedCfg := mgr.Get()
	if updatedCfg.Web.SSLCertFile != expectedCert || updatedCfg.Web.SSLKeyFile != expectedKey {
		t.Errorf("cfgMgr in-memory paths not updated")
	}

	// Check disk update
	diskData, _ := os.ReadFile(cfgPath)
	diskStr := string(diskData)
	if !strings.Contains(diskStr, expectedCert) || !strings.Contains(diskStr, expectedKey) {
		t.Errorf("disk config does not contain updated certificate paths:\n%s", diskStr)
	}

	// 2. Calling EnsureCertificates again should reuse existing files
	certPath2, keyPath2, err := EnsureCertificates(mgr, logger)
	if err != nil {
		t.Fatalf("second EnsureCertificates failed: %v", err)
	}
	if certPath2 != expectedCert || keyPath2 != expectedKey {
		t.Errorf("expected reused paths")
	}
}

func TestSplitListenerHTTPRedirectAndHTTPS(t *testing.T) {
	tempDir := t.TempDir()
	certPath := filepath.Join(tempDir, "test.crt")
	keyPath := filepath.Join(tempDir, "test.key")

	if err := GenerateSelfSignedCertificate(certPath, keyPath); err != nil {
		t.Fatal(err)
	}

	tlsConfig, err := NewTLSConfig(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}

	// Listen on ephemeral port on 127.0.0.1
	baseLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := baseLn.Addr().String()
	splitL := newSplitListener(baseLn, addr, slog.Default())
	tlsL := tls.NewListener(splitL, tlsConfig)

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Hello Secure World"))
	})

	handler := hstsMiddleware(mux)

	server := &http.Server{
		Handler: handler,
	}

	go func() {
		_ = server.Serve(tlsL)
	}()
	defer server.Close()

	// Wait briefly for server to spin up
	time.Sleep(50 * time.Millisecond)

	// 1. Test Plain HTTP Request -> Should receive 308 Permanent Redirect to https://...
	httpClientNoRedirect := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // do not follow redirect
		},
		Timeout: 3 * time.Second,
	}

	httpURL := fmt.Sprintf("http://%s/hello?test=1", addr)
	respHTTP, err := httpClientNoRedirect.Get(httpURL)
	if err != nil {
		t.Fatalf("plain HTTP GET failed: %v", err)
	}
	defer respHTTP.Body.Close()

	if respHTTP.StatusCode != http.StatusPermanentRedirect {
		t.Errorf("expected status 308 Permanent Redirect for plain HTTP, got %d", respHTTP.StatusCode)
	}

	expectedLocation := fmt.Sprintf("https://%s/hello?test=1", addr)
	gotLocation := respHTTP.Header.Get("Location")
	if gotLocation != expectedLocation {
		t.Errorf("expected Location %q, got %q", expectedLocation, gotLocation)
	}

	// 2. Test HTTPS Request to the same port -> Should receive 200 OK + HSTS header
	httpsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // test self-signed cert
			},
		},
		Timeout: 3 * time.Second,
	}

	httpsURL := fmt.Sprintf("https://%s/hello", addr)
	respHTTPS, err := httpsClient.Get(httpsURL)
	if err != nil {
		t.Fatalf("HTTPS GET failed: %v", err)
	}
	defer respHTTPS.Body.Close()

	if respHTTPS.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 OK for HTTPS, got %d", respHTTPS.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(respHTTPS.Body)
	if string(bodyBytes) != "Hello Secure World" {
		t.Errorf("expected body 'Hello Secure World', got %q", string(bodyBytes))
	}

	hstsHeader := respHTTPS.Header.Get("Strict-Transport-Security")
	if !strings.Contains(hstsHeader, "max-age=31536000") || !strings.Contains(hstsHeader, "includeSubDomains") {
		t.Errorf("missing or invalid Strict-Transport-Security header: %q", hstsHeader)
	}
}
