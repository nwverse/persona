package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestShareQuotaAndHeadersNeverLoadPublicationBodies(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	const issuer = "synthetic-header-issuer"
	const subject = "80000000-0000-4000-8000-000000000001"
	body, _ := json.Marshal(map[string]string{"synthetic_large_content": strings.Repeat("x", 2<<20)})
	now := time.Now()
	if err := d.ShareTransaction(ctx, func(tx *ShareTx) error {
		for _, row := range []ShareRow{
			{ID: "synthetic-header-active", Hash: strings.Repeat("a", 64), Expires: now.Add(time.Hour), Active: true},
			{ID: "synthetic-header-revoked", Hash: strings.Repeat("b", 64), Expires: now.Add(time.Hour)},
			{ID: "synthetic-header-expired", Hash: strings.Repeat("c", 64), Expires: now.Add(-time.Hour), Active: true},
		} {
			row.Issuer, row.Subject = issuer, subject
			row.Machine, row.Session = "synthetic-machine", "synthetic-session"
			row.Body, row.Header = body, json.RawMessage(`{"synthetic_metadata_only":true}`)
			if err := tx.SaveShare(row); err != nil {
				return err
			}
		}
		retained, active, err := tx.ShareCounts(issuer, subject, now)
		if err != nil {
			return err
		}
		if retained != 3 || active != 1 {
			t.Errorf("quota counts = %d/%d, want 3/1", retained, active)
		}
		headers, err := tx.ShareHeaders(issuer, subject)
		if err != nil {
			return err
		}
		if len(headers) != 3 {
			t.Errorf("listed %d headers", len(headers))
		}
		for _, row := range headers {
			if len(row.Body) != 0 || len(row.Header) > 128 {
				t.Error("metadata query fetched a publication body")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
