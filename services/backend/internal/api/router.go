package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/knot-dns-monitor/backend/internal/config"
)

type Server struct {
	cfg        *config.Config
	promURL    string
	ch         driver.Conn
	pg         *pgxpool.Pool
	rdb        *redis.Client
	httpClient *http.Client
}

func NewRouter(cfg *config.Config) (http.Handler, func(), error) {
	// ClickHouse connection
	chHost := strings.TrimPrefix(cfg.ClickHouseURL, "http://")
	chHost = strings.Replace(chHost, "8123", "9000", 1)

	ch, err := clickhouse.Open(&clickhouse.Options{
		Addr:            []string{chHost},
		Auth:            clickhouse.Auth{Database: cfg.ClickHouseDB, Username: "default", Password: ""},
		DialTimeout:     5 * time.Second,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: 10 * time.Minute,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("clickhouse: %w", err)
	}

	// PostgreSQL connection pool
	pg, err := pgxpool.New(context.Background(), cfg.PostgresURL)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: %w", err)
	}

	// Initialize PostgreSQL schema
	if err := initPostgres(pg); err != nil {
		return nil, nil, fmt.Errorf("postgres init: %w", err)
	}

	// Redis client
	opt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, nil, fmt.Errorf("redis parse url: %w", err)
	}
	rdb := redis.NewClient(opt)

	srv := &Server{
		cfg:     cfg,
		promURL: cfg.PrometheusURL,
		ch:      ch,
		pg:      pg,
		rdb:     rdb,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	r := chi.NewRouter()

	// Middleware
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Compress(5))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Public routes (no auth)
	r.Get("/api/health", srv.handleHealth)
	r.Get("/api/version", srv.handleVersion)
	r.Post("/api/auth/login", srv.handleLogin)
	r.Post("/api/auth/register", srv.handleRegister)
	r.Get("/api/auth/check", srv.handleAuthCheck)

	// Prometheus scrape endpoint — internal, no auth
	r.Get("/api/internal/metrics", srv.handleKresdMetrics)

	// Admin update routes (no timeout — SSE streams for minutes)
	r.Route("/api/admin", func(r chi.Router) {
		r.Use(srv.authMiddleware)
		r.Use(srv.adminMiddleware)

		r.Get("/update/check", srv.handleUpdateCheck)
		r.Post("/update/execute", srv.handleUpdateExecute)
		r.Get("/update/status", srv.handleUpdateStatus)
	})

	// Protected API routes (require JWT, with timeout)
	r.Route("/api", func(r chi.Router) {
		r.Use(srv.authMiddleware)
		r.Use(chimw.Timeout(30 * time.Second))

		// User info
		r.Get("/auth/me", srv.handleMe)
		r.Post("/auth/change-password", srv.handleChangePassword)

		// Resolver info (live config from kresd)
		r.Get("/resolver/info", srv.handleResolverInfo)

		// Metrics endpoints (proxy to Prometheus)
		r.Route("/metrics", func(r chi.Router) {
			r.Get("/qps", srv.handleMetricsQPS)
			r.Get("/latency", srv.handleMetricsLatency)
			r.Get("/cache", srv.handleMetricsCache)
			r.Get("/dnssec", srv.handleMetricsDNSSEC)
			r.Get("/system", srv.handleMetricsSystem)
			r.Get("/upstreams", srv.handleMetricsUpstreams)
			r.Get("/overview", srv.handleMetricsOverview)
		})

		// Query log endpoints (ClickHouse)
		r.Route("/queries", func(r chi.Router) {
			r.Get("/", srv.handleQuerySearch)
			r.Get("/top-domains", srv.handleTopDomains)
			r.Get("/type-distribution", srv.handleTypeDistribution)
			r.Get("/rcode-distribution", srv.handleRcodeDistribution)
			r.Get("/protocol-distribution", srv.handleProtocolDistribution)
			r.Get("/timeline", srv.handleQueryTimeline)
		})

		// Alert endpoints
		r.Route("/alerts", func(r chi.Router) {
			r.Get("/", srv.handleListAlerts)
			r.Post("/", srv.handleCreateAlert)
			r.Put("/{id}", srv.handleUpdateAlert)
			r.Delete("/{id}", srv.handleDeleteAlert)
			r.Get("/history", srv.handleAlertHistory)
		})

		// WebSocket for real-time updates
		r.Get("/ws/live", srv.handleWebSocket)
	})

	cleanup := func() {
		ch.Close()
		pg.Close()
		rdb.Close()
	}

	return r, cleanup, nil
}

func initPostgres(pool *pgxpool.Pool) error {
	ctx := context.Background()
	queries := []string{
		`CREATE TABLE IF NOT EXISTS alert_rules (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			metric VARCHAR(100) NOT NULL,
			condition VARCHAR(20) NOT NULL,
			threshold DOUBLE PRECISION NOT NULL,
			duration_sec INT NOT NULL DEFAULT 60,
			enabled BOOLEAN NOT NULL DEFAULT true,
			notify_channels JSONB NOT NULL DEFAULT '[]',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS alert_events (
			id SERIAL PRIMARY KEY,
			rule_id INT REFERENCES alert_rules(id) ON DELETE CASCADE,
			status VARCHAR(20) NOT NULL,
			value DOUBLE PRECISION,
			message TEXT,
			fired_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			resolved_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			username VARCHAR(100) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			role VARCHAR(20) NOT NULL DEFAULT 'viewer',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	}

	for _, q := range queries {
		if _, err := pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("exec %q: %w", q[:50], err)
		}
	}

	log.Println("PostgreSQL schema initialized")
	return nil
}
