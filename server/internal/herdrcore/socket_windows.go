package herdrcore

import (
	"context"
	"errors"
	"net"
	"os"
	"os/user"
	"strings"

	"github.com/tailscale/go-winio"
)

// socketPath is herdr-core's named pipe for this user and session. $HC_SOCKET and config.toml name a unix socket
// path, which herdr-core does not use on Windows, so they are not read; a pipe name in $HC_SOCKET is taken as it is.
func socketPath() string {
	if p := os.Getenv("HC_SOCKET"); strings.HasPrefix(p, `\\.\pipe\`) {
		return p
	}
	return pipeName(pipeUser(), sessionName())
}

// pipeUser is the user name hc puts in its pipe name: %USERNAME%, else the account's name without its domain.
func pipeUser() string {
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	if cu, err := user.Current(); err == nil {
		name := cu.Username
		if i := strings.LastIndexByte(name, '\\'); i >= 0 {
			name = name[i+1:]
		}
		return name
	}
	return ""
}

// socketStat asks for the pipe without opening it (opening one takes the server's only free instance for a moment).
func socketStat(path string) error {
	_, err := os.Stat(path)
	return err
}

// dialSocket connects to herdr-core's named pipe.
func dialSocket(ctx context.Context, path string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, path)
}

// socketGone: err says there is no pipe to connect to.
func socketGone(err error) bool { return errors.Is(err, os.ErrNotExist) }
