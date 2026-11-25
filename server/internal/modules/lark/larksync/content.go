package larksync

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Content bounds.
const (
	bodyMost          = 200 << 10 // a document's Markdown handed over
	transcriptMost    = 4 << 20   // a transcript downloaded (then bounded to bodyMost)
	docxBlockPages    = 20        // 10 000 blocks
	sheetsMost        = 10        // sheets of one spreadsheet
	sheetRange        = "A1:T200"
	bitableTablesMost = 10
	bitableRowsMost   = 200
)

// docCandidate is a document a listing found: what is known before reading it.
type docCandidate struct {
	objToken, objType string
	title, url        string
	alias             string // a wiki node token
	edited            int64  // last edit, seconds
}

// consider reads a listed document's content when the index says it is new or newer, and records the sighting
// either way. The revision is the docx revision_id (one cheap call) or the last edit time; a body that cannot be read
// for a reason that will not pass (no permission on that one document, an unsupported kind) is kept as its title and
// link at revision 0, so a later readable revision replaces it.
func (r *run) consider(ctx context.Context, resource, entry string, c docCandidate) error {
	key := larksyncwire.KeyDoc + c.objToken
	st := r.docs[key]
	if st == nil {
		st = &docState{kind: recDoc, doc: larksyncwire.Doc{Key: key, Kind: "docs", ObjToken: c.objToken, ObjType: c.objType, Revision: -1}}
		r.docs[key] = st
		r.docOrder = append(r.docOrder, key)
	}
	st.merge(c)
	rev := c.edited
	if c.objType == "docx" {
		if !st.hasMeta {
			meta, err := r.docxMeta(ctx, c.objToken)
			switch {
			case err == nil:
				st.metaRev = meta.revision
				if st.doc.Title == "" {
					st.doc.Title = meta.title
				}
			case passing(err):
				return err
			}
			st.hasMeta = true // an unreadable one is revision 0: its title and link only
		}
		rev = st.metaRev
	}
	if st.fetched && st.doc.Revision >= rev {
		st.sightings = append(st.sightings, sighting{resource: resource, ref: r.ref(entry), revision: st.doc.Revision})
		return nil
	}
	if err := r.lookup(ctx, key); err != nil {
		return err
	}
	known := r.known[key]
	switch larksyncwire.Decide(known.rec, known.ok, rev, "") {
	case larksyncwire.DecideSame, larksyncwire.DecideStale:
		st.sightings = append(st.sightings, sighting{resource: resource, ref: r.ref(entry), revision: rev})
		return nil
	}
	body, err := r.body(ctx, c.objType, c.objToken)
	switch {
	case err == nil:
	case passing(err):
		return err
	default:
		body, rev = "", 0
	}
	st.setContent(rev, body, c.edited)
	st.sightings = append(st.sightings, sighting{resource: resource, ref: r.ref(entry), revision: rev})
	r.findLeads(resource, entry, body)
	return nil
}

// merge adds what a listing knows of the document (a title, a link, an alias) to what is known already.
func (st *docState) merge(c docCandidate) {
	d := &st.doc
	if d.Title == "" {
		d.Title = c.title
	}
	if d.URL == "" || strings.Contains(d.URL, "/wiki/") && c.alias == "" && c.url != "" {
		d.URL = cmpString(c.url, d.URL) // a document's own link is preferred over a wiki node's
	}
	if c.alias != "" && !containsString(d.Aliases, c.alias) {
		d.Aliases = append(d.Aliases, c.alias)
	}
	if c.edited > 0 {
		d.At = maxTime(d.At, time.Unix(c.edited, 0).UTC())
	}
}

// setContent keeps body as the document's content at rev.
func (st *docState) setContent(rev int64, body string, edited int64) {
	body = bound(body, bodyMost)
	st.doc.Revision, st.doc.Body, st.fetched = rev, body, true
	st.doc.BodyHash = ""
	if body != "" {
		st.doc.BodyHash = hashOf(body)
	}
	if edited > 0 {
		st.doc.At = maxTime(st.doc.At, time.Unix(edited, 0).UTC())
	}
}

// passing: an error that fails the resource rather than only one document (a refused token, a missing scope, a rate
// limit or a server failure): the document is tried again next run.
func passing(err error) bool {
	if fatal(err) {
		return true
	}
	e := asSyncError(err)
	if e == nil {
		return true
	}
	switch e.Kind {
	case larksyncwire.ErrKindScope, larksyncwire.ErrKindRateLimited, larksyncwire.ErrKindTransient:
		return true
	}
	return false
}

type docxInfo struct {
	revision int64
	title    string
}

func (r *run) docxMeta(ctx context.Context, id string) (docxInfo, error) {
	var data struct {
		Document struct {
			RevisionID int64  `json:"revision_id"`
			Title      string `json:"title"`
		} `json:"document"`
	}
	err := r.sess.getJSON(ctx, request{family: famDocx, op: "docx document", path: "/open-apis/docx/v1/documents/" + pathEsc(id)}, &data)
	return docxInfo{revision: data.Document.RevisionID, title: data.Document.Title}, err
}

// body is a document's content as Markdown by its type; types the sync does not read into text are their title and
// link only (an empty body).
func (r *run) body(ctx context.Context, objType, token string) (string, error) {
	switch objType {
	case "docx":
		md, err := r.docxBody(ctx, token)
		if err == nil || passing(err) {
			return md, err
		}
		var raw struct {
			Content string `json:"content"`
		}
		if err := r.sess.getJSON(ctx, request{family: famDocx, op: "docx raw content", path: "/open-apis/docx/v1/documents/" + pathEsc(token) + "/raw_content"}, &raw); err != nil {
			return "", err
		}
		return raw.Content, nil
	case "sheet":
		return r.sheetBody(ctx, token)
	case "bitable":
		return r.bitableBody(ctx, token)
	}
	return "", nil
}

func (r *run) docxBody(ctx context.Context, id string) (string, error) {
	var blocks []docxBlock
	tok := ""
	for range docxBlockPages {
		var data struct {
			page
			Items []docxBlock `json:"items"`
		}
		q := map[string]string{"page_size": "500", "document_revision_id": "-1"}
		if tok != "" {
			q["page_token"] = tok
		}
		if err := r.sess.getJSON(ctx, request{family: famDocx, op: "docx blocks", path: "/open-apis/docx/v1/documents/" + pathEsc(id) + "/blocks", query: q}, &data); err != nil {
			return "", err
		}
		blocks = append(blocks, data.Items...)
		if !data.HasMore || data.PageToken == "" {
			break
		}
		tok = data.PageToken
	}
	return renderDocx(id, blocks), nil
}

func (r *run) sheetBody(ctx context.Context, token string) (string, error) {
	var data struct {
		Sheets []struct {
			SheetID      string `json:"sheet_id"`
			Title        string `json:"title"`
			ResourceType string `json:"resource_type"`
		} `json:"sheets"`
	}
	base := "/open-apis/sheets/v3/spreadsheets/" + pathEsc(token)
	if err := r.sess.getJSON(ctx, request{family: famSheets, op: "list sheets", path: base + "/sheets/query"}, &data); err != nil {
		return "", err
	}
	var b strings.Builder
	n := 0
	for _, sh := range data.Sheets {
		if sh.ResourceType != "" && sh.ResourceType != "sheet" {
			continue // an embedded table of another kind
		}
		if n == sheetsMost {
			break
		}
		n++
		var vals struct {
			ValueRange struct {
				Values [][]any `json:"values"`
			} `json:"valueRange"`
		}
		path := "/open-apis/sheets/v2/spreadsheets/" + pathEsc(token) + "/values/" + pathEsc(sh.SheetID) + "!" + sheetRange
		if err := r.sess.getJSON(ctx, request{family: famSheets, op: "sheet values", path: path, query: map[string]string{"valueRenderOption": "ToString"}}, &vals); err != nil {
			return "", err
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## " + cmpString(sh.Title, sh.SheetID) + "\n\n")
		b.WriteString(markdownTable(vals.ValueRange.Values))
		if b.Len() > bodyMost {
			break
		}
	}
	return b.String(), nil
}

func (r *run) bitableBody(ctx context.Context, token string) (string, error) {
	base := "/open-apis/bitable/v1/apps/" + pathEsc(token) + "/tables"
	var tables struct {
		Items []struct {
			TableID string `json:"table_id"`
			Name    string `json:"name"`
		} `json:"items"`
	}
	if err := r.sess.getJSON(ctx, request{family: famBitable, op: "list tables", path: base, query: map[string]string{"page_size": "100"}}, &tables); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, t := range tables.Items {
		if i == bitableTablesMost {
			break
		}
		var rows []map[string]any
		tok := ""
		for len(rows) < bitableRowsMost {
			var data struct {
				page
				Items []struct {
					Fields map[string]any `json:"fields"`
				} `json:"items"`
			}
			q := map[string]string{"page_size": "100"}
			if tok != "" {
				q["page_token"] = tok
			}
			if err := r.sess.getJSON(ctx, request{family: famBitable, op: "list records", path: base + "/" + pathEsc(t.TableID) + "/records", query: q}, &data); err != nil {
				return "", err
			}
			for _, it := range data.Items {
				if len(rows) < bitableRowsMost {
					rows = append(rows, it.Fields)
				}
			}
			if !data.HasMore || data.PageToken == "" {
				break
			}
			tok = data.PageToken
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## " + cmpString(t.Name, t.TableID) + "\n\n")
		b.WriteString(recordsTable(rows))
		if b.Len() > bodyMost {
			break
		}
	}
	return b.String(), nil
}

// readMinutes reads the minutes this run found linked or listed (resource "minutes"): the ones the index holds
// already only gain the source; the others are read with their transcript.
func (r *run) readMinutes(ctx context.Context) error {
	const resource = larksyncwire.ResMinutes
	var leads []minuteLead
	seen := map[string]bool{}
	for _, l := range r.leads {
		if l.token == "" || seen[l.token] || !r.valid(l.resource) {
			continue
		}
		seen[l.token] = true
		leads = append(leads, l)
	}
	if len(leads) == 0 {
		return nil
	}
	keys := make([]string, len(leads))
	for i, l := range leads {
		keys[i] = larksyncwire.KeyMinute + l.token
	}
	if err := r.lookup(ctx, keys...); err != nil {
		return r.settle(ctx, resource, err)
	}
	for i, l := range leads {
		if r.overBudget() {
			r.partial = true
			return nil
		}
		if err := r.readMinute(ctx, resource, keys[i], l); err != nil {
			return r.settle(ctx, resource, err)
		}
	}
	return nil
}

func (r *run) readMinute(ctx context.Context, resource, key string, l minuteLead) error {
	st := r.docs[key]
	if st == nil {
		st = &docState{kind: recMinute, doc: larksyncwire.Doc{Key: key, Kind: "meetings", ObjToken: l.token, ObjType: "minutes", URL: l.url, Revision: -1}}
		r.docs[key] = st
		r.docOrder = append(r.docOrder, key)
	}
	if known := r.known[key]; known.ok {
		// A minute does not change once made: the index's revision stands, only the source is new.
		st.sightings = append(st.sightings, sighting{resource: resource, ref: r.ref(l.entry), revision: known.rec.Revision})
		return nil
	}
	var data struct {
		Minute struct {
			Title      string `json:"title"`
			CreateTime string `json:"create_time"` // milliseconds
			URL        string `json:"url"`
		} `json:"minute"`
	}
	err := r.sess.getJSON(ctx, request{family: famMinutes, op: "get minute", path: "/open-apis/minutes/v1/minutes/" + pathEsc(l.token)}, &data)
	if err != nil {
		if passing(err) {
			return err
		}
		return nil // not the owner's to read: skipped
	}
	created := parseUnix(data.Minute.CreateTime)
	rev := created / 1000
	if created > 0 && created < 1e11 { // seconds after all
		rev = created
	}
	st.doc.Title = cmpString(st.doc.Title, data.Minute.Title)
	st.doc.URL = cmpString(data.Minute.URL, st.doc.URL)
	if rev > 0 {
		st.doc.At = time.Unix(rev, 0).UTC()
	}
	raw, _, err := r.sess.download(ctx, request{family: famMinutes, op: "minute transcript", path: "/open-apis/minutes/v1/minutes/" + pathEsc(l.token) + "/transcript", query: map[string]string{"need_speaker": "true", "file_format": "txt"}}, transcriptMost)
	body := ""
	switch {
	case err == nil:
		body = strings.TrimSpace(string(raw))
	case passing(err):
		return err
	default:
		rev = 0 // the title only; a readable transcript replaces it later
	}
	st.setContent(rev, body, 0)
	st.sightings = append(st.sightings, sighting{resource: resource, ref: r.ref(l.entry), revision: rev})
	return nil
}

// findLeads keeps the minutes linked in text, found in resource through entry.
func (r *run) findLeads(resource, entry, text string) {
	if !strings.Contains(text, "/minutes/") {
		return
	}
	for _, u := range linkPattern.FindAllString(text, -1) {
		if kind, tok := TokenFromURL(u); kind == "minutes" {
			r.leads = append(r.leads, minuteLead{token: tok, url: strings.TrimRight(u, ".,;:!?)]}>\"'"), resource: resource, entry: entry})
		}
	}
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// formatNumber prints a cell's number without a needless ".0".
func formatNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
