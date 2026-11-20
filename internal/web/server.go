package web

import (
	"context"
	"crypto/tls"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AndreyOkh/ros-fleet/internal/config"
	"github.com/AndreyOkh/ros-fleet/internal/gitrepo"
	"github.com/AndreyOkh/ros-fleet/internal/runner"
	"github.com/AndreyOkh/ros-fleet/internal/scheduler"
	"github.com/AndreyOkh/ros-fleet/internal/state"
	"github.com/AndreyOkh/ros-fleet/internal/storage"
	"github.com/AndreyOkh/ros-fleet/internal/updater"
	"github.com/AndreyOkh/ros-fleet/internal/version"
	"github.com/AndreyOkh/ros-fleet/web"
)

// UpdateChecker provides access to update check results.
type UpdateChecker interface {
	GetStatus() *updater.CheckResult
}

// Server coordinates the web dashboard, API endpoints, and template rendering.
type Server struct {
	httpServer *http.Server
	cfgMgr     *config.Manager
	runner     *runner.Runner
	stateMgr   *state.StateManager
	scheduler  *scheduler.Scheduler
	checker    UpdateChecker
	logger     *slog.Logger
	templates  *template.Template
	appCtx     context.Context
	cancelApp  context.CancelFunc
}

// TableRowData combines configuration, effective resolved settings, and live state.
type TableRowData struct {
	Router    config.RouterConfig
	Effective config.EffectiveRouterConfig
	State     state.DeviceState
}

// TableData contains all rows for the main device table.
type TableData struct {
	Items     []TableRowData
	IsRunning bool
}

// SummaryData contains KPI card stats for the dashboard header.
type SummaryData struct {
	TotalCount    int
	ActiveCount   int
	SuccessCount  int
	FailedCount   int
	NextRun       string
	ScheduleTimes string
	Timezone      string
	GitStatus     gitrepo.GitStatus
}

// IndexData is passed to the main index.html template.
type IndexData struct {
	Summary            SummaryData
	Table              TableData
	GitStatus          gitrepo.GitStatus
	Version            version.Info
	UpdateStatus       *updater.CheckResult
	UpdateCheckEnabled bool
	MigrationNotice    *config.MigrationNotice
}

// ModalData is passed to the router add/edit modal template.
type ModalData struct {
	IsEdit                         bool
	Router                         config.RouterConfig
	Defaults                       config.DefaultsConfig
	HasPassword                    bool
	HasDefaultPassword             bool
	HasPrivateKeyPassphrase        bool
	HasDefaultPrivateKeyPassphrase bool
	HasBackupPassword              bool
	HasDefaultBackupPassword       bool
}

// DefaultsModalData is passed to the defaults modal template.
type DefaultsModalData struct {
	Defaults                config.DefaultsConfig
	HasPassword             bool
	HasPrivateKeyPassphrase bool
	HasBackupPassword       bool
}

// Zero-leak sanitizers to ensure sensitive credentials never reach templates or client responses.
func sanitizeRouterConfig(rc config.RouterConfig) config.RouterConfig {
	rc.Password = ""
	rc.PrivateKeyPassphrase = ""
	rc.BackupPassword = ""
	return rc
}

func sanitizeEffectiveConfig(eff config.EffectiveRouterConfig) config.EffectiveRouterConfig {
	eff.Password = ""
	eff.PrivateKeyPassphrase = ""
	eff.BackupPassword = ""
	return eff
}

func sanitizeDefaultsConfig(def config.DefaultsConfig) config.DefaultsConfig {
	def.Password = ""
	def.PrivateKeyPassphrase = ""
	def.BackupPassword = ""
	return def
}

// BackupsModalData is passed to the backups list modal template.
type BackupsModalData struct {
	Router   config.RouterConfig
	RSC      *storage.BackupFileInfo
	Binaries []storage.BackupFileInfo
	HasFiles bool
}

// NewServer initializes the HTTP server, parses templates, and registers routes.
func NewServer(cfgMgr *config.Manager, runner *runner.Runner, stateMgr *state.StateManager, sched *scheduler.Scheduler, checker UpdateChecker, logger *slog.Logger) (*Server, error) {
	log := logger.With(slog.String("component", "web_server"))

	tmplFuncs := template.FuncMap{
		"formatBytes": formatBytes,
		"derefBool": func(b *bool) bool {
			if b == nil {
				return false
			}
			return *b
		},
		"triState": func(b *bool) string {
			if b == nil {
				return "inherit"
			}
			if *b {
				return "true"
			}
			return "false"
		},
		"hasPrefix": strings.HasPrefix,
	}

	tmpl, err := template.New("").Funcs(tmplFuncs).ParseFS(web.Assets, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse embedded templates: %w", err)
	}

	appCtx, cancelApp := context.WithCancel(context.Background())
	s := &Server{
		cfgMgr:    cfgMgr,
		runner:    runner,
		stateMgr:  stateMgr,
		scheduler: sched,
		checker:   checker,
		logger:    log,
		templates: tmpl,
		appCtx:    appCtx,
		cancelApp: cancelApp,
	}

	mux := http.NewServeMux()

	// Static assets from embedded FS
	staticFS, err := fs.Sub(web.Assets, "static")
	if err != nil {
		return nil, fmt.Errorf("failed to load static sub-filesystem: %w", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Dashboard routes
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /dashboard/summary", s.handleSummaryCards)
	mux.HandleFunc("GET /dashboard/git-status", s.handleGitStatus)
	mux.HandleFunc("POST /dashboard/git-push", s.handleGitPush)
	mux.HandleFunc("GET /routers/table", s.handleTable)

	// Global settings routes
	mux.HandleFunc("GET /settings/defaults", s.handleSettingsDefaultsModal)
	mux.HandleFunc("POST /settings/defaults", s.handleSaveDefaults)

	// Router management routes
	mux.HandleFunc("GET /routers/new", s.handleNewRouterModal)
	mux.HandleFunc("GET /routers/{name}/edit", s.handleEditRouterModal)
	mux.HandleFunc("POST /routers", s.handleSaveRouter)
	mux.HandleFunc("POST /routers/{name}/toggle", s.handleToggleRouter)
	mux.HandleFunc("POST /routers/{name}/backup", s.handleBackupSingle)
	mux.HandleFunc("POST /routers/{name}/refresh", s.handleRefreshSingle)
	mux.HandleFunc("DELETE /routers/{name}", s.handleDeleteRouter)

	// Router backup files inspection and download routes
	mux.HandleFunc("GET /routers/{name}/backups", s.handleRouterBackupsModal)
	mux.HandleFunc("GET /routers/{name}/backups/download", s.handleDownloadBackup)
	mux.HandleFunc("GET /routers/{name}/backups/latest", s.handleDownloadLatestBackup)

	// Bulk backup route
	mux.HandleFunc("POST /backup/all", s.handleBackupAll)

	// Update and notice routes
	mux.HandleFunc("GET /update/badge", s.handleUpdateBadge)
	mux.HandleFunc("GET /update/modal", s.handleUpdateModal)
	mux.HandleFunc("POST /notices/dismiss", s.handleDismissNotice)

	cfg := cfgMgr.Get()
	var handler http.Handler = mux

	if cfg.Web.AuthUser != "" && cfg.Web.AuthPassword != "" {
		handler = basicAuthMiddleware(cfg.Web.AuthUser, cfg.Web.AuthPassword, mux)
		log.Info("Basic Auth enabled for web dashboard", slog.String("user", cfg.Web.AuthUser))
	}

	if cfg.IsSSLEnabled() {
		handler = hstsMiddleware(handler)
	}

	s.httpServer = &http.Server{
		Addr:    cfg.Web.ListenAddr,
		Handler: handler,
	}

	return s, nil
}

// Start launches the HTTP server in a background goroutine.
func (s *Server) Start() error {
	cfg := s.cfgMgr.Get()
	addr := s.httpServer.Addr
	if addr == "" {
		addr = ":8080"
	}

	baseLn, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	scheme := "http"
	var ln net.Listener = baseLn

	if cfg.IsSSLEnabled() {
		scheme = "https"
		certFile, keyFile, err := EnsureCertificates(s.cfgMgr, s.logger)
		if err != nil {
			_ = baseLn.Close()
			return fmt.Errorf("failed to ensure SSL certificates: %w", err)
		}

		tlsConfig, err := NewTLSConfig(certFile, keyFile)
		if err != nil {
			_ = baseLn.Close()
			return fmt.Errorf("failed to configure TLS: %w", err)
		}

		defaultHost := addr
		if strings.HasPrefix(defaultHost, ":") {
			defaultHost = "localhost" + defaultHost
		}
		splitL := newSplitListener(baseLn, defaultHost, s.logger)
		ln = tls.NewListener(splitL, tlsConfig)
	}

	displayHost := addr
	if strings.HasPrefix(displayHost, ":") {
		displayHost = "localhost" + displayHost
	} else if strings.HasPrefix(displayHost, "0.0.0.0:") {
		displayHost = "localhost:" + strings.TrimPrefix(displayHost, "0.0.0.0:")
	}

	s.logger.Info("Starting web dashboard",
		slog.String("listen_addr", addr),
		slog.String("url", fmt.Sprintf("%s://%s", scheme, displayHost)),
		slog.Bool("ssl_enabled", cfg.IsSSLEnabled()),
	)

	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Web server Serve error", slog.Any("error", err))
		}
	}()
	return nil
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down web server...")
	if s.cancelApp != nil {
		s.cancelApp()
	}
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	var updateStatus *updater.CheckResult
	if s.checker != nil {
		updateStatus = s.checker.GetStatus()
	}

	var migrationNotice *config.MigrationNotice
	if s.cfgMgr != nil {
		migrationNotice = s.cfgMgr.GetMigrationNotice()
	}

	data := IndexData{
		Summary:            s.buildSummaryData(),
		Table:              s.buildTableData(),
		GitStatus:          s.runner.GitStatus(),
		Version:            version.Get(),
		UpdateStatus:       updateStatus,
		UpdateCheckEnabled: s.checker != nil,
		MigrationNotice:    migrationNotice,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		s.logger.Error("Template render error (index.html)", slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUpdateBadge(w http.ResponseWriter, r *http.Request) {
	if s.checker == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	updateStatus := s.checker.GetStatus()
	if updateStatus == nil || !updateStatus.UpdateAvailable {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "update_badge.html", updateStatus); err != nil {
		s.logger.Error("Template render error (update_badge.html)", slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleUpdateModal(w http.ResponseWriter, r *http.Request) {
	if s.checker == nil {
		http.Error(w, "Update checker is disabled", http.StatusNotFound)
		return
	}

	updateStatus := s.checker.GetStatus()

	type updateModalData struct {
		CurrentVersion string
		LatestVersion  string
		ReleaseNotes   string
		PublishedAt    string
		HTMLURL        string
		IsDocker       bool
	}

	v := version.Get()
	currVer := v.Version
	latestVer := "latest"
	notes := ""
	pubDate := ""
	htmlURL := ""

	if updateStatus != nil {
		if updateStatus.CurrentVersion != "" {
			currVer = updateStatus.CurrentVersion
		}
		if updateStatus.LatestVersion != "" {
			latestVer = updateStatus.LatestVersion
		}
		notes = updateStatus.ReleaseNotes
		if !updateStatus.PublishedAt.IsZero() {
			pubDate = updateStatus.PublishedAt.Format("2006-01-02 15:04:05 UTC")
		}
		htmlURL = updateStatus.HTMLURL
	}

	data := updateModalData{
		CurrentVersion: currVer,
		LatestVersion:  latestVer,
		ReleaseNotes:   notes,
		PublishedAt:    pubDate,
		HTMLURL:        htmlURL,
		IsDocker:       updater.IsRunningInDocker(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "update_modal.html", data); err != nil {
		s.logger.Error("Template render error (update_modal.html)", slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleDismissNotice(w http.ResponseWriter, r *http.Request) {
	if s.cfgMgr != nil {
		s.cfgMgr.ClearMigrationNotice()
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	status := s.runner.GitStatus()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "git_status.html", status); err != nil {
		s.logger.Error("Template render error (git_status.html)", slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleGitPush(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := s.runner.GitPush(ctx); err != nil {
		s.logger.Error("Manual git push failed", slog.Any("error", err))
	}

	status := s.runner.GitStatus()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "git_status.html", status); err != nil {
		s.logger.Error("Template render error (git_status.html)", slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleSummaryCards(w http.ResponseWriter, r *http.Request) {
	summary := s.buildSummaryData()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.templates.ExecuteTemplate(w, "summary_cards.html", summary)
}

func (s *Server) handleTable(w http.ResponseWriter, r *http.Request) {
	table := s.buildTableData()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.templates.ExecuteTemplate(w, "table_container.html", table)
}

func (s *Server) handleSettingsDefaultsModal(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	hasPassword := strings.TrimSpace(cfg.Defaults.Password) != ""
	hasPassphrase := strings.TrimSpace(cfg.Defaults.PrivateKeyPassphrase) != ""
	hasBackupPwd := strings.TrimSpace(cfg.Defaults.BackupPassword) != ""

	data := DefaultsModalData{
		Defaults:                sanitizeDefaultsConfig(cfg.Defaults),
		HasPassword:             hasPassword,
		HasPrivateKeyPassphrase: hasPassphrase,
		HasBackupPassword:       hasBackupPwd,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.templates.ExecuteTemplate(w, "defaults_modal.html", data)
}

func (s *Server) handleSaveDefaults(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	port, _ := strconv.Atoi(r.FormValue("port"))
	if port <= 0 {
		port = 22
	}

	currentDefaults := s.cfgMgr.Get().Defaults

	password := strings.TrimSpace(r.FormValue("password"))
	if password == "-" {
		password = ""
	} else if password == "" {
		password = currentDefaults.Password
	}

	passphrase := strings.TrimSpace(r.FormValue("private_key_passphrase"))
	if passphrase == "-" {
		passphrase = ""
	} else if passphrase == "" {
		passphrase = currentDefaults.PrivateKeyPassphrase
	}

	encryptBackup := r.FormValue("encrypt_backup") == "true"
	backupPassword := strings.TrimSpace(r.FormValue("backup_password"))
	if !encryptBackup {
		backupPassword = ""
	} else if backupPassword == "-" {
		backupPassword = ""
	} else if backupPassword == "" {
		backupPassword = currentDefaults.BackupPassword
	}

	def := config.DefaultsConfig{
		Port:                  port,
		User:                  strings.TrimSpace(r.FormValue("user")),
		Password:              password,
		PrivateKeyPath:        strings.TrimSpace(r.FormValue("private_key_path")),
		PrivateKeyPassphrase:  passphrase,
		InsecureIgnoreHostKey: r.FormValue("insecure_ignore_host_key") == "true",
		ExportSensitive:       r.FormValue("export_sensitive") == "true",
		EncryptBackup:         encryptBackup,
		BackupPassword:        backupPassword,
	}

	if err := s.cfgMgr.UpdateDefaults(def); err != nil {
		s.logger.Error("Failed to update defaults", slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.logger.Info("Global defaults updated via Web UI")
	s.handleTable(w, r)
}

func (s *Server) handleNewRouterModal(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	hasDefPassword := strings.TrimSpace(cfg.Defaults.Password) != ""
	hasDefPassphrase := strings.TrimSpace(cfg.Defaults.PrivateKeyPassphrase) != ""
	hasDefBackupPwd := strings.TrimSpace(cfg.Defaults.BackupPassword) != ""

	data := ModalData{
		IsEdit:                         false,
		Router:                         config.RouterConfig{},
		Defaults:                       sanitizeDefaultsConfig(cfg.Defaults),
		HasPassword:                    false,
		HasDefaultPassword:             hasDefPassword,
		HasPrivateKeyPassphrase:        false,
		HasDefaultPrivateKeyPassphrase: hasDefPassphrase,
		HasBackupPassword:              false,
		HasDefaultBackupPassword:       hasDefBackupPwd,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.templates.ExecuteTemplate(w, "router_modal.html", data)
}

func (s *Server) handleEditRouterModal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	rc, found := s.cfgMgr.GetRouter(name)
	if !found {
		http.Error(w, "Router not found", http.StatusNotFound)
		return
	}

	cfg := s.cfgMgr.Get()
	hasPassword := strings.TrimSpace(rc.Password) != ""
	hasPassphrase := strings.TrimSpace(rc.PrivateKeyPassphrase) != ""
	hasBackupPwd := strings.TrimSpace(rc.BackupPassword) != ""

	hasDefPassword := strings.TrimSpace(cfg.Defaults.Password) != ""
	hasDefPassphrase := strings.TrimSpace(cfg.Defaults.PrivateKeyPassphrase) != ""
	hasDefBackupPwd := strings.TrimSpace(cfg.Defaults.BackupPassword) != ""

	data := ModalData{
		IsEdit:                         true,
		Router:                         sanitizeRouterConfig(rc),
		Defaults:                       sanitizeDefaultsConfig(cfg.Defaults),
		HasPassword:                    hasPassword,
		HasDefaultPassword:             hasDefPassword,
		HasPrivateKeyPassphrase:        hasPassphrase,
		HasDefaultPrivateKeyPassphrase: hasDefPassphrase,
		HasBackupPassword:              hasBackupPwd,
		HasDefaultBackupPassword:       hasDefBackupPwd,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.templates.ExecuteTemplate(w, "router_modal.html", data)
}

func (s *Server) handleSaveRouter(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	host := strings.TrimSpace(r.FormValue("host"))

	enabled := true
	existing, isExisting := s.cfgMgr.GetRouter(name)
	if isExisting {
		enabled = existing.IsEnabled()
	}

	parseTriState := func(val string) *bool {
		switch val {
		case "true":
			b := true
			return &b
		case "false":
			b := false
			return &b
		default: // "inherit" or empty
			return nil
		}
	}

	port, _ := strconv.Atoi(r.FormValue("port"))

	password := strings.TrimSpace(r.FormValue("password"))
	if password == "-" {
		password = ""
	} else if password == "" && isExisting {
		password = existing.Password
	}

	encryptBackup := parseTriState(r.FormValue("encrypt_backup"))
	backupPassword := strings.TrimSpace(r.FormValue("backup_password"))
	// Only persist a device-specific backup password if encryption is explicitly enabled for this router.
	if encryptBackup == nil || !*encryptBackup {
		backupPassword = ""
	} else if backupPassword == "-" {
		backupPassword = ""
	} else if backupPassword == "" && isExisting {
		backupPassword = existing.BackupPassword
	}

	passphrase := strings.TrimSpace(r.FormValue("private_key_passphrase"))
	if passphrase == "-" {
		passphrase = ""
	} else if passphrase == "" && isExisting {
		if strings.TrimSpace(r.FormValue("private_key_path")) == "" && existing.PrivateKeyPath != "" {
			passphrase = ""
		} else {
			passphrase = existing.PrivateKeyPassphrase
		}
	}

	router := config.RouterConfig{
		Enabled:               &enabled,
		Name:                  name,
		Host:                  host,
		Port:                  port,
		User:                  strings.TrimSpace(r.FormValue("user")),
		Password:              password,
		PrivateKeyPath:        strings.TrimSpace(r.FormValue("private_key_path")),
		PrivateKeyPassphrase:  passphrase,
		InsecureIgnoreHostKey: parseTriState(r.FormValue("insecure_ignore_host_key")),
		ExportSensitive:       parseTriState(r.FormValue("export_sensitive")),
		EncryptBackup:         encryptBackup,
		BackupPassword:        backupPassword,
	}

	if err := s.cfgMgr.UpsertRouter(router); err != nil {
		s.logger.Error("Failed to upsert router", slog.Any("error", err))
		http.Error(w, fmt.Sprintf("Error saving router: %v", err), http.StatusBadRequest)
		return
	}

	s.handleTable(w, r)
}

func (s *Server) handleToggleRouter(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	_, err := s.cfgMgr.ToggleRouter(name)
	if err != nil {
		s.logger.Error("Failed to toggle router", slog.String("router", name), slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.renderSingleRow(w, name)
}

func (s *Server) handleBackupSingle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.logger.Info("Manual backup triggered via Web UI", slog.String("router", name))

	cfg := s.cfgMgr.Get()
	timeout := time.Duration(cfg.TimeoutSec+10) * time.Second
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
	defer cancel()

	_, _ = s.runner.RunSingle(ctx, name)
	s.renderSingleRow(w, name)
}

func (s *Server) handleRefreshSingle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.logger.Info("Device telemetry refresh triggered via Web UI", slog.String("router", name))

	_, _ = s.runner.RefreshDevice(r.Context(), name)
	s.renderSingleRow(w, name)
}

func (s *Server) handleDeleteRouter(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.cfgMgr.DeleteRouter(name); err != nil {
		s.logger.Error("Failed to delete router", slog.String("router", name), slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.handleTable(w, r)
}

func (s *Server) handleBackupAll(w http.ResponseWriter, r *http.Request) {
	s.logger.Info("Backup all triggered via Web UI")
	if s.runner.IsRunning() {
		s.logger.Warn("Backup all triggered while already running")
		s.handleTable(w, r)
		return
	}

	go func() {
		if _, err := s.runner.Run(s.appCtx); err != nil {
			s.logger.Error("Background fleet backup failed", slog.Any("error", err))
		}
	}()

	s.handleTable(w, r)
}

func (s *Server) renderSingleRow(w http.ResponseWriter, name string) {
	rc, found := s.cfgMgr.GetRouter(name)
	if !found {
		http.Error(w, "Router not found", http.StatusNotFound)
		return
	}

	cfg := s.cfgMgr.Get()
	st, _ := s.stateMgr.Get(name)
	row := TableRowData{
		Router:    sanitizeRouterConfig(rc),
		Effective: sanitizeEffectiveConfig(rc.Effective(cfg.Defaults)),
		State:     st,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "table_row.html", row); err != nil {
		s.logger.Error("Template render error (table_row.html)", slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) buildTableData() TableData {
	cfg := s.cfgMgr.Get()
	routers := s.cfgMgr.ListRouters()
	states := s.stateMgr.GetAll()

	items := make([]TableRowData, 0, len(routers))
	for _, r := range routers {
		st, exists := states[r.Name]
		if !exists {
			st = state.DeviceState{
				Name:     r.Name,
				Host:     r.Host,
				Port:     r.Port,
				Model:    "Unknown",
				Firmware: "Unknown",
				Status:   state.StatusNeverRun,
			}
		}
		items = append(items, TableRowData{
			Router:    sanitizeRouterConfig(r),
			Effective: sanitizeEffectiveConfig(r.Effective(cfg.Defaults)),
			State:     st,
		})
	}

	isRunning := false
	if s.runner != nil {
		isRunning = s.runner.IsRunning()
	}

	return TableData{
		Items:     items,
		IsRunning: isRunning,
	}
}

func (s *Server) buildSummaryData() SummaryData {
	cfg := s.cfgMgr.Get()
	states := s.stateMgr.GetAll()

	activeCount := 0
	for _, r := range cfg.Routers {
		if r.IsEnabled() {
			activeCount++
		}
	}

	successCount := 0
	failedCount := 0
	for _, st := range states {
		if st.Status == state.StatusSuccess {
			successCount++
		} else if st.Status == state.StatusError {
			failedCount++
		}
	}

	nextRunStr := "Расписание не задано"
	if s.scheduler != nil {
		nextRunStr = s.scheduler.NextRunFormatted()
	}

	timesStr := strings.Join(cfg.Schedule.Times, ", ")
	if timesStr == "" {
		timesStr = "не задано"
	}

	var gitStatus gitrepo.GitStatus
	if s.runner != nil {
		gitStatus = s.runner.GitStatus()
	}

	return SummaryData{
		TotalCount:    len(cfg.Routers),
		ActiveCount:   activeCount,
		SuccessCount:  successCount,
		FailedCount:   failedCount,
		NextRun:       nextRunStr,
		ScheduleTimes: timesStr,
		Timezone:      cfg.Schedule.Timezone,
		GitStatus:     gitStatus,
	}
}

func basicAuthMiddleware(user, pass string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="ros-fleet"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func (s *Server) getStorage() *storage.Storage {
	cfg := s.cfgMgr.Get()
	return storage.New(cfg.BinaryBackupDir, cfg.ExportDir)
}

func (s *Server) handleRouterBackupsModal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	rc, found := s.cfgMgr.GetRouter(name)
	if !found {
		http.Error(w, "Router not found", http.StatusNotFound)
		return
	}

	stor := s.getStorage()
	backups, err := stor.ListRouterBackups(name)
	if err != nil {
		s.logger.Error("Failed to list router backups", slog.String("router", name), slog.Any("error", err))
		http.Error(w, "Failed to read backups", http.StatusInternalServerError)
		return
	}

	var rsc *storage.BackupFileInfo
	var binaries []storage.BackupFileInfo

	for _, b := range backups {
		if b.Type == "rsc" && rsc == nil {
			rscCopy := b
			rsc = &rscCopy
		} else if b.Type == "backup" {
			binaries = append(binaries, b)
		}
	}

	data := BackupsModalData{
		Router:   sanitizeRouterConfig(rc),
		RSC:      rsc,
		Binaries: binaries,
		HasFiles: len(backups) > 0,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "backups_modal.html", data); err != nil {
		s.logger.Error("Template render error (backups_modal.html)", slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, found := s.cfgMgr.GetRouter(name); !found {
		http.Error(w, "Router not found", http.StatusNotFound)
		return
	}

	kind := r.URL.Query().Get("kind")
	relPath := r.URL.Query().Get("file")
	if relPath == "" {
		http.Error(w, "Missing file parameter", http.StatusBadRequest)
		return
	}

	stor := s.getStorage()
	fullPath, downloadName, err := stor.ResolveBackupFile(kind, name, relPath)
	if err != nil {
		s.logger.Warn("Backup file resolution rejected",
			slog.String("router", name),
			slog.String("kind", kind),
			slog.String("file", relPath),
			slog.Any("error", err),
		)
		http.Error(w, fmt.Sprintf("Forbidden or not found: %v", err), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", downloadName))
	if strings.HasSuffix(downloadName, ".rsc") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	http.ServeFile(w, r, fullPath)
}

func (s *Server) handleDownloadLatestBackup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, found := s.cfgMgr.GetRouter(name); !found {
		http.Error(w, "Router not found", http.StatusNotFound)
		return
	}

	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "binary"
	}

	stor := s.getStorage()
	latest, err := stor.LatestRouterBackup(name, kind)
	if err != nil {
		http.Error(w, fmt.Sprintf("No %s backup found: %v", kind, err), http.StatusNotFound)
		return
	}

	fullPath, downloadName, err := stor.ResolveBackupFile(kind, name, latest.RelPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to resolve backup: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", downloadName))
	if strings.HasSuffix(downloadName, ".rsc") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	http.ServeFile(w, r, fullPath)
}
