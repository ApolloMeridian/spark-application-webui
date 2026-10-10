package localauth

import (
	"context"
	"spark-control-center/backend/internal/domain"
)

type userAuditKey struct{}

// Only authenticated service calls attach an actor. HTTP bodies cannot set it.
func withAuditActor(ctx context.Context, actor domain.User) context.Context {
	return context.WithValue(ctx, userAuditKey{}, actor)
}

func AuditActor(ctx context.Context) (domain.User, bool) {
	actor, ok := ctx.Value(userAuditKey{}).(domain.User)
	return actor, ok
}
