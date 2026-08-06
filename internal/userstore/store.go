package userstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Mapping represents the link between a Dex user and their Odoo credentials.
// The API key is stored encrypted (APIKeyEncrypted).
type Mapping struct {
	DexUserID        string
	Email            string
	OdooURL          string
	OdooDB           string
	OdooLogin        string
	OdooUID          int
	APIKeyEncrypted  string
	CreatedAt        time.Time
}

// Store provides CRUD operations for user mappings backed by PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// Connect opens a connection pool to PostgreSQL using the given database URL.
// The caller must call Close when the Store is no longer needed.
func Connect(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("userstore connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("userstore ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close closes the PostgreSQL connection pool.
func (s *Store) Close() error {
	s.pool.Close()
	return nil
}

// GetMapping retrieves a user mapping by Dex user ID.
// Returns nil, nil if no mapping exists for the given ID.
func (s *Store) GetMapping(ctx context.Context, dexUserID string) (*Mapping, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT dex_user_id, email, odoo_url, odoo_db, odoo_login, odoo_uid, api_key_encrypted, created_at
		 FROM user_mappings WHERE dex_user_id = $1`,
		dexUserID,
	)

	m := &Mapping{}
	err := row.Scan(
		&m.DexUserID,
		&m.Email,
		&m.OdooURL,
		&m.OdooDB,
		&m.OdooLogin,
		&m.OdooUID,
		&m.APIKeyEncrypted,
		&m.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get mapping %s: %w", dexUserID, err)
	}

	return m, nil
}

// CreateMapping inserts a new user mapping. The DexUserID field must be unique.
func (s *Store) CreateMapping(ctx context.Context, m *Mapping) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO user_mappings
			(dex_user_id, email, odoo_url, odoo_db, odoo_login, odoo_uid, api_key_encrypted, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		m.DexUserID,
		m.Email,
		m.OdooURL,
		m.OdooDB,
		m.OdooLogin,
		m.OdooUID,
		m.APIKeyEncrypted,
		m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create mapping %s: %w", m.DexUserID, err)
	}
	return nil
}

// DeleteMapping removes a user mapping by Dex user ID.
func (s *Store) DeleteMapping(ctx context.Context, dexUserID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM user_mappings WHERE dex_user_id = $1`,
		dexUserID,
	)
	if err != nil {
		return fmt.Errorf("delete mapping %s: %w", dexUserID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete mapping %s: %w", dexUserID, pgx.ErrNoRows)
	}
	return nil
}

// ListMappings returns all user mappings.
func (s *Store) ListMappings(ctx context.Context) ([]*Mapping, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT dex_user_id, email, odoo_url, odoo_db, odoo_login, odoo_uid, api_key_encrypted, created_at
		 FROM user_mappings ORDER BY dex_user_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list mappings: %w", err)
	}
	defer rows.Close()

	var mappings []*Mapping
	for rows.Next() {
		m := &Mapping{}
		if err := rows.Scan(
			&m.DexUserID,
			&m.Email,
			&m.OdooURL,
			&m.OdooDB,
			&m.OdooLogin,
			&m.OdooUID,
			&m.APIKeyEncrypted,
			&m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("list mappings scan: %w", err)
		}
		mappings = append(mappings, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list mappings rows: %w", err)
	}

	return mappings, nil
}