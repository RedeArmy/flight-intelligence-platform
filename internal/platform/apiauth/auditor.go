package apiauth

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/id"
)

// PGAuditor writes audit events as the runtime role, which may insert into audit_events and nothing else there
// (append-only, INV-3). It is used only for events that need a valid key to trigger, so callers cannot flood it
// without first passing authentication and the client rate limit.
type PGAuditor struct {
	db    database.Querier
	clock clock.Nower
}

var _ httpserver.Auditor = (*PGAuditor)(nil)

// NewPGAuditor returns an auditor on db.
func NewPGAuditor(db database.Querier, now clock.Nower) *PGAuditor {
	return &PGAuditor{db: db, clock: now}
}

// auditWriteTimeout bounds the write so a slow database cannot hold a request open for long.
const auditWriteTimeout = 2 * time.Second

// Record implements httpserver.Auditor.
func (a *PGAuditor) Record(ctx context.Context, e httpserver.AuditEvent) error {
	now := a.clock.Now()
	eventID, err := id.NewV7(now, rand.Reader)
	if err != nil {
		return err
	}
	// The response is already decided; a cancelled request must still leave its audit row.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditWriteTimeout)
	defer cancel()
	_, err = a.db.Exec(ctx,
		`INSERT INTO audit_events (id, occurred_at, actor_client_id, action, resource_type, resource_id, outcome, request_id)
		 VALUES ($1, $2, $3, $4, 'route', $5, $6, $7)`,
		eventID, now, e.ClientID, e.Action, e.Resource, e.Outcome, e.RequestID)
	if err != nil {
		return fmt.Errorf("write audit event: %w", database.Classify(err))
	}
	return nil
}
