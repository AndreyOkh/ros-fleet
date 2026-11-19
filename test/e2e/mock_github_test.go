package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/updater"
)

type MockGitHubServer struct {
	T               *testing.T
	Server          *httptest.Server
	mu              sync.Mutex
	LatestRelease   *updater.Release
	RawReleaseJSON  string
	StatusCode      int
	Assets          map[string][]byte
	RecordedHeaders []http.Header
	RecordedURLs    []string
}

func NewMockGitHubServer(t *testing.T) *MockGitHubServer {
	m := &MockGitHubServer{
		T:          t,
		StatusCode: http.StatusOK,
		Assets:     make(map[string][]byte),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", m.handleRequest)

	m.Server = httptest.NewServer(mux)
	t.Cleanup(func() {
		m.Server.Close()
	})
	return m
}

func (m *MockGitHubServer) URL() string {
	return m.Server.URL
}

func (m *MockGitHubServer) Close() {
	m.Server.Close()
}

func (m *MockGitHubServer) SetRelease(rel *updater.Release) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LatestRelease = rel
	m.RawReleaseJSON = ""
}

func (m *MockGitHubServer) SetRawJSON(raw string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RawReleaseJSON = raw
	m.LatestRelease = nil
}

func (m *MockGitHubServer) SetStatus(code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StatusCode = code
}

func (m *MockGitHubServer) AddAsset(name string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Assets[name] = data
}

// ConfigureReleaseWithAssets builds a complete mock release with archives and checksums.
func (m *MockGitHubServer) ConfigureReleaseWithAssets(tag string, assets map[string][]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var relAssets []updater.ReleaseAsset
	for name, data := range assets {
		m.Assets[name] = data
		relAssets = append(relAssets, updater.ReleaseAsset{
			Name:               name,
			BrowserDownloadURL: fmt.Sprintf("%s/assets/%s", m.Server.URL, name),
			Size:               int64(len(data)),
		})
	}

	m.LatestRelease = &updater.Release{
		TagName:     tag,
		Name:        "Release " + tag,
		Body:        "Automated release notes for " + tag,
		HTMLURL:     "https://github.com/AndreyOkh/ros-fleet/releases/tag/" + tag,
		PublishedAt: time.Now().UTC(),
		Assets:      relAssets,
	}
	m.RawReleaseJSON = ""
	m.StatusCode = http.StatusOK
}

func (m *MockGitHubServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RecordedHeaders = append(m.RecordedHeaders, r.Header.Clone())
	m.RecordedURLs = append(m.RecordedURLs, r.URL.String())

	if strings.HasPrefix(r.URL.Path, "/assets/") {
		assetName := strings.TrimPrefix(r.URL.Path, "/assets/")
		data, ok := m.Assets[assetName]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}

	// Releases endpoint
	if strings.Contains(r.URL.Path, "/releases/latest") {
		if m.StatusCode != http.StatusOK {
			http.Error(w, http.StatusText(m.StatusCode), m.StatusCode)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if m.RawReleaseJSON != "" {
			_, _ = w.Write([]byte(m.RawReleaseJSON))
			return
		}

		if m.LatestRelease != nil {
			data, _ := json.Marshal(m.LatestRelease)
			_, _ = w.Write(data)
			return
		}

		http.NotFound(w, r)
		return
	}

	http.NotFound(w, r)
}
