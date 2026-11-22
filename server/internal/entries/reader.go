package entries

import "context"

type extensionReaderKey struct{}

// WithExtensionReader is transport-only identity: the extension manager sets it
// after checking the caller's live grant. Request JSON cannot assert it.
func WithExtensionReader(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, extensionReaderKey{}, id)
}
func ExtensionReader(ctx context.Context) string {
	id, _ := ctx.Value(extensionReaderKey{}).(string)
	return id
}
