package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHealthcheckDBReadsSources(t *testing.T) {
	dsn := os.Getenv("HEALTHCHECK_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("set HEALTHCHECK_TEST_DB_DSN to an isolated PostgreSQL/TimescaleDB test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the temporary table and queries on the same connection.
	cfg.MaxConns = 1
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	db := &HealthcheckDB{pool: pool, timeout: 10 * time.Second, maxRows: 10}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
CREATE TEMP TABLE healthchecks (
    time timestamptz NOT NULL, start timestamptz, name text, type text, node text,
    location text, code integer, message text, target text, alive boolean, duration bigint, source text
)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i, source := range []any{"ams", "fra", nil} {
		_, err := pool.Exec(ctx, `
INSERT INTO healthchecks (time, name, type, node, location, code, message, target, alive, duration, source)
VALUES ($1, 'http', 'HTTP', 'n1', $2, 200, 'OK', '74.220.31.183', true, 4879531, $3)`,
			now.Add(-time.Duration(i)*time.Second), "edgecdnx/fra1-c1", source)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = pool.Exec(ctx, `
INSERT INTO healthchecks (time, location, source) VALUES
($1, 'edgecdnx/other', 'other'), ($2, 'edgecdnx/fra1-c1', 'old')`, now, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	records, err := db.RecentHealthchecks(ctx, "edgecdnx/fra1-c1", now.Add(-15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("expected three recent results for this location, got %+v", records)
	}
	for i, want := range []string{"ams", "fra", ""} {
		if records[i].Source != want || records[i].Node != "n1" || !records[i].Alive {
			t.Fatalf("unexpected result %d: %+v", i, records[i])
		}
		if records[i].Duration == nil || *records[i].Duration != 4879531 {
			t.Fatalf("duration scan changed: %+v", records[i])
		}
	}
	db.maxRows = 2
	records, err = db.RecentHealthchecks(ctx, "edgecdnx/fra1-c1", now.Add(-15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Source != "ams" || records[1].Source != "fra" {
		t.Fatalf("expected row limit and newest-first ordering, got %+v", records)
	}
}
