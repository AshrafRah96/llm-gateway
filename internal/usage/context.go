package usage

import "context"

type projectContextKey struct{}
type operationContextKey struct{}

func WithProject(ctx context.Context, project string) context.Context {
	return context.WithValue(ctx, projectContextKey{}, project)
}
func Project(ctx context.Context) string { p, _ := ctx.Value(projectContextKey{}).(string); return p }

// Operation is local to one synchronous provider attempt, including its streaming reader.
type Operation struct {
	RequestID string
	ClientID  string
}

func ClientRequestID(ctx context.Context) string {
	if o, ok := ctx.Value(operationContextKey{}).(*Operation); ok {
		return o.ClientID
	}
	return ""
}

func WithOperation(ctx context.Context, o *Operation) context.Context {
	return context.WithValue(ctx, operationContextKey{}, o)
}
func ProviderRequestID(ctx context.Context, id string) {
	if o, ok := ctx.Value(operationContextKey{}).(*Operation); ok {
		if len(id) > 256 {
			id = id[:256]
		}
		o.RequestID = id
	}
}
