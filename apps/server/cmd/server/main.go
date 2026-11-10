package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	runapp "qual/internal/agentrun/application"
	runpg "qual/internal/agentrun/infrastructure/postgres"
	evidenceapp "qual/internal/evidence/application"
	evidencepg "qual/internal/evidence/infrastructure/postgres"
	evidenceS3 "qual/internal/evidence/infrastructure/s3store"
	eventapp "qual/internal/externalevent/application"
	eventpg "qual/internal/externalevent/infrastructure/postgres"
	identityapp "qual/internal/identity/application"
	idpostgres "qual/internal/identity/infrastructure/postgres"
	webhooks "qual/internal/integration/webhooks"
	"qual/internal/platform/httpapi"
	testapp "qual/internal/testing/application"
	testpostgres "qual/internal/testing/infrastructure/postgres"
	workapp "qual/internal/work/application"
	workpostgres "qual/internal/work/infrastructure/postgres"
	contextapp "qual/internal/workcontext/application"
	contextpg "qual/internal/workcontext/infrastructure/postgres"
)

// loopbackBind rejects accidental public exposure while the application still
// uses local Human sign-in codes and has no production-grade OAuth/TLS boundary.
func loopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	addr := os.Getenv("QUAL_HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:18080"
	}
	if !loopbackBind(addr) {
		logger.Error("Phase 3 local-session service must listen on loopback only", "addr", addr)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	metricsToken := os.Getenv("QUAL_METRICS_TOKEN")
	if metricsToken != "" && len(metricsToken) < 32 {
		logger.Error("QUAL_METRICS_TOKEN must contain at least 32 characters")
		os.Exit(1)
	}
	var deps *httpapi.Dependencies
	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		pool, err := pgxpool.New(ctx, databaseURL)
		if err != nil {
			logger.Error("database configuration invalid", "error", err)
			os.Exit(1)
		}
		defer pool.Close()
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err != nil {
			logger.Error("database unavailable", "error", err)
			os.Exit(1)
		}
		dbSchema, err := inspectSchema(ctx, pool)
		if err != nil {
			logger.Error("unsupported or incomplete migration state", "error", err)
			os.Exit(1)
		}
		// Route gates reflect tables actually present, never the highest Goose
		// number alone (Goose can otherwise skip a late-arriving migration).
		var hasThreadSchema, hasAgentRunSchema bool
		if err := pool.QueryRow(ctx, `SELECT
			to_regclass('qual.external_threads') IS NOT NULL AND to_regclass('qual.work_thread_links') IS NOT NULL,
			to_regclass('qual.agent_runs') IS NOT NULL AND to_regclass('qual.agent_run_reports') IS NOT NULL`).Scan(&hasThreadSchema, &hasAgentRunSchema); err != nil {
			logger.Error("schema probe failed", "error", err)
			os.Exit(1)
		}
		var threadRecords evidenceapp.ThreadRepository
		if dbSchema.Version >= 14 && hasThreadSchema {
			threadRecords = evidencepg.Repository{Pool: pool}
		}
		auth := idpostgres.Repository{Pool: pool}
		objects, err := evidenceS3.NewFromEnv(ctx)
		if err != nil {
			logger.Error("S3 configuration unavailable", "error", err)
			os.Exit(1)
		}
		whCfg, err := webhooks.FromEnv()
		if err != nil {
			logger.Error("webhook configuration invalid")
			os.Exit(1)
		}
		hooks := &webhooks.Manager{Pool: pool, Identity: auth, Config: whCfg}
		readiness := func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return err
			}
			if _, err := inspectSchema(ctx, pool); err != nil {
				return err
			}
			if objects != nil {
				return objects.Check(ctx)
			}
			return nil
		}
		workService := workapp.Service{Identity: auth, Work: workpostgres.Repository{Pool: pool}, TaskPolicy: workpostgres.Repository{Pool: pool}, Roles: workpostgres.Repository{Pool: pool}, Completion: workpostgres.Repository{Pool: pool}, ReviewPolicy: workpostgres.Repository{Pool: pool}, AcceptanceHistory: workpostgres.Repository{Pool: pool}, WorkReviewPolicy: workpostgres.Repository{Pool: pool}, ReviewAssignments: workpostgres.Repository{Pool: pool}}
		deps = &httpapi.Dependencies{
			Identity:     auth,
			Ready:        readiness,
			MetricsToken: metricsToken,
			Logger:       logger,
			Browser:      auth,
			WebOrigin:    os.Getenv("QUAL_WEB_ORIGIN"),
			Work:         workService,
			Access:       identityapp.AccessService{Identity: auth, Repository: auth},
			Tests:        testapp.Service{Identity: auth, Tests: testpostgres.Repository{Pool: pool}},
			Evidence:     evidenceapp.Service{Identity: auth, Records: evidencepg.Repository{Pool: pool, Objects: objects}, Threads: threadRecords, Objects: objects},
			Webhooks:     hooks,
		}
		// Context's bounded Attempt scans require Goose15's four real indexes.
		// On supported Goose12/13/14 the legacy API stays available, but this
		// new route must not be registered (not even for overview reads).
		if dbSchema.allowsContext() {
			deps.Context = &contextapp.Service{Identity: auth, Work: workService, Reader: contextpg.Repository{Pool: pool}}
		}
		if dbSchema.allowsEvents() && hasThreadSchema {
			deps.Events = &eventapp.Service{Identity: auth, Events: eventpg.Repository{Pool: pool}}
			if hasAgentRunSchema {
				deps.Work.Attention = workpostgres.Repository{Pool: pool}
			}
		}
		if dbSchema.Version >= 16 && hasThreadSchema && hasAgentRunSchema {
			sampleSeconds := 60
			if raw := os.Getenv("QUAL_AGENT_RUN_SAMPLE_SECONDS"); raw != "" {
				configured, parseErr := strconv.Atoi(raw)
				if parseErr != nil || configured < 60 || configured > 3600 {
					logger.Error("QUAL_AGENT_RUN_SAMPLE_SECONDS must be 60..3600")
					os.Exit(1)
				}
				sampleSeconds = configured
			}
			sample := time.Duration(sampleSeconds) * time.Second
			staleAfter := 5 * time.Minute
			if 3*sample > staleAfter {
				staleAfter = 3 * sample
			}
			deps.Runs = runapp.Service{Identity: auth, Runs: runpg.Repository{Pool: pool}, HeartbeatSampleInterval: sample, StaleAfter: staleAfter}
			deps.Work.AttentionStaleAfter = staleAfter
		}
		logger.Info("Local business and evidence routes enabled", "agent_run_reports", hasAgentRunSchema)
	} else {
		logger.Warn("DATABASE_URL not set: only liveness endpoint is available")
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewHandler(deps),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       45 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown", "error", err)
		}
	}()

	logger.Info("Qual HTTP server starting", "addr", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}
