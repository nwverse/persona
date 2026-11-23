package channel

import "github.com/nwverse/persona/server/internal/mdsplit"

// Split cuts long Markdown into pieces of at most n runes (mdsplit.Split).
func Split(s string, n int) []string { return mdsplit.Split(s, n) }
