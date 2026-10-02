package api

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// What an API key may see and do beyond the endpoint it is let into (apikeys.go keeps the
// keys themselves).

// requireSession refuses an API key on a change of what a leaked "full" key must not be
// able to turn against the admin: where clients are sent, the secret paths. The change
// is one field of an endpoint a key may otherwise call, so the check is by field; field
// names it in the answer.
func requireSession(ctx context.Context, field string) error {
	if _, ok := apiKeyOf(ctx); ok {
		return huma.Error403Forbidden("session_only", &huma.ErrorDetail{Location: "body." + field, Message: "session_only"})
	}
	return nil
}

// hidesSecrets: a "read" key may look at the panel, not take it over. What would hand the
// reader other people's credentials or the way into the admin (a subscription link, the
// secret paths) stays out of its answers.
func hidesSecrets(ctx context.Context) bool {
	k, ok := apiKeyOf(ctx)
	return ok && k.Scope != "full"
}
