package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"dreamwhiteboard/backend/internal/store"
	"github.com/lib/pq"
)

func TestRunMigrationsIntegration(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping migration integration test")
	}
	admin, err := sql.Open("postgres", baseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("dwmigration_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA IF EXISTS ` + pq.QuoteIdentifier(schema) + ` CASCADE`) })
	databaseURL := migrationTestSearchPath(t, baseURL, schema)

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate migration test source")
	}
	sourceDirectory := filepath.Join(filepath.Dir(currentFile), "..", "..", "migrations")
	testDirectory := t.TempDir()
	entries, err := os.ReadDir(sourceDirectory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(sourceDirectory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(testDirectory, entry.Name()), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runMigrations(ctx, databaseURL, testDirectory); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	if err := runMigrations(ctx, databaseURL, testDirectory); err != nil {
		t.Fatalf("idempotent migration check: %v", err)
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var versions int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != store.LatestSchemaVersion {
		t.Fatalf("recorded migration count = %d, want %d", versions, store.LatestSchemaVersion)
	}

	latest := ""
	latestPrefix := fmt.Sprintf("%03d_", store.LatestSchemaVersion)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), latestPrefix) && filepath.Ext(entry.Name()) == ".sql" {
			latest = filepath.Join(testDirectory, entry.Name())
			break
		}
	}
	if latest == "" {
		t.Fatalf("latest migration with prefix %q was not found", latestPrefix)
	}
	file, err := os.OpenFile(latest, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n-- tampered\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, databaseURL, testDirectory); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered migration should fail checksum validation, got %v", err)
	}
}

func migrationTestSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	if strings.HasPrefix(databaseURL, "postgres://") || strings.HasPrefix(databaseURL, "postgresql://") {
		parsed, err := url.Parse(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return databaseURL + " search_path=" + schema
}
