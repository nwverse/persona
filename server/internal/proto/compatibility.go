package proto

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// Both keys carry the same value on all hub/node/manager boundaries. Old peers
// ignore the canonical key. Presence, not boolean truth, gives it precedence.
type sessionJSON Session

// sessionWire is a Session as sent: its fields, then the legacy key. The session is embedded by pointer, so encoding
// one copies none of its ~1 KB of fields.
type sessionWire struct {
	*sessionJSON
	LegacyStarted bool `json:"started_by_jarvis"`
}

func (s Session) MarshalJSON() ([]byte, error) {
	return json.Marshal(&sessionWire{(*sessionJSON)(&s), s.StartedByPersona})
}

// MarshalJSONTo writes MarshalJSON's bytes straight into the encoder that asked: the hub encodes every session of a
// list on each push, and MarshalJSON's bytes were then checked and copied again by that encoder. On a pointer, so the
// session is not copied either; encoding/json calls it for an addressable session (a pointer, a slice element) and
// MarshalJSON for the rest. Encoded with encoding/json's options, as MarshalJSON is, so an encoder that does not
// escape HTML still gets it escaped; the caller's indentation applies, as it did to MarshalJSON's output.
func (s *Session) MarshalJSONTo(enc *jsontext.Encoder) error {
	return jsonv2.MarshalEncode(enc, &sessionWire{(*sessionJSON)(s), s.StartedByPersona}, json.DefaultOptionsV1())
}
func (s *Session) UnmarshalJSON(data []byte) error {
	v := struct {
		sessionJSON
		LegacyStarted  json.RawMessage `json:"started_by_jarvis"`
		CurrentStarted json.RawMessage `json:"started_by_persona"`
	}{sessionJSON: sessionJSON(*s)}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	raw := v.LegacyStarted
	if len(v.CurrentStarted) > 0 {
		raw = v.CurrentStarted
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v.sessionJSON.StartedByPersona); err != nil {
			return err
		}
	}
	*s = Session(v.sessionJSON)
	return nil
}

// Only the managed-launcher enum is projected; focus-source IDs and arbitrary
// extension source strings do not pass through this adapter.
const legacyLauncherSource = "jarvis"

// Managed launcher source is a wire enum, independent of the MCP namespace.
func CanonicalLauncherSource(value string) string {
	if value == legacyLauncherSource {
		return "persona"
	}
	return value
}
func LegacyLauncherSource(value string) string {
	if value == "persona" {
		return legacyLauncherSource
	}
	return value
}

// LauncherEnvelope is an explicit transport projection. Launcher itself has no
// JSON methods: embedding it must not hide health, agent-type or other siblings.
type LauncherEnvelope Launcher

func (v LauncherEnvelope) MarshalJSON() ([]byte, error) {
	copy := Launcher(v)
	copy.Source = LegacyLauncherSource(copy.Source)
	return json.Marshal(copy)
}
func (v *LauncherEnvelope) UnmarshalJSON(raw []byte) error {
	copy := Launcher(*v)
	if err := json.Unmarshal(raw, &copy); err != nil {
		return err
	}
	copy.Source = CanonicalLauncherSource(copy.Source)
	*v = LauncherEnvelope(copy)
	return nil
}
