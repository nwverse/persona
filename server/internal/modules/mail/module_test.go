package mail

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// The manifest names what the module adds: every route Handler serves and every tool Tools lists, nothing else.
func TestManifestDeclaresTheRoutesAndTools(t *testing.T) {
	info := Info()
	if info.ID != "mail" || info.Origin != "builtin" || info.API != 1 {
		t.Fatalf("manifest %+v", info)
	}
	src, err := os.ReadFile("http.go")
	if err != nil {
		t.Fatal(err)
	}
	var routes []string
	for _, m := range regexp.MustCompile(`mux\.HandleFunc\("([^"]+)"`).FindAllStringSubmatch(string(src), -1) {
		routes = append(routes, m[1])
	}
	if len(routes) == 0 || !slices.Equal(routes, info.HubRoutes) {
		t.Errorf("module.json hub_routes %q, Handler serves %q", info.HubRoutes, routes)
	}
	var tools []string
	for _, tool := range (*Service)(nil).Tools() {
		tools = append(tools, tool.Name)
	}
	if !slices.Equal(tools, info.MCPTools) {
		t.Errorf("module.json mcp_tools %q, Tools lists %q", info.MCPTools, tools)
	}
}
