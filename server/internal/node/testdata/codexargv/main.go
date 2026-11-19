// A disposable process boundary for the real codex-start helper. It never runs
// Codex, opens a terminal or contacts a node/hub. Both roles use only the test's
// directory and synthetic input; the receiver sees the OS-decoded native argv.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/node"
)

func main() {
	if len(os.Args) > 2 && os.Args[1] == "receive" {
		_, err := os.Stat(filepath.Join(os.Args[2], "start.json"))
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"args": os.Args[3:], "source_removed": os.IsNotExist(err),
		})
		return
	}
	var input struct{ Dir, Prompt string }
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		panic(err)
	}
	self, err := os.Executable()
	if err != nil {
		panic(err)
	}
	file := filepath.Join(input.Dir, "start.json")
	data, _ := json.Marshal(map[string]any{
		"argv": []string{self, "receive", input.Dir}, "prompt": input.Prompt, "dir": input.Dir,
	})
	if err := os.WriteFile(file, data, 0o600); err != nil {
		panic(err)
	}
	err = node.CodexStart([]string{file})
	_, statErr := os.Stat(file)
	// A successful exec never returns. Limit failures must happen before the
	// receiver starts, with no truncation or terminal-input fallback.
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"limit_refused":  err != nil && strings.Contains(err.Error(), "32767"),
		"source_removed": os.IsNotExist(statErr),
	})
}
