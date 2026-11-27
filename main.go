package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed templates/*.html static/* assets/fonts/*
var embedded embed.FS

// Config holds everything that can be tuned from the environment.
type Config struct {
	Port          string
	DataDir       string
	APIKey        string
	SessionSecret string
	SiteName      string
	SiteTagline   string
	PublicBaseURL string
	MaxUploadMB   int64
	MaxTotalMB    int64
}

type nonceContextKey struct{}

func newNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "fallback"
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func nonceFromRequest(r *http.Request) string {
	nonce, _ := r.Context().Value(nonceContextKey{}).(string)
	return nonce
}

type App struct {
	cfg           Config
	store         *Store
	tmpl          *template.Template
	sessionSecret []byte
	started       time.Time

	loginMu    sync.Mutex
	loginFails map[string]*loginAttempt

	ogMu    sync.Mutex
	ogCache map[string][]byte
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			os.Exit(runHealthcheck())
		case "localize":
			os.Exit(runLocalize())
		}
	}

	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[p2611003] ")

	cfg := loadConfig()
	store, err := NewStore(cfg.DataDir, cfg.MaxTotalMB<<20)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}

	app, err := newApp(cfg, store)
	if err != nil {
		log.Fatalf("init app: %v", err)
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           app.handler(app.routes()),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errc := make(chan error, 1)
	go func() {
		log.Printf("%s is running at http://0.0.0.0:%s (data: %s)", cfg.SiteName, cfg.Port, cfg.DataDir)
		errc <- srv.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errc:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	case sig := <-stop:
		log.Printf("Received %s, shutting down…", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}
}

var assetURLs sync.Map

// asset resolves a static file to a URL carrying a content hash, so a deploy
// is picked up by browser and CDN caches without manual version bumps.
func asset(name string) string {
	if cached, ok := assetURLs.Load(name); ok {
		return cached.(string)
	}
	url := "/static/" + name
	if b, err := embedded.ReadFile("static/" + name); err == nil {
		sum := sha256.Sum256(b)
		url += "?v=" + hex.EncodeToString(sum[:4])
	}
	assetURLs.Store(name, url)
	return url
}

func newApp(cfg Config, store *Store) (*App, error) {
	funcs := template.FuncMap{
		"typeLabel":      typeLabel,
		"typeShort":      typeShort,
		"formatSize":     formatSize,
		"formatDate":     formatDate,
		"formatDateTime": formatDateTime,
		"add":            func(a, b int) int { return a + b },
		"sub":            func(a, b int) int { return a - b },
		"asset":          asset,
		"urlquery":       url.QueryEscape,
	}
	tmpl, err := template.New("p2611003").Funcs(funcs).ParseFS(embedded, "templates/*.html")
	if err != nil {
		return nil, err
	}

	secret := cfg.SessionSecret
	if secret == "" {
		sum := sha256.Sum256([]byte(cfg.APIKey + "|p2611003-session-v1"))
		secret = hex.EncodeToString(sum[:])
	}

	return &App{
		cfg:           cfg,
		store:         store,
		tmpl:          tmpl,
		sessionSecret: []byte(secret),
		started:       time.Now(),
		loginFails:    map[string]*loginAttempt{},
		ogCache:       map[string][]byte{},
	}, nil
}

func loadConfig() Config {
	cfg := Config{
		Port:          env("PORT", "8080"),
		DataDir:       env("DATA_DIR", "./data"),
		APIKey:        os.Getenv("API_KEY"),
		SiteName:      env("SITE_NAME", "Report for public"),
		SiteTagline:   env("SITE_TAGLINE", "Public documents"),
		PublicBaseURL: strings.TrimRight(env("PUBLIC_BASE_URL", ""), "/"),
		SessionSecret: os.Getenv("SESSION_SECRET"),
		MaxUploadMB:   50,
		MaxTotalMB:    2048,
	}
	if cfg.APIKey == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		cfg.APIKey = hex.EncodeToString(b)
		log.Printf("API_KEY is not set; generated a temporary key: %s", cfg.APIKey)
	}
	if raw := os.Getenv("MAX_UPLOAD_MB"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 1 && n <= 500 {
			cfg.MaxUploadMB = n
		}
	}
	if raw := os.Getenv("MAX_TOTAL_MB"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 && n <= 1_000_000 {
			cfg.MaxTotalMB = n
		}
	}
	return cfg
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	staticFS, err := fs.Sub(embedded, "static")
	if err != nil {
		log.Fatalf("static assets: %v", err)
	}
	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cache := "public, max-age=3600"
		if r.URL.Query().Get("v") != "" {
			// Asset URLs carry a content hash, so they are safe to cache for a year.
			cache = "public, max-age=31536000, immutable"
		}
		w.Header().Set("Cache-Control", cache)
		fileServer.ServeHTTP(w, r)
	}))

	mux.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/favicon.svg"
		w.Header().Set("Cache-Control", "public, max-age=86400")
		fileServer.ServeHTTP(w, r)
	})

	mux.HandleFunc("GET /healthz", a.handleHealth)
	mux.HandleFunc("GET /api", a.handleAPIDocs)
	mux.HandleFunc("GET /robots.txt", a.handleRobots)
	mux.HandleFunc("GET /sitemap.xml", a.handleSitemap)
	mux.HandleFunc("GET /feed.xml", a.handleFeed)
	mux.HandleFunc("GET /{$}", a.handleHome)
	mux.HandleFunc("GET /d/{id}", a.handleView)
	mux.HandleFunc("GET /raw/{id}", a.handleRaw)
	mux.HandleFunc("GET /download/{id}", a.handleDownload)
	mux.HandleFunc("GET /version/{id}/{versionID}", a.handleVersionDownload)
	mux.HandleFunc("GET /og/{id}", a.handleOGImage)
	mux.HandleFunc("GET /card/{id}", a.handleCardImage)
	mux.HandleFunc("GET /media/{id}/{name}", a.handleMedia)

	// Studio routes are intentionally unlinked from the public UI. The URL is
	// documented in README instead of being advertised in navigation. Access
	// still requires the admin key.
	mux.HandleFunc("GET /studio", a.handleLoginPage)
	mux.HandleFunc("POST /studio", a.handleLogin)
	mux.HandleFunc("POST /studio/logout", a.handleLogout)
	mux.HandleFunc("GET /studio/upload", a.requireSession(a.handleUploadPage))
	mux.HandleFunc("POST /studio/upload", a.requireSession(a.handleUploadForm))
	mux.HandleFunc("GET /studio/admin", a.requireSession(a.handleAdmin))
	mux.HandleFunc("POST /studio/admin/delete/{id}", a.requireSession(a.handleAdminDelete))
	mux.HandleFunc("POST /studio/admin/replace/{id}", a.requireSession(a.handleAdminReplace))

	mux.HandleFunc("POST /api/upload", a.handleAPIUpload)
	mux.HandleFunc("GET /api/documents", a.handleAPIList)
	mux.HandleFunc("GET /api/documents/{id}", a.handleAPIGet)
	mux.HandleFunc("GET /api/documents/{id}/versions", a.handleAPIVersions)
	mux.HandleFunc("DELETE /api/documents/{id}", a.handleAPIDelete)
	mux.HandleFunc("PUT /api/documents/{id}", a.handleAPIReplace)

	mux.HandleFunc("/", a.handleNotFound)
	return mux
}

func (a *App) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}

		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic on %s %s: %v", r.Method, r.URL.Path, rec)
				if !sw.wrote {
					http.Error(sw, "internal server error", http.StatusInternalServerError)
				}
			}
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		}()

		nonce := newNonce()
		r = r.WithContext(context.WithValue(r.Context(), nonceContextKey{}, nonce))

		scheme := requestScheme(r)
		if scheme == "http" && !isLoopbackHost(r.Host) {
			target := "https://" + r.Host + r.URL.RequestURI()
			http.Redirect(sw, r, target, http.StatusPermanentRedirect)
			return
		}

		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("X-Permitted-Cross-Domain-Policies", "none")
		h.Set("Content-Security-Policy", fmt.Sprintf("default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'nonce-%s'; frame-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'", nonce))
		if scheme == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(sw, r)
	})
}

// publicBaseURL prefers the configured canonical origin, so links, sitemap
// entries and API URLs do not depend on a spoofable Host header.
func (a *App) publicBaseURL(r *http.Request) string {
	if a.cfg.PublicBaseURL != "" {
		return a.cfg.PublicBaseURL
	}
	return requestBaseURL(r)
}

func requestScheme(r *http.Request) string {
	if p := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))); p == "http" || p == "https" {
		return p
	}
	if raw := r.Header.Get("CF-Visitor"); raw != "" {
		var visitor struct {
			Scheme string `json:"scheme"`
		}
		if json.Unmarshal([]byte(raw), &visitor) == nil {
			if s := strings.ToLower(visitor.Scheme); s == "http" || s == "https" {
				return s
			}
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.status = http.StatusOK
		w.wrote = true
	}
	return w.ResponseWriter.Write(p)
}

func (a *App) render(w http.ResponseWriter, status int, name string, data any) {
	var buf strings.Builder
	if err := a.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(buf.String()))
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"service":   "p2611003",
		"uptime":    time.Since(a.started).Round(time.Second).String(),
		"documents": a.store.Stats().Count,
	})
}

func (a *App) handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", a.publicBaseURL(r))
}

func (a *App) handleNotFound(w http.ResponseWriter, r *http.Request) {
	base := a.privateBase(r, "")
	base.Title = "Page not found · " + a.cfg.SiteName
	a.render(w, http.StatusNotFound, "notfound", base)
}

func (a *App) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.isAdmin(r) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			target := "/studio"
			if r.Method == http.MethodGet {
				target += "?next=" + url.QueryEscape(r.URL.RequestURI())
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && !sameOrigin(r) {
			http.Error(w, "cross-site request blocked", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func runLocalize() int {
	cfg := loadConfig()
	store, err := NewStore(cfg.DataDir, cfg.MaxTotalMB<<20)
	if err != nil {
		fmt.Fprintln(os.Stderr, "localize:", err)
		return 1
	}
	n, err := store.LocalizeExistingMarkdown()
	if err != nil {
		fmt.Fprintln(os.Stderr, "localize:", err)
		return 1
	}
	fmt.Printf("localized %d Markdown document(s) in %s\n", n, cfg.DataDir)
	return 0
}

func runHealthcheck() int {
	port := env("PORT", "8080")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}
