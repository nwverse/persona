package assistantwire

import "context"

// Settings keeps a module's settings. Its method takes any value, so what an adapter writes into it reaches whoever
// handed it over.
type Settings interface {
	PutSetting(ctx context.Context, key string, v any) error
}

// TitleSource looks a session's title up. Its method returns raw text.
type TitleSource interface {
	Title(ref SessionRef) string
}

// RowSink takes the Needs you rows one at a time. Its method takes a contract and returns an error.
type RowSink interface {
	Put(row NeedsYouRow) error
}

// Hooks is what a caller is told through. Its callback takes raw text.
type Hooks struct {
	OnWhy func(why string)
}

// Lookup finds a word by its key, as raw text.
type Lookup func(key string) string
