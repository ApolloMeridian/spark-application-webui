package domain

import "context"

type tenantContextKey struct{}

// Empty scope is reserved for internal maintenance. HTTP requests always set it.
func WithTenant(ctx context.Context, tenant string) context.Context {
	if tenant == "" {
		tenant = "default"
	}
	return context.WithValue(ctx, tenantContextKey{}, tenant)
}
func TenantScope(ctx context.Context) string {
	value, _ := ctx.Value(tenantContextKey{}).(string)
	return value
}
func WriteTenant(ctx context.Context) string {
	if value := TenantScope(ctx); value != "" {
		return value
	}
	return "default"
}
