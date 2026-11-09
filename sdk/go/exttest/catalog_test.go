package exttest_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

const catalogManifest = `{"manifest_version":1,"id":"examplecatalog","name":"Example Catalog","version":"1.0.0","engines":{"extension_protocol":1},"runtime":{"command":"bin/examplecatalog"},"activation_events":["on_startup"],"contributes":{"connectors":[{"id":"examplecatalog","name":"Example Catalog","capabilities":["catalog"]}]}}`

type catalogRecorder struct {
	testing.TB
	errors []string
}

func (*catalogRecorder) Helper()                     {}
func (r *catalogRecorder) Errorf(f string, a ...any) { r.errors = append(r.errors, f) }

func catalogSDKProgram(catalog wire.ConnectorCatalog) *ext.Program {
	return &ext.Program{ID: "examplecatalog", Version: "1.0.0", Manifest: []byte(catalogManifest), Namespaces: map[string]ext.Handler{
		ext.ConnectorNamespace: ext.Methods{
			"catalog.list": ext.Method(func(context.Context, wire.ConnectorParams) (wire.ConnectorCatalog, error) { return catalog, nil }),
			"catalog.call": ext.Method(func(_ context.Context, p wire.ConnectorCatalogCallParams) (wire.ConnectorCallResult, error) {
				if p.Source != "notices" || p.Tool != "list" {
					return wire.ConnectorCallResult{}, &ext.Error{Code: wire.ConnectorErrorCodeNotReadOnly, Message: "Not a read-only tool"}
				}
				return wire.ConnectorCallResult{Text: string(p.Args)}, nil
			}),
		}.For("examplecatalog"),
	}}
}
func TestCatalogThroughSDKMethodsAndConformance(t *testing.T) {
	source := wire.ConnectorCatalogSource{ID: "notices", Name: "Notices", Tools: []wire.ConnectorCatalogTool{{Name: "list", Description: "Read notices", InputSchema: json.RawMessage(`{"type":"object"}`), ReadOnly: true}}}
	catalog := wire.ConnectorCatalog{Sources: []wire.ConnectorCatalogSource{source}}
	h := exttest.CheckConnectors(t, []byte(catalogManifest), exttest.Serve(t, *catalogSDKProgram(catalog)))
	var out wire.ConnectorCallResult
	if err := h.Call("connector.catalog.call", wire.ConnectorCatalogCallParams{Contribution: "examplecatalog", Source: "notices", Tool: "list", Args: json.RawMessage(`{"pageToken":"next"}`)}, &out); err != nil || out.Text != `{"pageToken":"next"}` {
		t.Fatalf("%+v %v", out, err)
	}
	for _, bad := range []string{"read_only", "duplicate", "schema"} {
		t.Run(bad, func(t *testing.T) {
			var c wire.ConnectorCatalog
			raw, _ := json.Marshal(catalog)
			_ = json.Unmarshal(raw, &c)
			switch bad {
			case "read_only":
				c.Sources[0].Tools[0].ReadOnly = false
			case "duplicate":
				c.Sources[0].Tools = append(c.Sources[0].Tools, c.Sources[0].Tools[0])
			case "schema":
				c.Sources[0].Tools[0].InputSchema = json.RawMessage(`[]`)
			}
			rec := &catalogRecorder{TB: t}
			exttest.CheckConnectors(rec, []byte(catalogManifest), exttest.Serve(t, *catalogSDKProgram(c)))
			if len(rec.errors) == 0 || !strings.Contains(strings.Join(rec.errors, " "), "catalog.list") {
				t.Fatal("SDK conformance accepted an invalid catalog")
			}
		})
	}
}
