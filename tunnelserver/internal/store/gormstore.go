package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// tunnelRow is the GORM model for a tunnel record. It lives in the default
// (public) schema and is created/maintained by AutoMigrate. The id format is
// guaranteed by store.NewID (a 12-char DNS-label), so no DB-level CHECK is
// needed (and a regex CHECK can't be expressed in a GORM tag — the comma in
// {10,32} collides with the tag grammar).
type tunnelRow struct {
	ID                string `gorm:"primaryKey;column:id"`
	ConnectSecretHash []byte `gorm:"column:connect_secret_hash;not null"`
	CreatedAt         time.Time
	LastConnectedAt   *time.Time
	ConnectCount      int64 `gorm:"column:connect_count;not null;default:0"`
	Revoked           bool  `gorm:"column:revoked;not null;default:false"`
}

// TableName pins the table name regardless of struct naming.
func (tunnelRow) TableName() string { return "tunnels" }

// Gorm is a Postgres-backed Store using GORM. It connects directly to Postgres
// (a standard DSN — not the Supabase Data API) and creates/migrates its table
// on startup via AutoMigrate.
type Gorm struct {
	db *gorm.DB
}

// NewGorm opens a GORM Postgres connection, runs AutoMigrate, and verifies the
// connection.
func NewGorm(ctx context.Context, dsn string) (*Gorm, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true, // surface gorm.ErrDuplicatedKey / ErrRecordNotFound
	})
	if err != nil {
		return nil, fmt.Errorf("opening postgres: %w", err)
	}
	if err := db.WithContext(ctx).AutoMigrate(&tunnelRow{}); err != nil {
		return nil, fmt.Errorf("migrating tunnels table: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pctx); err != nil {
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}
	return &Gorm{db: db}, nil
}

func (g *Gorm) Create(ctx context.Context, t *Tunnel) error {
	row := tunnelRow{ID: t.ID, ConnectSecretHash: t.ConnectSecretHash, Revoked: t.Revoked}
	err := g.db.WithContext(ctx).Create(&row).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrConflict
	}
	return err
}

func (g *Gorm) GetByID(ctx context.Context, id string) (*Tunnel, error) {
	var row tunnelRow
	err := g.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &Tunnel{ID: row.ID, ConnectSecretHash: row.ConnectSecretHash, Revoked: row.Revoked}, nil
}

func (g *Gorm) MarkConnected(ctx context.Context, id string) error {
	return g.db.WithContext(ctx).Model(&tunnelRow{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"last_connected_at": gorm.Expr("now()"),
			"connect_count":     gorm.Expr("connect_count + 1"),
		}).Error
}

func (g *Gorm) Close() {
	if sqlDB, err := g.db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
