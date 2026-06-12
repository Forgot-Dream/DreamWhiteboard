package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"dreamwhiteboard/backend/internal/httpapi"
	"dreamwhiteboard/backend/internal/store"
)

func main() {
	addr := env("HTTP_ADDR", ":8080")
	uploadDir := env("UPLOAD_DIR", "./uploads")

	repo := store.Repository(store.NewMemoryStore())
	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		pg, err := store.OpenPostgres(context.Background(), databaseURL)
		if err != nil {
			log.Fatalf("connect postgres: %v", err)
		}
		repo = pg
	}
	adminEmail := env("FIRST_ADMIN_EMAIL", "admin@example.com")
	adminPassword := env("FIRST_ADMIN_PASSWORD", "admin123")
	if _, err := repo.EnsureSystemAdmin(adminEmail, adminPassword); err != nil {
		log.Fatalf("create initial admin: %v", err)
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewServer(repo, uploadDir),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("dreamwhiteboard api listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
