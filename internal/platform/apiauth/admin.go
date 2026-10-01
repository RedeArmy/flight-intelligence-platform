package apiauth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/security"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/id"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

// Codes returned by Admin.
const (
	CodeClientExists   = "CLIENT_EXISTS"
	CodeClientNotFound = "CLIENT_NOT_FOUND"
	CodeKeyNotFound    = "KEY_NOT_FOUND"
	CodeInvalidRole    = "INVALID_ROLE"
	CodeInvalidName    = "INVALID_NAME"
	CodeInvalidTTL     = "INVALID_TTL"
)

// Audit actions written by Admin (audit_events.action).
const (
	ActionClientCreated = "client.created"
	ActionKeyIssued     = "key.issued"
	ActionKeyRevoked    = "key.revoked"
)

const (
	maxNameLength = 128
	actorKeyctl   = "keyctl"
)

var validRoles = map[httpserver.Role]bool{
	httpserver.RoleUser: true, httpserver.RoleDeveloper: true, httpserver.RoleOperator: true,
	httpserver.RoleAdmin: true, httpserver.RoleService: true,
}

// TxRunner runs fn in a transaction. *database.Pool implements it.
type TxRunner interface {
	WithTx(ctx context.Context, opts pgx.TxOptions, fn func(tx pgx.Tx) error) error
}

// Admin manages clients and keys. It runs as the fip_admin role (cmd/keyctl) and writes an audit event in the same
// transaction as every change, so a change without its audit row cannot exist (SR-12).
type Admin struct {
	db      TxRunner
	hasher  *security.KeyHasher
	clock   clock.Nower
	entropy io.Reader
}

// NewAdmin returns an Admin that reads randomness from crypto/rand.
func NewAdmin(db TxRunner, hasher *security.KeyHasher, now clock.Nower) *Admin {
	return &Admin{db: db, hasher: hasher, clock: now, entropy: rand.Reader}
}

// WithEntropy replaces the randomness source (tests only).
func (a *Admin) WithEntropy(r io.Reader) *Admin { a.entropy = r; return a }

// Client is an API client.
type Client struct {
	ID   string
	Name string
	Role httpserver.Role
}

// IssuedKey is returned once, at creation. Token is the only time the full key is available.
type IssuedKey struct {
	KeyID     string
	Prefix    string
	Token     secret.Secret
	ExpiresAt *time.Time
}

// CreateClient registers a client with a role.
func (a *Admin) CreateClient(ctx context.Context, name string, role httpserver.Role) (Client, error) {
	if name == "" || len(name) > maxNameLength {
		return Client{}, sharederrors.Invalid(CodeInvalidName, "client names have 1 to 128 characters")
	}
	if !validRoles[role] {
		return Client{}, sharederrors.Invalid(CodeInvalidRole, "unknown role")
	}
	now := a.clock.Now()
	clientID, err := id.NewV7(now, a.entropy)
	if err != nil {
		return Client{}, err
	}
	err = a.db.WithTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO api_clients (id, name, role, created_at) VALUES ($1, $2, $3, $4)`, clientID, name, string(role), now); err != nil {
			return err
		}
		return a.audit(ctx, tx, now, ActionClientCreated, "api_client", clientID, map[string]any{"name": name, "role": string(role)})
	})
	if err != nil {
		if sharederrors.CodeOf(database.Classify(err)) == database.CodeDBConflict {
			return Client{}, sharederrors.Conflict(CodeClientExists, "a client with this name already exists")
		}
		return Client{}, database.Classify(err)
	}
	return Client{ID: clientID, Name: name, Role: role}, nil
}

// IssueKey creates a key for the named client. A zero ttl means the key does not expire.
func (a *Admin) IssueKey(ctx context.Context, clientName string, ttl time.Duration) (IssuedKey, error) {
	if ttl < 0 {
		return IssuedKey{}, sharederrors.Invalid(CodeInvalidTTL, "the key lifetime must not be negative")
	}
	generated, err := a.hasher.Generate(a.entropy)
	if err != nil {
		return IssuedKey{}, err
	}
	now := a.clock.Now()
	keyID, err := id.NewV7(now, a.entropy)
	if err != nil {
		return IssuedKey{}, err
	}
	var expires *time.Time
	if ttl > 0 {
		e := now.Add(ttl)
		expires = &e
	}
	err = a.db.WithTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var clientID string
		if err := tx.QueryRow(ctx, `SELECT id FROM api_clients WHERE name = $1`, clientName).Scan(&clientID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO api_keys (id, client_id, prefix, secret_hmac, created_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6)`,
			keyID, clientID, generated.Prefix, generated.Hash, now, expires); err != nil {
			return err
		}
		return a.audit(ctx, tx, now, ActionKeyIssued, "api_key", keyID,
			map[string]any{"client": clientName, "prefix": generated.Prefix, "expires": expires != nil})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return IssuedKey{}, sharederrors.NotFound(CodeClientNotFound, "client not found")
	}
	if err != nil {
		return IssuedKey{}, database.Classify(err)
	}
	return IssuedKey{KeyID: keyID, Prefix: generated.Prefix, Token: generated.Token, ExpiresAt: expires}, nil
}

// RevokeKey revokes the key with the given prefix. Revoking an already revoked key keeps its first revocation time.
func (a *Admin) RevokeKey(ctx context.Context, prefix string) error {
	now := a.clock.Now()
	err := a.db.WithTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var keyID string
		err := tx.QueryRow(ctx,
			`UPDATE api_keys SET revoked_at = COALESCE(revoked_at, $2) WHERE prefix = $1 RETURNING id`, prefix, now).Scan(&keyID)
		if err != nil {
			return err
		}
		return a.audit(ctx, tx, now, ActionKeyRevoked, "api_key", keyID, map[string]any{"prefix": prefix})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sharederrors.NotFound(CodeKeyNotFound, "key not found")
	}
	if err != nil {
		return database.Classify(err)
	}
	return nil
}

// KeyInfo describes a key without any secret material.
type KeyInfo struct {
	Prefix    string
	Client    string
	Role      string
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

const listKeysSQL = `
SELECT k.prefix, c.name, c.role, k.created_at, k.expires_at, k.revoked_at
FROM api_keys k JOIN api_clients c ON c.id = k.client_id
ORDER BY k.created_at DESC`

// ListKeys returns every key, newest first.
func (a *Admin) ListKeys(ctx context.Context) ([]KeyInfo, error) {
	var out []KeyInfo
	err := a.db.WithTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		out = out[:0] // a retried transaction starts again
		rows, err := tx.Query(ctx, listKeysSQL)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k KeyInfo
			if err := rows.Scan(&k.Prefix, &k.Client, &k.Role, &k.CreatedAt, &k.ExpiresAt, &k.RevokedAt); err != nil {
				return err
			}
			out = append(out, k)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, database.Classify(err)
	}
	return out, nil
}

// execer is the part of pgx.Tx that audit needs.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (a *Admin) audit(ctx context.Context, tx execer, now time.Time, action, resourceType, resourceID string, details map[string]any) error {
	auditID, err := id.NewV7(now, a.entropy)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_events (id, occurred_at, actor_label, action, resource_type, resource_id, outcome, details)
		 VALUES ($1, $2, $3, $4, $5, $6, 'success', $7)`,
		auditID, now, actorKeyctl, action, resourceType, resourceID, details)
	if err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}
