package panel

import (
	"context"

	"github.com/YOUR-ORG/ike/agent/internal/panel/store"
)

// checkPackageLimits enforces hosting package quotas before a request reaches
// the agent (filled in with hosting packages).
func (s *Server) checkPackageLimits(ctx context.Context, p *store.Person, method, apiPath string, body []byte) error {
	return nil
}
