package web

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/AndreyOkh/ros-fleet/internal/config"
	"github.com/AndreyOkh/ros-fleet/internal/gitrepo"
	"github.com/AndreyOkh/ros-fleet/internal/runner"
	"github.com/AndreyOkh/ros-fleet/internal/scheduler"
	"github.com/AndreyOkh/ros-fleet/internal/state"
	"github.com/AndreyOkh/ros-fleet/web"
)

// Server coordinates the web dashboard, API endpoints, and template rendering.
type Server struct {
	httpServer *http.Server
	cfgMgr     *config.Manager
	runner     *runner.Runner
	stateMgr   *state.StateManager
	scheduler  *scheduler.Scheduler
	logger     *slog.Logger
	templates  *template.Template
}

// TableRowData combines configuration, effective resolved settings, and live state.
type TableRowData struct {
	Router    config.RouterConfig
	Effective config.EffectiveRouterConfig
	State     state.DeviceState
}

// TableData contains all rows for the main device table.
type TableData struct {
	Items []TableRowData
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
}

// IndexData is passed to the main index.html template.
type IndexData struct {
	Summary   SummaryData
	Table     TableData
	GitStatus gitrepo.GitStatus
}

// ModalData is passed to the router add/edit modal template.
type ModalData struct {
	IsEdit                         bool
	Router                         config.RouterConfig
	Defaults                       config.DefaultsConfig
	UsesDefaults                   bool
	InsecureIgnoreHostKeyEffective bool
	ExportSensitiveEffective       bool
	EncryptBackupEffective         bool
}

// DefaultsModalData is passed to the defaults modal template.
type DefaultsModalData struct {
	Defaults config.DefaultsConfig
}

// NewServer initializes the HTTP server, parses templates, and registers routes.
func NewServer(cfgMgr *config.Manager, runner *runner.Runner, stateMgr *state.StateManager, sched *scheduler.Scheduler, logger *slog.Logger) (*Server, error) {
	log := logger.With(slog.String("component", "web_server"))

	tmplFuncs := template.FuncMap{
		"formatBytes": formatBytes,
		"derefBool": func(b *bool) bool {
			if b == nil {
				return false
			}
			return *b
		},
	}

	tmpl, err := template.New("").Funcs(tmplFuncs).ParseFS(web.Assets, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse embedded templates: %w", err)
	}

	s := &Server{
		cfgMgr:    cfgMgr,
		runner:    runner,
		stateMgr:  stateMgr,
		scheduler: sched,
		logger:    log,
		templates: tmpl,
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

	// Bulk backup route
	mux.HandleFunc("POST /backup/all", s.handleBackupAll)

	cfg := cfgMgr.Get()
	var handler http.Handler = mux

	if cfg.Web.AuthUser != "" && cfg.Web.AuthPassword != "" {
		handler = basicAuthMiddleware(cfg.Web.AuthUser, cfg.Web.AuthPassword, mux)
		log.Info("Basic Auth enabled for web dashboard", slog.String("user", cfg.Web.AuthUser))
	}

	s.httpServer = &http.Server{
		Addr:    cfg.Web.ListenAddr,
		Handler: handler,
	}

	return s, nil
}

// Start launches the HTTP server in a background goroutine.
func (s *Server) Start() error {
	s.logger.Info("Starting web dashboard", slog.String("listen_addr", s.httpServer.Addr))
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Web server ListenAndServe error", slog.Any("error", err))
		}
	}()
	return nil
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down web server...")
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := IndexData{
		Summary:   s.buildSummaryData(),
		Table:     s.buildTableData(),
		GitStatus: s.runner.GitStatus(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		s.logger.Error("Template render error (index.html)", slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleGitStatus(w http.ResponseWriter, r *http.Request) {
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
	data := DefaultsModalData{
		Defaults: cfg.Defaults,
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

	def := config.DefaultsConfig{
		Port:                  port,
		User:                  strings.TrimSpace(r.FormValue("user")),
		Password:              strings.TrimSpace(r.FormValue("password")),
		PrivateKeyPath:        strings.TrimSpace(r.FormValue("private_key_path")),
		PrivateKeyPassphrase:  strings.TrimSpace(r.FormValue("private_key_passphrase")),
		InsecureIgnoreHostKey: r.FormValue("insecure_ignore_host_key") == "true",
		ExportSensitive:       r.FormValue("export_sensitive") == "true",
		EncryptBackup:         r.FormValue("encrypt_backup") == "true",
		BackupPassword:        strings.TrimSpace(r.FormValue("backup_password")),
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
	data := ModalData{
		IsEdit:                         false,
		Router:                         config.RouterConfig{},
		Defaults:                       cfg.Defaults,
		UsesDefaults:                   true,
		InsecureIgnoreHostKeyEffective: cfg.Defaults.InsecureIgnoreHostKey,
		ExportSensitiveEffective:       cfg.Defaults.ExportSensitive,
		EncryptBackupEffective:         cfg.Defaults.EncryptBackup,
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
	eff := rc.Effective(cfg.Defaults)
	data := ModalData{
		IsEdit:                         true,
		Router:                         rc,
		Defaults:                       cfg.Defaults,
		UsesDefaults:                   rc.UsesDefaults(),
		InsecureIgnoreHostKeyEffective: eff.InsecureIgnoreHostKey,
		ExportSensitiveEffective:       eff.ExportSensitive,
		EncryptBackupEffective:         eff.EncryptBackup,
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
	useDefaults := r.FormValue("use_defaults") == "on" || r.FormValue("use_defaults") == "true"

	enabled := true
	if existing, found := s.cfgMgr.GetRouter(name); found {
		enabled = existing.IsEnabled()
	}

	var router config.RouterConfig
	if useDefaults {
		router = config.RouterConfig{
			Enabled: &enabled,
			Name:    name,
			Host:    host,
		}
	} else {
		port, _ := strconv.Atoi(r.FormValue("port"))
		insecure := r.FormValue("insecure_ignore_host_key") == "on"
		sensitive := r.FormValue("export_sensitive") == "on"
		encrypt := r.FormValue("encrypt_backup") == "on"

		router = config.RouterConfig{
			Enabled:               &enabled,
			Name:                  name,
			Host:                  host,
			Port:                  port,
			User:                  strings.TrimSpace(r.FormValue("user")),
			Password:              strings.TrimSpace(r.FormValue("password")),
			PrivateKeyPath:        strings.TrimSpace(r.FormValue("private_key_path")),
			PrivateKeyPassphrase:  strings.TrimSpace(r.FormValue("private_key_passphrase")),
			InsecureIgnoreHostKey: &insecure,
			ExportSensitive:       &sensitive,
			EncryptBackup:         &encrypt,
			BackupPassword:        strings.TrimSpace(r.FormValue("backup_password")),
		}
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

	_, _ = s.runner.RunSingle(r.Context(), name)
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
	_, _ = s.runner.Run(r.Context())
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
		Router:    rc,
		Effective: rc.Effective(cfg.Defaults),
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
			Router:    r,
			Effective: r.Effective(cfg.Defaults),
			State:     st,
		})
	}
	return TableData{Items: items}
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

	return SummaryData{
		TotalCount:    len(cfg.Routers),
		ActiveCount:   activeCount,
		SuccessCount:  successCount,
		FailedCount:   failedCount,
		NextRun:       nextRunStr,
		ScheduleTimes: timesStr,
		Timezone:      cfg.Schedule.Timezone,
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
