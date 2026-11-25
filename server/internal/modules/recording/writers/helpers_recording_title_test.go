package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"context"
	"encoding/json"
	"strings"
)

// titleModel answers each call with the next reply and keeps what it was asked; an error reply is returned instead.
type titleModel struct {
	replies []string
	errs    []error
	calls   [][2]string
}

func (m *titleModel) JSONMax(_ context.Context, system, user string, _ int, out any) error {
	m.calls = append(m.calls, [2]string{system, user})
	i := len(m.calls) - 1
	if i < len(m.errs) && m.errs[i] != nil {
		return m.errs[i]
	}
	reply := m.replies[min(i, len(m.replies)-1)]
	return json.Unmarshal([]byte(reply), out)
}

// talk is n lines of about 50 characters each, numbered so each upload's text is new.
func talk(from, n int) []string {
	var out []string
	for i := from; i < from+n; i++ {
		out = append(out, strings.Repeat("导出上限按条还是按录音算要先定下来，", 2)+"第"+string(rune('A'+i%26))+"段")
	}
	return out
}
