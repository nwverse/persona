# Windows pitfalls

Read this before changing the Windows node (#1090), or developing or testing `personad` on a Windows computer. See [windows.md](../operations/windows.md) for the design and current status.

## Development environment

- **Check out the repository with LF endings**: Git for Windows defaults to `core.autocrlf=true`, so every checked-out `.go` file has CRLF endings. `gofmt -l` reports many files, and scripts and golden files can also fail to match.
  After cloning, run `git config core.autocrlf false && git config core.eol lf` in this repository, then `git rm -q --cached -r . && git reset -q --hard` to check it out again (commit or stash your changes first).
- **Unix socket paths must not exceed 108 bytes**: Go supports AF_UNIX on Windows, but a long path makes `bind` return `invalid argument` (not “unsupported”).
  Paths such as `~/.persona/node-attach.sock` are fine; do not put test sockets deep inside temporary directories (Claude Code's scratchpad exceeded the limit).
- **`/tmp` is not the temporary directory**: On Windows, `os.MkdirTemp("/tmp", …)` means `\tmp` on the current drive, which usually does not exist. Tests that need short paths should use platform-specific files (`listen_*.go` in `orcarttest`).

## Processes and shells

- **`bash` may be WSL**: In the system PATH inherited by the service, `C:\Windows\System32\bash.exe` precedes Git. It runs commands inside WSL (another “Linux” machine, with its own claude and `~/.zshrc`).
  On Windows, node must always run shell scripts with Git for Windows bash (`loginShellProgram`, like Claude Code itself, honoring `CLAUDE_CODE_GIT_BASH_PATH`). If it cannot be found, fail to start the command; do not fall back to bare `bash`.
  Use `interactiveShell()` wherever a launcher is started. The assistant, Codex assistant, and launcher-model listing each once hardcoded “bash if zsh is missing.” On Windows, the assistant's claude entered WSL
  and immediately exited with “open terminal failed: not a terminal”; the main chat did not respond, and there was nothing in the logs (#1994). Do not choose a shell independently in new launch sites.
- **Read other processes' environment variables from the PEB**: There is no `/proc` or `ps -E`. `procs_windows.go` lists processes with Toolhelp32, then uses `NtQueryInformationProcess` + `ReadProcessMemory` on candidates
  to read `RTL_USER_PROCESS_PARAMETERS` (command line, current directory, environment block). Pointers in the PEB are addresses in the other process: read bytes and extract fields using x/sys offsets.
  Do not read directly into a struct such as `windows.PEB` that contains Go pointer fields (the GC would treat the other process's addresses as its own pointers).
- **Environment-variable values can contain spaces**: Whitespace-splitting regexes such as `envVarRe` are for `ps -E`; they truncate `C:\Users\A B\.claude`. On Windows, fetch the complete value by name (`pickEnv`); names are case-insensitive.
- **There is no “foreground process group” or “suspended” state**: On Windows, the Orca host considers claude / codex foreground when its parent-process chain contains no agent process for the same pane. See [windows.md](../operations/windows.md) (“Orca”).
- **PIDs are reused quickly and do not increase monotonically**: Sorting by PID to put wrappers first does not work on Windows. Sort by start time; when walking the parent chain, require ancestors to have started before the current process.

## Scheduled tasks (automatic startup)

`persona-link install` registers scheduled tasks on Windows ([windows.md](../operations/windows.md), “Installation and automatic startup”). These findings were all tested on a physical Windows 11 machine on 2026-11-04:

- **Standard users cannot register “Run whether user is logged on or not” tasks**: With S4U `LogonType` (no stored password), `Register-ScheduledTask` returns access denied (even for an unelevated administrator account).
  Only `InteractiveToken` works, running with the login session.
- **“Restart on failure” does not cover program exits**: `RestartOnFailure` restarts only when the task cannot start. A program that exited with code 1 was not restarted even after 4 minutes. For supervision, add a trigger that fires every few minutes,
  with the `IgnoreNew` multiple-instance policy (do nothing if already running).
- **Repetition on a logon trigger starts only at the next login**: Immediately after installation, before another login, it will not start every 5 minutes. Use a separate time trigger for supervision (`StartBoundary` set to the installation time); it still fires on schedule after a restart.
- **Console programs open windows**: A task running a console program in the user session opens a console window for it (a Windows Terminal window if Windows Terminal is the default terminal).
  Have the task start a GUI-subsystem program instead (a copy with Subsystem in the PE header changed from 3 to 2), which starts the console program with `CREATE_NO_WINDOW`: a console, but no window.
- **Change the defaults**: `ExecutionTimeLimit` defaults to 72 hours, after which the task ends (set `PT0S`); `Priority` defaults to 7, “below normal” (set 5); by default it will not start on battery and stops when unplugged.
- **schtasks output uses the system code page** (GBK on a Chinese system), and the `/Query` status column is localized (Running / 正在运行, both meaning “running”): display it, but do not parse it.
  Write XML files as UTF-16LE with a BOM so Chinese characters in usernames and paths are not corrupted.
- **Ending a task kills only the daemon itself**: Its children survive. The daemon puts itself in a Job with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` (subsequent children automatically join it),
  so when it disappears (`/End`, a crash, or a kill), its children disappear too.
- A running `.exe` cannot be overwritten or deleted. Before reinstalling, `/End` the task and wait for persona-link to release the run lock; during uninstall, the daemon file may take a moment to become deletable.

## Tray (WebView2, notification area)

`internal/linktray`; see [windows.md](../operations/windows.md) ("Tray"). Found on a physical Windows 11 machine on 2026-11-07:

- **WebView2 holds back its callbacks while one of them runs**: creating a WebView inside a `WebMessageReceived` handler gets the environment back, but the controller's completion waits until the handler returns.
  go-webview2's `Embed` spins a nested message loop until then, so it never returned: the pairing window stayed hidden, and the tray's thread stayed inside that loop (which also swallowed the quit message). Handle page messages after the event has returned (`app.fromPage` queues them).
- **go-webview2's `PutRasterizationScale` passes a pointer to the double**: the COM method takes the double itself. On windows/amd64 Go's syscall also copies the integer arguments into the XMM registers, so pass `math.Float64bits(scale)`, as its `PutZoomFactor` does.
  With the pointer, pages rendered at scale 0.5: `devicePixelRatio` was 0.5 and everything was half size.
- **go-webview2 clears `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS`** (and the other override variables) before it creates the environment, so Microsoft's documented way to add `--remote-debugging-port` does nothing. Pass flags through `AdditionalBrowserArgs` (`PERSONA_TRAY_DEVTOOLS=<port>`).
- **Windows 11 puts new notification icons in the overflow**: UI Automation on the taskbar doesn't see them. `HKCU\Control Panel\NotifyIconSettings\<id>` (`ExecutablePath`, `InitialTooltip`) shows that the shell took the icon.
  A tray run from a temporary directory leaves an entry there; delete it after testing.
- **Tests must not touch the real registry**: the install tests once wrote the HKCU Run entry and the pairing link handlers, pointing at a test's temporary directory. Everything the tray does to the computer goes through `trayControl`, which tests fake.
- **A page that loses focus hides the flyout**: while scripting the panel over CDP, Playwright's clicks wait for it to be visible forever; click with `element.click()` in the page.
- **Push the new view to a hidden panel after an action**: the tray pushed views only while the panel was visible, so a switch flipped from the right-click menu, or an action that ended after the panel closed, left the page showing the old state, and the next open showed it too. After a user's action the tray pushes the view whether the panel is shown or not (`app.changed`).

## Machine monitoring (sysmon)

`sysmon/windows.go`; see [machines.md](../server/machines.md) for measurement definitions. Findings tested on a physical machine on 2026-11-04:

- **`NtQuerySystemInformation(SystemProcessorPerformanceInformation)` requires an exact-size buffer**: Given 4096 bytes, it returns `STATUS_INFO_LENGTH_MISMATCH`;
  increasing the buffer to the returned length does not help. It must be exactly “logical CPU count × 48 bytes.” Other classes (page files) allow increasing to the returned length. This was why all per-core data was initially empty.
- **The registry ProductName still says “Windows 10” on Windows 11**: They share the NT 10.0 kernel; identify Windows 11 by CurrentBuild ≥ 22000.
- **Google Drive's G: is a fake fixed disk**: `GetDriveType` reports `DRIVE_FIXED`, the filesystem claims FAT32, capacity copies C:, and it even has a volume GUID.
  Only asking for the volume's disk extents (`IOCTL_VOLUME_GET_VOLUME_DISK_EXTENTS`, no administrator privileges needed) reveals it: disk 0, offset 0, length 0. Exclude it using this, not guesses based on the filesystem name.
- **One `net.Interfaces()` call takes about 7 ms** (using `GetAdaptersAddresses` underneath), while reading counters for one adapter (`GetIfEntry2Ex`) takes only tens of microseconds. Cache the adapter list for one minute.
- Determine which adapters count from `MIB_IF_ROW2.InterfaceAndOperStatusFlags`: bit 0 is HardwareInterface, bit 1 is FilterInterface. Tailscale (wintun),
  Wi-Fi Direct, Bluetooth personal-area networking, and loopback all lack HardwareInterface.

## Orca

- **Orca runtime on Windows uses named pipes**: `transports` in `orca-runtime.json` contains `named-pipe` (`\\.\pipe\orca-<pid>-…`) and `websocket`, but no `unix`.
  `orcart` selects the transport by platform: on Windows, `named-pipe` first (`tailscale/go-winio` for dialing), then `unix`; on other platforms, only `unix`. The protocol (one JSON per line) is unchanged.
- Orca terminals have `ORCA_PANE_KEY` / `ORCA_AGENT_PANE`, just like macOS, plus `ORCA_USER_DATA_PATH` (`%APPDATA%\orca`).
- Paths in the `topologyRevisions` keys of `terminal.list` mix separators (`C:/Users/…` and `C:\Users\…` on the same computer). Do not compare them directly to `worktreePath` as strings.

## herdr

- **herdr's API is a named pipe whose name is the socket path**: herdr uses interprocess's `GenericNamespaced`; `C:\Users\<you>\AppData\Roaming\herdr\herdr.sock` maps to
  `\\.\pipe\C:\Users\<you>\AppData\Roaming\herdr\herdr.sock`. The path itself is not a file, so `os.Stat` on it always reports missing; stat the pipe name instead. The configuration directory is `%APPDATA%\herdr`, not `~/.config/herdr`.
- **`herdr server` started over ssh exits with the ssh session** (Windows OpenSSH ends the entire job when closing a session); `Start-Process` behaves the same way. For a persistent server, use a scheduled task triggered at login, running in the user's desktop session.
- **Panes default to PowerShell**: node's input commands use POSIX shell syntax (`'…'` quotes, `A=1 cmd`), which PowerShell splits incorrectly. Set `[terminal] default_shell` in `config.toml` to Git Bash.
  This key must be under `[terminal]`; at the top level, `reload-config` only reports “unknown config key default_shell; ignoring key.”
- **A fresh Claude Code install first shows the theme-selection onboarding**: node's `waitReady` cannot find the input box within a minute and reports “claude did not become ready,” while the screen shows a theme list. Set `hasCompletedOnboarding: true` in `~/.claude.json`, or run it manually once.
- **apiKeyHelper is executed by cmd on Windows**: `cat ~/.config/…` returns “'cat' 不是内部或外部命令” (“'cat' is not recognized as an internal or external command”); use `type "C:\…\key"`.
- **MinGit does not contain bash.exe** (only `usr\bin\sh.exe`). For Git Bash, use PortableGit or the installed distribution.
- **Quotes are parsed again by cmd when running PowerShell over ssh**: For long scripts, use `powershell -EncodedCommand <UTF-16LE base64>`. PowerShell 5.1 also splits arguments such as `-test.run=A|B` incorrectly when passing them to native programs; wrap them in `cmd /c "…"`.
- **`signal.Ignore(os.Interrupt)` cannot block Ctrl+C**: On Windows, only `signal.Notify` disables the default exit behavior for Ctrl+C / Ctrl+Break (see the os/signal documentation). When `osproc.Exec` used Ignore,
  one Ctrl+C in the console ended the launcher with `0xc000013a`; the child (claude) survived, the parent disappeared, and the exit code was lost. `TestExecSurvivesCtrlC` guards this.
- A test program `xxx.test.exe` can keep running briefly after exit (child processes), making scp fail to overwrite it with `dest open … Failure`. Use another filename or end the process first.
- **Stopping a background shell does not stop the mock hub it started, and a second mock still binds the port**: on Windows, ending the Git Bash job that ran `python3 scripts/mock/mock_hub.py` leaves the Python process running, and Python's `HTTPServer` sets `SO_REUSEADDR`, which on Windows lets a restarted mock bind the same port too. Requests then go to either process, so a fix in the mock seems to have no effect (an unknown `/mock/…` route answers 404). Before restarting, look with `netstat -ano | grep :<port>` and end your own old process by its PID (`taskkill //PID <pid> //F`), never every Python.
