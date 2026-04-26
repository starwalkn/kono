package kono

import "context"

type contextKey uint8

const (
	contextKeyClientIP contextKey = iota
	contextKeyRequestID
	contextKeyRoute
)

func withClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, contextKeyClientIP, ip)
}

func clientIPFromContext(ctx context.Context) string {
	ip, _ := ctx.Value(contextKeyClientIP).(string)
	return ip
}

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKeyRequestID, id)
}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKeyRequestID).(string)
	return id
}

func withRoute(ctx context.Context, route string) context.Context {
	return context.WithValue(ctx, contextKeyRoute, route)
}

func routeFromContext(ctx context.Context) string {
	route, _ := ctx.Value(contextKeyRoute).(string)
	return route
}
