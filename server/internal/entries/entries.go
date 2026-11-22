// Package entries defines the unified ledger's stored and public contract.
package entries

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// Source reserves message, card and repository pointers for later quote verification.
type Source struct {
	Machine string `json:"machine"`
	Session string `json:"session"`
	Message string `json:"msg_uuid,omitempty"`
	Card    string `json:"card_id,omitempty"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
}

type Scope struct {
	Executor string   `json:"executor"`
	Machine  []string `json:"machine"`
	Project  []string `json:"project"`
	Personal bool     `json:"personal"`
}

// Decision keeps unknown reversibility distinct from an explicit no.
type Decision struct {
	Options    []string `json:"options"`
	Selected   []string `json:"selected"`
	Rejected   []string `json:"rejected"`
	Reason     string   `json:"reason"`
	Reversible *bool    `json:"reversible"`
}

type Entry struct {
	OriginSpace string     `json:"origin_space"`
	Category    []string   `json:"category"`
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Text        string     `json:"text"`
	Quote       string     `json:"quote"`
	Source      Source     `json:"source"`
	RelayedVia  []string   `json:"relayed_via"`
	Scope       Scope      `json:"scope"`
	At          time.Time  `json:"at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	Supersedes  string     `json:"supersedes"`
	Trust       string     `json:"trust"`
	Status      string     `json:"status"`
	By          string     `json:"by"`
	Tag         []string   `json:"tag"`
	Decision    *Decision  `json:"decision,omitempty"`
	// ExtID is reserved for future per-publisher idempotency; H1a exposes no write RPC.
	ExtID string `json:"ext_id,omitempty"`
}

// ScopeFilter matches all specified dimensions; empty machine/project constraints on
// an entry apply everywhere. Personal is a filter, never a permission grant.
type ScopeFilter struct {
	Executor string   `json:"executor,omitempty"`
	Machine  []string `json:"machine,omitempty"`
	Project  []string `json:"project,omitempty"`
	Personal *bool    `json:"personal,omitempty"`
}

type Params struct {
	After  string       `json:"after,omitempty"`
	Limit  int          `json:"limit,omitempty"`
	By     string       `json:"by,omitempty"`
	Trust  string       `json:"trust,omitempty"`
	Status string       `json:"status,omitempty"`
	Scope  *ScopeFilter `json:"scope,omitempty"`
	Tag    string       `json:"tag,omitempty"`
	Type   string       `json:"type,omitempty"`
}

type Result struct {
	Entries []Entry `json:"entries"`
	After   string  `json:"after"`
	HasMore bool    `json:"has_more"`
}

type Cursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func EncodeCursor(e Entry) string {
	b, _ := json.Marshal(Cursor{At: e.At, ID: e.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}
func DecodeCursor(s string) (Cursor, error) {
	var c Cursor
	if s == "" {
		return c, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) > 1024 || json.Unmarshal(b, &c) != nil || c.At.IsZero() || c.ID == "" {
		return c, errors.New("invalid after cursor")
	}
	return c, nil
}
func (p *Params) Validate() error {
	if p.Limit < 0 || p.Limit > 200 {
		return errors.New("limit must be between 0 and 200")
	}
	if p.Limit == 0 {
		p.Limit = 50
	}
	if _, err := DecodeCursor(p.After); err != nil {
		return err
	}
	for _, v := range []struct {
		value   string
		allowed []string
	}{
		{p.By, []string{"user", "agent", "enkeep", "import"}},
		{p.Trust, []string{"user_verbatim", "user_relayed", "repo", "agent", "inferred"}},
		{p.Status, []string{"active", "superseded", "expired", "wrong", "all"}},
		{p.Type, []string{"fact", "preference", "rule", "decision"}},
	} {
		if v.value != "" && !slices.Contains(v.allowed, v.value) {
			return errors.New("invalid ledger filter")
		}
	}
	if p.Scope != nil && p.Scope.Executor != "" && !slices.Contains([]string{"system", "assistant", "any"}, p.Scope.Executor) {
		return errors.New("invalid scope executor")
	}
	return nil
}

func (e *Entry) Normalize() {
	if e.ExpiresAt != nil {
		at := e.ExpiresAt.UTC()
		e.ExpiresAt = &at
	}
	if e.Category == nil {
		e.Category = []string{}
	}
	if e.RelayedVia == nil {
		e.RelayedVia = []string{}
	}
	if e.Tag == nil {
		e.Tag = []string{}
	}
	if e.Scope.Machine == nil {
		e.Scope.Machine = []string{}
	}
	if e.Scope.Project == nil {
		e.Scope.Project = []string{}
	}
	if e.Decision != nil {
		if e.Decision.Options == nil {
			e.Decision.Options = []string{}
		}
		if e.Decision.Selected == nil {
			e.Decision.Selected = []string{}
		}
		if e.Decision.Rejected == nil {
			e.Decision.Rejected = []string{}
		}
	}
}
func (e Entry) Validate() error {
	p := Params{By: e.By, Trust: e.Trust, Status: e.Status, Type: e.Type}
	if p.Validate() != nil || e.ID == "" || len(e.ID) > 200 || len(e.Text) > 16000 || len(e.Quote) > 64000 || strings.TrimSpace(e.Text) == "" || e.Type == "" || e.By == "" || e.Trust == "" || !slices.Contains([]string{"active", "superseded", "wrong"}, e.Status) {
		return errors.New("invalid ledger entry")
	}
	if !slices.Contains([]string{"system", "assistant", "any"}, e.Scope.Executor) || e.Scope.Executor == "any" && !e.Scope.Personal {
		return errors.New("invalid entry scope")
	}
	if e.Supersedes == e.ID {
		return errors.New("an entry cannot supersede itself")
	}
	if e.By == "user" && (e.Quote == "" || e.Source.Machine == "" || e.Source.Message == "" && e.Source.Card == "" && e.Source.File == "") {
		return errors.New("user entries require a quote and source")
	}
	if (e.Trust == "user_verbatim" || e.Trust == "user_relayed") && (e.By != "user" || e.Quote == "") {
		return errors.New("user trust requires user provenance")
	}
	if e.Type == "decision" {
		if e.Decision == nil || len(e.Decision.Selected) == 0 {
			return errors.New("decisions require structured choices")
		}
		for _, v := range e.Decision.Selected {
			if !slices.Contains(e.Decision.Options, v) {
				return errors.New("selected choice is not an option")
			}
		}
		for _, v := range e.Decision.Rejected {
			if !slices.Contains(e.Decision.Options, v) || slices.Contains(e.Decision.Selected, v) {
				return errors.New("invalid rejected choice")
			}
		}
	} else if e.Decision != nil {
		return errors.New("decision fields require type decision")
	}
	return nil
}

// Changed is the public invalidation payload, without internal routing metadata.
type Changed struct {
	ID       string `json:"id"`
	RecordID string `json:"record_id"`
	Status   string `json:"status"`
}
