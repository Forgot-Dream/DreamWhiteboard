package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"dreamwhiteboard/backend/internal/httpapi"
	"dreamwhiteboard/backend/internal/store"
)

func main() {
	logger := newLogger()
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		logger.Error("DATABASE_URL is required; the production server does not use an in-memory store")
		os.Exit(1)
	}
	if err := runMigrations(ctx, databaseURL, env("MIGRATIONS_DIR", "./migrations")); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	repo, err := store.OpenPostgres(ctx, databaseURL)
	if err != nil {
		logger.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	if err := repo.Ready(ctx); err != nil {
		logger.Error("database is not ready after migrations", "error", err)
		os.Exit(1)
	}

	adminEmail := strings.ToLower(strings.TrimSpace(os.Getenv("FIRST_ADMIN_EMAIL")))
	adminPassword := os.Getenv("FIRST_ADMIN_PASSWORD")
	userCount, err := repo.UserCount(ctx)
	if err != nil {
		logger.Error("count users before administrator bootstrap", "error", err)
		os.Exit(1)
	}
	if userCount == 0 {
		if adminEmail == "" || adminPassword == "" {
			logger.Error("database has no users; configure FIRST_ADMIN_EMAIL and FIRST_ADMIN_PASSWORD for initial bootstrap")
			os.Exit(1)
		}
		if !validBootstrapEmail(adminEmail) {
			logger.Error("FIRST_ADMIN_EMAIL must be a valid email address")
			os.Exit(1)
		}
		if len(adminPassword) < 12 {
			logger.Error("FIRST_ADMIN_PASSWORD must contain at least 12 characters")
			os.Exit(1)
		}
		if _, err := repo.EnsureSystemAdmin(adminEmail, adminPassword); err != nil {
			logger.Error("create initial administrator", "error", err)
			os.Exit(1)
		}
	} else if adminEmail != "" || adminPassword != "" {
		logger.Debug("initial administrator bootstrap skipped because users already exist")
	}

	uploadDir := env("UPLOAD_DIR", "./uploads")
	cfg := httpapi.DefaultConfig(uploadDir)
	cfg.AllowedOrigins = csvEnv("ALLOWED_ORIGINS", nil)
	cfg.SessionTTL = durationEnv(logger, "SESSION_TTL", cfg.SessionTTL)
	cfg.CookieSecure = boolEnv(logger, "SESSION_COOKIE_SECURE", true)
	cfg.MaxBodyBytes = int64Env(logger, "MAX_REQUEST_BYTES", cfg.MaxBodyBytes)
	cfg.MaxUploadBytes = int64Env(logger, "MAX_UPLOAD_BYTES", cfg.MaxUploadBytes)
	cfg.MaxImagePixels = int64Env(logger, "MAX_IMAGE_PIXELS", cfg.MaxImagePixels)
	cfg.LoginLimit = intEnv(logger, "LOGIN_RATE_LIMIT", cfg.LoginLimit)
	cfg.LoginWindow = durationEnv(logger, "LOGIN_RATE_WINDOW", cfg.LoginWindow)
	cfg.WSAuthInterval = durationEnv(logger, "WS_AUTH_CHECK_INTERVAL", cfg.WSAuthInterval)
	cfg.TrustProxy = boolEnv(logger, "TRUST_PROXY_HEADERS", false)
	cfg.Logger = logger
	api := httpapi.NewServerWithConfig(repo, cfg)

	server := &http.Server{
		Addr:              env("HTTP_ADDR", ":8080"),
		Handler:           api,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       time.Minute,
		WriteTimeout:      time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("DreamWhiteboard API listening", "address", server.Addr)
		serveErr <- server.ListenAndServe()
	}()

	exitCode := 0
	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server stopped unexpectedly", "error", err)
			exitCode = 1
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful HTTP shutdown failed", "error", err)
		_ = server.Close()
		exitCode = 1
	}
	if err := api.Close(); err != nil {
		logger.Error("close realtime connections", "error", err)
		exitCode = 1
	}
	if err := repo.Close(); err != nil {
		logger.Error("close database", "error", err)
		exitCode = 1
	}
	logger.Info("shutdown complete")
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func validBootstrapEmail(value string) bool {
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value && len(value) <= 254
}

func csvEnv(key string, fallback []string) []string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	items := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, strings.TrimRight(item, "/"))
		}
	}
	return items
}

func durationEnv(logger *slog.Logger, key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		logger.Error("invalid duration environment value", "key", key)
		os.Exit(1)
	}
	return parsed
}

func boolEnv(logger *slog.Logger, key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		logger.Error("invalid boolean environment value", "key", key)
		os.Exit(1)
	}
	return parsed
}

func intEnv(logger *slog.Logger, key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		logger.Error("invalid positive integer environment value", "key", key)
		os.Exit(1)
	}
	return parsed
}

func int64Env(logger *slog.Logger, key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		logger.Error("invalid positive integer environment value", "key", key)
		os.Exit(1)
	}
	return parsed
}
