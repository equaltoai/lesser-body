package mcpapp

import (
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func WithActorBinding(next apptheory.Handler) apptheory.Handler {
	if next == nil {
		return nil
	}

	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		actor := actorFromRequestContext(ctx)
		if actor == "" || !strings.EqualFold(strings.TrimSpace(ctx.AuthIdentity), actor) {
			return nil, &apptheory.AppError{
				Code:    "app.forbidden",
				Message: "forbidden",
			}
		}
		return next(ctx)
	}
}
