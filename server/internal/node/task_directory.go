package node

import (
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/i18n"
	"io"
	"os"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// taskDirectory runs on the execution computer. It never prepares, creates or chmods a user directory.
func (n *Node) taskDirectory(path string) (proto.Workspace, error) {
	if path != strings.TrimSpace(path) {
		return proto.Workspace{}, i18n.Err("assistant-schedule.execution_directory_whitespace")
	}
	w, err := n.resolveWorkspace(path)
	if err != nil {
		return w, err
	}
	// Opening '/.' checks search access to the directory as well as read access, without changing process cwd.
	f, err := os.Open(w.Cwd + string(os.PathSeparator) + ".")
	if err != nil {
		return w, err
	}
	defer f.Close()
	if _, err = f.ReadDir(1); err != nil && !errors.Is(err, io.EOF) {
		return w, err
	}
	return w, nil
}

func (n *Node) checkTaskDirectory(id, cwd, launcher string) error {
	w, err := n.taskDirectory(cwd)
	if err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	st, ok := n.state.get(id)
	if !ok {
		return fmt.Errorf("conversation %s is gone", id)
	}
	previous, err := n.taskDirectory(st.Cwd)
	if err != nil {
		return err
	}
	if previous.Cwd != w.Cwd || st.Launcher != launcher {
		return errors.New("task execution context differs from this conversation; start a new conversation")
	}
	return nil
}
