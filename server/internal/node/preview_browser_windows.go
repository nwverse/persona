package node

import (
	"errors"
	"io"
)

// launchBrowser: CDP over a pipe needs inherited fds 3 and 4, which Windows does not have (docs/web/preview.md "Not done
// yet").
func launchBrowser(string, []string, io.Writer, chan struct{}) (io.Reader, io.WriteCloser, func(), error) {
	return nil, nil, nil, errors.New("previews are not available on Windows yet")
}
