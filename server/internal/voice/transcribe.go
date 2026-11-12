package voice

import "net/http"

// Meeting transcription (#3862 phase 5, docs/assistant/meeting-minutes.md "The `transcription` extension point" and
// "The built-in adapter"): the built-in voice extension's transcription contribution, on its own socket under
// /transcription/{cid}/, apart from the voice point's /info and /ws.

// TranscriptionContribution is the built-in voice extension's transcription contribution's id.
const TranscriptionContribution = "gpt-live"

// transcriptionRoutes adds the transcription point's routes, GET /transcription/{cid}/info and
// GET /transcription/{cid}/ws, to the plugin's API.
func (pl *Plugin) transcriptionRoutes(mux *http.ServeMux) {}
