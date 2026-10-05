package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultHealthcheckDBTimeout = 10 * time.Second
	defaultHealthcheckDBMaxRows = 10000
)

// ErrHealthcheckDBNotConfigured is returned when no healthcheck database DSN was provided.
var ErrHealthcheckDBNotConfigured = errors.New("healthcheck database is not configured")

type HealthcheckDBConfig struct {
	DSN     string
	Timeout time.Duration
	MaxRows int
}

type HealthcheckRecord struct {
	Time     time.Time  `json:"time"`
	Start    *time.Time `json:"start,omitempty"`
	Name     string     `json:"name"`
	Type     string     `json:"type"`
	Node     string     `json:"node"`
	Location string     `json:"location"`
	Code     *int32     `json:"code,omitempty"`
	Message  string     `json:"message"`
	Target   string     `json:"target"`
	Alive    bool       `json:"alive"`
	// Duration is stored in nanoseconds.
	Duration *int64 `json:"duration,omitempty"`
}

// HealthcheckReader reads healthcheck results produced by the healthchecker.
type HealthcheckReader interface {
	RecentHealthchecks(ctx context.Context, location string, since time.Time) ([]HealthcheckRecord, error)
}

type HealthcheckDBAware interface {
	SetHealthcheckDB(db HealthcheckReader)
}

type HealthcheckDB struct {
	pool    *pgxpool.Pool
	timeout time.Duration
	maxRows int
}

// NewHealthcheckDB returns nil without error when no DSN is configured so the API can run without the database.
func NewHealthcheckDB(ctx context.Context, cfg HealthcheckDBConfig) (*HealthcheckDB, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, nil
	}

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid healthcheck database DSN: %w", err)
	}
	if poolCfg.MaxConns > 10 {
		poolCfg.MaxConns = 10
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultHealthcheckDBTimeout
	}
	maxRows := cfg.MaxRows
	if maxRows <= 0 {
		maxRows = defaultHealthcheckDBMaxRows
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create healthcheck database pool: %w", err)
	}

	return &HealthcheckDB{pool: pool, timeout: timeout, maxRows: maxRows}, nil
}

// Ping verifies the connection to the database.
func (d *HealthcheckDB) Ping(ctx context.Context) error {
	if d == nil {
		return ErrHealthcheckDBNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	return d.pool.Ping(ctx)
}

func (d *HealthcheckDB) Close() {
	if d != nil {
		d.pool.Close()
	}
}

const recentHealthchecksQuery = `
SELECT time, start, COALESCE(name, ''), COALESCE(type, ''), COALESCE(node, ''), COALESCE(location, ''),
       code, COALESCE(message, ''), COALESCE(target, ''), COALESCE(alive, false), duration
FROM healthchecks
WHERE location = $1 AND time >= $2
ORDER BY time DESC
LIMIT $3`

// RecentHealthchecks returns healthchecks for a location newer than since, newest first.
func (d *HealthcheckDB) RecentHealthchecks(ctx context.Context, location string, since time.Time) ([]HealthcheckRecord, error) {
	if d == nil {
		return nil, ErrHealthcheckDBNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	rows, err := d.pool.Query(ctx, recentHealthchecksQuery, location, since, d.maxRows)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (HealthcheckRecord, error) {
		var r HealthcheckRecord
		err := row.Scan(&r.Time, &r.Start, &r.Name, &r.Type, &r.Node, &r.Location, &r.Code, &r.Message, &r.Target, &r.Alive, &r.Duration)
		return r, err
	})
}
