package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"dreamwhiteboard/backend/internal/store"
	_ "github.com/lib/pq"
)

func runMigrations(ctx context.Context, databaseURL, directory string) error {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum TEXT`); err != nil {
		return err
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read migration directory: %w", err)
	}
	type migration struct {
		version  int64
		name     string
		contents []byte
		checksum string
	}
	migrations := []migration{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return fmt.Errorf("migration %q must start with a numeric version and underscore", entry.Name())
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil || version <= 0 {
			return fmt.Errorf("migration %q has an invalid version", entry.Name())
		}
		if !validMigrationFilename(entry.Name(), version) {
			return fmt.Errorf("migration %q must use a zero-padded numeric prefix and lowercase snake_case name", entry.Name())
		}
		contents, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(contents)
		migrations = append(migrations, migration{
			version:  version,
			name:     entry.Name(),
			contents: contents,
			checksum: hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	if len(migrations) != store.LatestSchemaVersion {
		return fmt.Errorf("found %d migration files, but server requires exactly %d", len(migrations), store.LatestSchemaVersion)
	}
	knownVersions := make(map[int64]migration, len(migrations))
	for index, migration := range migrations {
		if index > 0 && migrations[index-1].version == migration.version {
			return fmt.Errorf("duplicate migration version %d", migration.version)
		}
		if migration.version != int64(index+1) {
			return fmt.Errorf("migration history has a gap: found version %d at position %d", migration.version, index+1)
		}
		knownVersions[migration.version] = migration
	}
	appliedRows, err := db.QueryContext(ctx, `SELECT version, name, COALESCE(checksum, '') FROM schema_migrations ORDER BY version`)
	if err != nil {
		return err
	}
	expectedApplied := int64(1)
	for appliedRows.Next() {
		var version int64
		var name, checksum string
		if err := appliedRows.Scan(&version, &name, &checksum); err != nil {
			appliedRows.Close()
			return err
		}
		migration, ok := knownVersions[version]
		if !ok {
			appliedRows.Close()
			return fmt.Errorf("database contains unknown migration version %d", version)
		}
		if version != expectedApplied {
			appliedRows.Close()
			return fmt.Errorf("database migration history has a gap before version %d", version)
		}
		if name != migration.name || checksum != migration.checksum {
			appliedRows.Close()
			return fmt.Errorf("migration version %d does not match file %s checksum", version, migration.name)
		}
		expectedApplied++
	}
	if err := appliedRows.Close(); err != nil {
		return err
	}
	for _, migration := range migrations {
		var applied bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, migration.version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(migration.contents)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", migration.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, checksum) VALUES ($1,$2,$3)`, migration.version, migration.name, migration.checksum); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", migration.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", migration.name, err)
		}
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE schema_migrations ALTER COLUMN checksum SET NOT NULL`); err != nil {
		return fmt.Errorf("enforce migration checksum constraint: %w", err)
	}
	return nil
}

func validMigrationFilename(name string, version int64) bool {
	prefix := fmt.Sprintf("%03d_", version)
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".sql") {
		return false
	}
	base := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".sql")
	if base == "" {
		return false
	}
	for _, character := range base {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}
