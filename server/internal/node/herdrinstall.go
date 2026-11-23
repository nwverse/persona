package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// A machine with no herdr at all (a new computer joined as a service, #2847) had the Agents list's "Start" answer only
// "herdr is not installed", and someone had to log in and install it. The node installs it itself now: herdr's own
// release binary for this OS and CPU, of one pinned version (the one the other computers run, not "latest"),
// checked against the sha256 written here, into ~/.local/bin (no sudo; the node's PATH has it), then starts it as
// before (docs/integrations/engines.md "When herdr is not installed").
//
// Only when the user asked: the "Start" button always may; the node's own starts (at node start, before a start or
// resume) only when hub.json machines[].herdr_autoinstall is true. A download can take minutes on a slow link: it
// goes on in the background, "Start" waits herdrInstallWait for it and else says it is installing; once installed the
// node starts herdr itself, so the Agents list's row goes away on its next read.

// herdrRelease is the herdr the node installs: its version, where each target's binary is and its sha256 (taken
// from the release's assets; GitHub lists the same digests). Tests replace it.
var herdrRelease = struct {
	Version string
	URL     func(version, target string) string
	SHA256  map[string]string // target (linux-x86_64, macos-aarch64, …) → hex sha256
}{
	// 0.9.5: the herdr the docs describe (agy's states, tab.create's environment, the JSON integration API the agy start
	// asks, agyhook.go). The digests are the release's, each checked against its downloaded binary.
	Version: "0.9.5",
	URL: func(version, target string) string {
		return "https://github.com/herdrdev/herdr/releases/download/v" + version + "/herdr-" + target
	},
	SHA256: map[string]string{
		"linux-x86_64":  "14d9e8bc8b4a9e0118d77c32266f76d461f09b40342eb23752960bd89f59929d",
		"linux-aarch64": "17d7536f2da50859523680ef279dffcac8e4a235f4e46fe8da6c2ec44141897c",
		"macos-x86_64":  "5fb0a908ec7f6745e3e245738b06bfad56c46391c391ab3c0955fbead01f1369",
		"macos-aarch64": "fb77c2232a05a8035421382c894392f4174e4e38a18e48af3dd0290157c3a7e2",
	},
}

// herdrInstallWait is how long "Start" waits for an install before saying it goes on (the hub gives the call 40 s,
// starting the server takes up to herdrReadyWait more); herdrInstallTimeout bounds one install.
var herdrInstallWait, herdrInstallTimeout = 15 * time.Second, 15 * time.Minute

// herdrInstallDir is where it goes; tests replace it.
var herdrInstallDir = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin"), nil
}

// errHerdrInstalling: "Start" waited and the download still runs; the node starts herdr when it is done.
var errHerdrInstalling = errors.New("正在安装 herdr " + herdrRelease.Version + "（从 GitHub 下载），装好后会自动启动，稍等一会儿再看")

// herdrInstaller is the node's state of installing herdr: one install at a time, the ones asking meanwhile wait for
// it.
type herdrInstaller struct {
	auto atomic.Bool // hub.json machines[].herdr_autoinstall: the node's own starts may install too
	mu   sync.Mutex
	done chan struct{}  // the install running, nil when none
	err  error          // the last one's
	bg   sync.WaitGroup // the background installs, start included (tests wait for them)
}

// setHerdrAutoinstall is the hub's word (node.configure herdr_autoinstall).
func (n *Node) setHerdrAutoinstall(on bool) { n.hinstall.auto.Store(on) }

// herdrInstalling says whether an install runs now and why the last one failed ("" when it did not): herdr.server.status.
func (n *Node) herdrInstalling() (bool, string) {
	n.hinstall.mu.Lock()
	defer n.hinstall.mu.Unlock()
	msg := ""
	if n.hinstall.err != nil {
		msg = n.hinstall.err.Error()
	}
	return n.hinstall.done != nil, msg
}

// startOrInstallHerdr is startHerdr, and when herdr is not installed and install says it may, installs it and
// starts it then.
func (n *Node) startOrInstallHerdr(ctx context.Context, h *herdrHost, why string, install bool) error {
	err := n.startHerdr(ctx, h, why)
	if !install || !errors.Is(err, errHerdrNotInstalled) {
		return err
	}
	if err := n.installHerdrFor(ctx, h); err != nil {
		return err
	}
	return n.startHerdr(ctx, h, why)
}

// installHerdrFor installs herdr unless it is there (then nil at once), waiting at most herdrInstallWait:
// errHerdrInstalling when it still runs then, the install's error when it failed. The install itself goes on
// with the node (not the asking call) and starts herdr's server for h when it is done.
func (n *Node) installHerdrFor(ctx context.Context, h *herdrHost) error {
	if findHerdr(ctx, h.c.Bin) != "" {
		return nil
	}
	n.hinstall.mu.Lock()
	done := n.hinstall.done
	if done == nil {
		done = make(chan struct{})
		n.hinstall.done, n.hinstall.err = done, nil
		n.hinstall.bg.Add(1)
		go func() {
			defer n.hinstall.bg.Done()
			n.runHerdrInstall(h, done)
		}()
	}
	n.hinstall.mu.Unlock()
	wait := time.NewTimer(herdrInstallWait)
	defer wait.Stop()
	select {
	case <-done:
		n.hinstall.mu.Lock()
		defer n.hinstall.mu.Unlock()
		return n.hinstall.err
	case <-wait.C:
		return errHerdrInstalling
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runHerdrInstall is the background install: download, check, install, then start the server (for an asker that
// stopped waiting).
func (n *Node) runHerdrInstall(h *herdrHost, done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrInstallTimeout)
	defer cancel()
	start := time.Now()
	n.logf("herdr: not installed, installing herdr %s", herdrRelease.Version)
	path, err := installHerdr(ctx)
	if err == nil && findHerdr(ctx, h.c.Bin) == "" {
		err = fmt.Errorf("装好了 %s，可 node 还是找不到 herdr 命令", path)
	}
	if err != nil {
		n.logf("herdr: install failed: %v", err)
	} else {
		n.logf("herdr: installed %s to %s in %s", herdrRelease.Version, path, time.Since(start).Round(time.Second))
	}
	n.hinstall.mu.Lock()
	n.hinstall.err, n.hinstall.done = err, nil
	n.hinstall.mu.Unlock()
	close(done)
	if err == nil {
		if err := n.startHerdr(ctx, h, "herdr was just installed"); err != nil {
			n.logf("herdr: installed but not started: %v", err)
		}
	}
}

// herdrTarget is herdr's name for this machine in its release assets.
func herdrTarget(goos, goarch string) (string, bool) {
	o := map[string]string{"linux": "linux", "darwin": "macos"}[goos]
	a := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	if o == "" || a == "" {
		return "", false
	}
	return o + "-" + a, true
}

// installHerdr downloads the pinned herdr for this machine into herdrInstallDir, checks its sha256 and makes it the
// herdr command there; it returns the path. The binary only lands under its name once checked: a broken download
// leaves nothing behind.
func installHerdr(ctx context.Context) (string, error) {
	v := herdrRelease.Version
	target, ok := herdrTarget(runtime.GOOS, runtime.GOARCH)
	want := herdrRelease.SHA256[target]
	if !ok || want == "" {
		return "", fmt.Errorf("herdr %s 没有 %s/%s 的安装包，没法自动装：请在这台电脑上手动安装 herdr（https://herdr.dev）",
			v, runtime.GOOS, runtime.GOARCH)
	}
	dir, err := herdrInstallDir()
	if err != nil {
		return "", fmt.Errorf("没装上 herdr：找不到安装目录：%w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("没装上 herdr：建不了 %s：%w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".herdr-install-*")
	if err != nil {
		return "", fmt.Errorf("没装上 herdr：%s 写不进去：%w", dir, err)
	}
	defer os.Remove(tmp.Name()) // gone once renamed
	defer tmp.Close()
	src := herdrRelease.URL(v, target)
	if err := downloadHerdr(ctx, src, tmp); err != nil {
		return "", err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("没装上 herdr：%w", err)
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, tmp); err != nil {
		return "", fmt.Errorf("没装上 herdr：%w", err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != want {
		return "", fmt.Errorf("下载的 herdr %s 校验不对（sha256 %s，应为 %s），没有安装", v, got, want)
	}
	if err := tmp.Chmod(0o755); err != nil {
		return "", fmt.Errorf("没装上 herdr：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("没装上 herdr：%w", err)
	}
	path := filepath.Join(dir, "herdr")
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("没装上 herdr：%w", err)
	}
	return path, nil
}

// downloadHerdr writes src's body to w. Its errors say what the user can do: a machine that cannot reach GitHub
// usually needs a proxy (the node honours HTTPS_PROXY, as Go's client does).
func downloadHerdr(ctx context.Context, src string, w io.Writer) error {
	v := herdrRelease.Version
	host := src
	if u, err := url.Parse(src); err == nil {
		host = u.Host
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return fmt.Errorf("没装上 herdr：%w", err)
	}
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{Timeout: 15 * time.Second}).DialContext, TLSHandshakeTimeout: 15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second}}).Do(req)
	if err == nil && resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("没能下载 herdr %s：%s 回了 HTTP %d", v, host, resp.StatusCode)
	}
	if err == nil {
		defer resp.Body.Close()
		_, err = io.Copy(w, resp.Body)
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("下载 herdr %s 太慢，%s 内没下完（%s）：网络慢或要走代理，可给这台电脑的 node 设 HTTPS_PROXY 后重启它再试，或手动安装 herdr",
			v, herdrInstallTimeout, host)
	}
	return fmt.Errorf("没能下载 herdr %s：连不上 %s（%v）。这台电脑可能要走代理：给它的 node 设 HTTPS_PROXY 后重启它再试，或手动安装 herdr", v, host, err)
}
