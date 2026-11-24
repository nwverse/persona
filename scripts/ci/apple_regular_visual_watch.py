"""Observe only the regular visual app-host launch inside its existing 600-second step."""

import argparse
from datetime import datetime, timedelta, timezone
import json
import re
import selectors
import signal
import subprocess

import apple_launch_diagnostics as launch
import apple_readiness_watch as watch

VISUAL_SECONDS = launch.VISUAL_SECONDS
SAMPLE_SECONDS = 15
SYSTEM_AFTER = 60
FINISH_BEFORE = 35
VISUAL_PROCESSES = ("Persona", "debugserver")


def scoped_processes(udid, runner=None):
    """Return owned host/debugger metadata, never arguments or environment."""
    runner = runner or watch.subprocess.run
    try:
        result = runner(["ps", "-axo", "pid=,ppid=,stat=,comm="],
                        capture_output=True, text=True, timeout=5)
    except watch.subprocess.TimeoutExpired:
        return {"query_status": "query-timeout", "processes": []}
    except (OSError, watch.subprocess.SubprocessError):
        return {"query_status": "query-failed", "processes": []}
    if result.returncode:
        return {"query_status": "query-failed", "processes": []}
    process_pattern = re.compile(r"^\s*(\d+)\s+(\d+)\s+(\S+)\s+([^\n]*?/([^/\s]+))\s*$")
    host_pattern = re.compile(r".*/CoreSimulator/Devices/" + re.escape(udid) +
                              r"/[^\n]*/Persona\.app/Persona$")
    records = []
    for line in result.stdout.splitlines():
        match = process_pattern.match(line)
        if match and match.group(5) in VISUAL_PROCESSES:
            records.append(match.groups())
    hosts = {int(pid) for pid, _ppid, _state, path, name in records
             if name == "Persona" and host_pattern.fullmatch(path)}
    processes = [{"pid": int(pid), "ppid": int(ppid), "state": state, "name": name}
                 for pid, ppid, state, path, name in records
                 if (name == "Persona" and int(pid) in hosts) or
                 (name == "debugserver" and int(ppid) in hosts)]
    return {"query_status": "completed", "processes": sorted(processes, key=lambda item: (item["name"], item["pid"]))}


def sample_processes(udid, destination, index, runner=watch.subprocess.run):
    observation = scoped_processes(udid, runner)
    label = f"process-{index:03d}"
    launch.publish(destination / (label + ".json"), json.dumps({
        "captured_at": datetime.now(timezone.utc).isoformat(), **observation,
        "note": "Process presence is not a launch, test-bundle or readiness verdict."}, indent=2) + "\n")
    for process in observation["processes"]:
        watch.bounded_file(["ps", "-M", str(process["pid"])],
                           destination / (label + f"-{process['name']}-{process['pid']}-threads.txt"), timeout=5)
    return observation


def launch_system_line(raw):
    """Retain service identity and launch state, never launchd environment or arguments."""
    try:
        text = raw.decode()
    except UnicodeDecodeError:
        return False
    return launch.BUNDLE in text and not launch.SECRET.search(text)


def launch_system(udid, destination):
    """Filter while reading; even a silent or noisy launchctl cannot outlive its bound."""
    path = destination / "launch-system-after-60s.txt"
    status = path.with_suffix(path.suffix + ".status.txt")
    launch.publish(status, "incomplete\n")
    process = None
    try:
        with path.with_suffix(path.suffix + ".command").open("wb") as output:
            process = subprocess.Popen(
                ["xcrun", "simctl", "spawn", udid, "launchctl", "print", "system"],
                stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            deadline = watch.time.monotonic() + 10
            kept = 0
            partial = bytearray()
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                while selector.get_map():
                    remaining = deadline - watch.time.monotonic()
                    if remaining <= 0:
                        raise subprocess.TimeoutExpired(process.args, 10)
                    for key, _ in selector.select(remaining):
                        chunk = launch.os.read(key.fd, 4096)
                        if not chunk:
                            selector.unregister(key.fileobj)
                            continue
                        partial.extend(chunk)
                        while True:
                            try:
                                newline = partial.index(10)
                            except ValueError:
                                break
                            line, partial = partial[:newline + 1], partial[newline + 1:]
                            if launch_system_line(line) and kept < watch.MAX_SAMPLE_BYTES:
                                piece = bytes(line[:watch.MAX_SAMPLE_BYTES + 1 - kept])
                                output.write(piece)
                                kept += len(piece)
                        if kept > watch.MAX_SAMPLE_BYTES or len(partial) > watch.MAX_SAMPLE_BYTES:
                            raise launch.OutputLimitError("launch system output exceeded byte limit")
            if process.wait(timeout=max(0, deadline - watch.time.monotonic())):
                raise subprocess.CalledProcessError(process.returncode, process.args)
        path.with_suffix(path.suffix + ".command").replace(path)
        launch.publish(status, "0\n")
        return 0
    except subprocess.TimeoutExpired:
        launch.publish(status, "124\n")
        launch.publish(path, "unavailable: command timed out\n")
        return 124
    except launch.OutputLimitError:
        path.unlink(missing_ok=True)
        launch.publish_status(status, "output-limit\n")
        return 125
    except (OSError, subprocess.SubprocessError):
        launch.publish_status(status, "unavailable: command-failure\n")
        return 127
    finally:
        if process is not None:
            if process.poll() is None:
                process.kill()
                process.wait()
            process.stdout.close()


def observe(udid, bundle, destination, seconds=VISUAL_SECONDS, scope_root=None,
            monotonic=watch.time.monotonic, sleep=watch.time.sleep):
    launch.identity(udid, bundle)
    if seconds > VISUAL_SECONDS:
        raise ValueError("visual observation cannot exceed the existing step deadline")
    destination.mkdir(parents=True, exist_ok=True)
    launch.publish(destination / "observer.status.txt",
                   "collection incomplete; observation only; no launch cause established\n")
    launch.publish(destination / "launch-system-after-60s.txt", "unavailable: observation incomplete\n")
    launch.publish(destination / "launch-system-after-60s.txt.status.txt", "incomplete\n")
    deadline = monotonic() + seconds
    started = monotonic()
    stopped = (destination / "observer.stop").exists()
    live = None
    index = 0
    system_captured = False
    try:
        try:
            if not stopped:
                live = watch.start_live_log(scope_root, destination,
                                            deadline=datetime.now(timezone.utc) + timedelta(seconds=seconds))
            while not stopped and monotonic() < deadline - FINISH_BEFORE:
                if (destination / "observer.stop").exists():
                    stopped = True
                    break
                now = monotonic()
                if now - started >= index * SAMPLE_SECONDS:
                    observation = sample_processes(udid, destination, index)
                    watch.correlate_live(live, {"host_pids": [p["pid"] for p in observation["processes"]
                                                               if p["name"] == "Persona"]})
                    index += 1
                if not system_captured and now - started >= SYSTEM_AFTER:
                    launch_system(udid, destination)
                    system_captured = True
                sleep(min(1, max(0, deadline - FINISH_BEFORE - monotonic())))
        except KeyboardInterrupt:
            stopped = True
        if not system_captured and not stopped:
            launch_system(udid, destination)
        launch.collect(destination.parent, destination, scope_root=scope_root)
        launch.publish(destination / "observer.status.txt",
                       ("xcodebuild-finished" if stopped else "visual-deadline") +
                       "; observation only; no launch cause established\n")
    finally:
        watch.stop_live_log(live)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--udid", required=True)
    parser.add_argument("--bundle-id", required=True)
    parser.add_argument("--scope-root", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--seconds", type=float, default=VISUAL_SECONDS)
    args = parser.parse_args()
    if not 0 < args.seconds <= VISUAL_SECONDS:
        parser.error("observation cannot exceed the visual step deadline")
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, watch.interrupted)
    observe(args.udid, args.bundle_id, args.output, args.seconds, scope_root=args.scope_root)


if __name__ == "__main__":
    main()
