// Package storage provides PostgreSQL persistence for metrics, logs, and traces.
package storage

import (
	"context"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgx connection pool and exposes repository methods.
type DB struct {
	pool *pgxpool.Pool
}

// Connect creates a connection pool and runs all pending migrations.
func Connect(ctx context.Context, dsn, migrationsPath string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}

	if migrationsPath != "" {
		if err := runMigrations(dsn, migrationsPath); err != nil {
			return nil, fmt.Errorf("migrations: %w", err)
		}
	}

	return &DB{pool: pool}, nil
}

func runMigrations(dsn, path string) error {
	m, err := migrate.New("file://"+path, dsn)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

// Close releases all pool connections.
func (db *DB) Close() { db.pool.Close() }

// Pool exposes the underlying pgxpool for advanced queries in tests.
func (db *DB) Pool() *pgxpool.Pool { return db.pool }
