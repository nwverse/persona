//go:build !windows

package node

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/osproc"
)

// launchBrowser starts the browser with CDP on its fds 3 (it reads) and 4 (it writes). stop ends it and everything it
// started (renderers, GPU process): it runs in a process group of its own. exited is closed once it is gone.
func launchBrowser(path string, args []string, stderr io.Writer, exited chan struct{}) (io.Reader, io.WriteCloser, func(), error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, nil, nil, err
	}
	cmd := exec.Command(path, args...)
	cmd.ExtraFiles = []*os.File{inR, outW}
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	cmd.SysProcAttr = osproc.GroupAttr()
	if err := cmd.Start(); err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, nil, nil, err
	}
	inR.Close()
	outW.Close()
	go func() {
		_ = cmd.Wait()
		outR.Close()
		close(exited)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			inW.Close() // the browser exits when its pipe closes
			select {
			case <-exited:
				return
			case <-time.After(2 * time.Second):
			}
			_ = osproc.StopGroup(cmd.Process.Pid, false)
			select {
			case <-exited:
			case <-time.After(2 * time.Second):
				_ = osproc.StopGroup(cmd.Process.Pid, true)
			}
		})
	}
	return outR, inW, stop, nil
}
