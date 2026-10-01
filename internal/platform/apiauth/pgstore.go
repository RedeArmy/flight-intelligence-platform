package apiauth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
)

// PGStore is the PostgreSQL KeyStore. It runs as the runtime role, which may read keys and touch last_used_at only.
type PGStore struct {
	db database.Querier
}

var _ KeyStore = (*PGStore)(nil)

// NewPGStore returns a store on db.
func NewPGStore(db database.Querier) *PGStore { return &PGStore{db: db} }

const findByPrefixSQL = `
SELECT k.id, k.client_id, c.role, c.active, k.secret_hmac, k.expires_at, k.revoked_at, k.last_used_at
FROM api_keys k
JOIN api_clients c ON c.id = k.client_id
WHERE k.prefix = $1`

// FindByPrefix implements KeyStore.
func (s *PGStore) FindByPrefix(ctx context.Context, prefix string) (KeyRecord, bool, error) {
	var rec KeyRecord
	err := s.db.QueryRow(ctx, findByPrefixSQL, prefix).Scan(
		&rec.KeyID, &rec.ClientID, &rec.Role, &rec.ClientActive, &rec.Hash, &rec.ExpiresAt, &rec.RevokedAt, &rec.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return KeyRecord{}, false, nil
	}
	if err != nil {
		return KeyRecord{}, false, database.Classify(err)
	}
	return rec, true, nil
}

// TouchLastUsed implements KeyStore.
func (s *PGStore) TouchLastUsed(ctx context.Context, keyID string, at time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE api_keys SET last_used_at = $2 WHERE id = $1`, keyID, at)
	return err
}
