package transcript

const restartContextThreshold = 300_000

type contextRequest struct {
	id   string
	high bool
}

// observeRequestContext keeps the three latest measured requests. Claude repeats a request across content
// blocks. Codex already deduplicates by the running total and therefore needs no message ID here.
func (m *Meta) observeRequestContext(id string, tokens int64) {
	recent := &m.restartContext
	high := tokens > restartContextThreshold
	if id != "" {
		for i := range recent {
			if recent[i].id == id {
				recent[i].high = recent[i].high || high
				return
			}
		}
	}
	copy(recent[:], recent[1:])
	recent[len(recent)-1] = contextRequest{id: id, high: high}
}

// ContextRestartRecommended reports sustained large input after the most recent compaction.
func (m *Meta) ContextRestartRecommended() bool {
	for _, req := range m.restartContext {
		if !req.high {
			return false
		}
	}
	return true
}
