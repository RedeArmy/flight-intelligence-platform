package httpserver

import (
	"context"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver/openapi"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

// api implements the generated openapi.StrictServerInterface. Handlers only translate between the contract and
// the platform; business behaviour lives in application services.
type api struct {
	health *Health
}

var _ openapi.StrictServerInterface = (*api)(nil)

func (a *api) GetHealthz(context.Context, openapi.GetHealthzRequestObject) (openapi.GetHealthzResponseObject, error) {
	return openapi.GetHealthz200JSONResponse(openapi.Liveness{Status: openapi.Ok}), nil
}

func (a *api) GetReadyz(ctx context.Context, _ openapi.GetReadyzRequestObject) (openapi.GetReadyzResponseObject, error) {
	state, checks := a.health.Readiness(ctx)
	body := openapi.Readiness{Status: openapi.ReadinessStatus(state)}
	if len(checks) > 0 {
		body.Checks = &checks
	}
	if state == StateNotReady {
		return openapi.GetReadyz503JSONResponse(body), nil
	}
	return openapi.GetReadyz200JSONResponse(body), nil
}

func (a *api) GetWhoami(ctx context.Context, _ openapi.GetWhoamiRequestObject) (openapi.GetWhoamiResponseObject, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		// The policy middleware guarantees a principal on protected routes; reaching here is a wiring bug.
		return nil, sharederrors.Internal(sharederrors.CodeInternal, "internal error")
	}
	id := logging.RequestID(ctx)
	return openapi.GetWhoami200JSONResponse{
		Body:    openapi.Whoami{ClientId: p.ClientID, Role: openapi.WhoamiRole(p.Role)},
		Headers: openapi.GetWhoami200ResponseHeaders{XRequestId: &id},
	}, nil
}
