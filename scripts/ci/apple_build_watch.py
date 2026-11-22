"""Bound resource evidence for one Apple build, without changing its scheduling."""

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import shlex
import signal
import stat
import subprocess
import time

COMMAND_TIMEOUT = 10
SAMPLE_SECONDS = 15
MAX_PROCESSES = 8
MAX_OUTPUT_BYTES = 64 * 1024
MAX_PERIODIC_SAMPLES = 120
MAX_SECONDS = 3600
MAX_FRONTEND_LIFETIMES = 512
MAX_ACTIVITY_LOGS = 4
MAX_ACTIVITY_LOG_CANDIDATES = 64
SAFE_IDENTITY = re.compile(r"[^A-Za-z0-9_.-]+")


def output(command, timeout=COMMAND_TIMEOUT, limit=MAX_OUTPUT_BYTES, runner=subprocess.run):
    try:
        result = runner(command, capture_output=True, text=True, timeout=timeout)
        if len(result.stdout) > limit:
            return {"command": command, "unavailable": "output-limit"}
        return {"command": command, "exit": result.returncode,
                "stdout": result.stdout, "stderr": result.stderr[:4096]}
    except (OSError, subprocess.SubprocessError) as error:
        return {"command": command, "unavailable": type(error).__name__}


def metadata(runner=subprocess.run):
    return {
        "cpu": output(["sysctl", "-n", "hw.ncpu", "hw.memsize"], runner=runner),
        "limits": output(["/bin/sh", "-c", "ulimit -a"], runner=runner),
        "swap": output(["sysctl", "vm.swapusage"], runner=runner),
        "disk": output(["df", "-h", "."], runner=runner),
    }


def safe_identity(value, limit=128):
    """Only an argv token's bounded basename; no directories or arbitrary content."""
    if not isinstance(value, str) or "/" not in value and "\\" not in value:
        return "redacted"
    name = re.split(r"[/\\]", value)[-1]
    if len(name) > limit or name.startswith("."):
        return "redacted"
    name = SAFE_IDENTITY.sub("_", name).strip("._")
    if not name or not name[-1].isalnum():
        return "redacted"
    return name


def swift_frontend_identity(command):
    """Retain only selected module/source names; never the compiler's raw argv."""
    unavailable = {"target_module": "unavailable", "primary_source": "unavailable", "output": "unavailable"}
    if not isinstance(command, str) or len(command) > MAX_OUTPUT_BYTES:
        return unavailable
    try:
        argv = shlex.split(command)
    except ValueError:
        return {"target_module": "redacted", "primary_source": "redacted", "output": "redacted"}
    module = output_path = primary = None
    for index, argument in enumerate(argv[1:], 1):
        if argument not in {"-module-name", "-o", "-primary-file"} or index + 1 >= len(argv):
            continue
        value = argv[index + 1]
        if argument == "-module-name":
            module = value if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]{0,79}", value) else "redacted"
        elif argument == "-o":
            output_path = safe_identity(value)
        else:
            primary = safe_identity(value)
    return {"target_module": module or "unknown",
            "primary_source": primary or "unknown", "output": output_path or "unknown"}


def duration(value):
    fields = value.split("-", 1)
    days = int(fields.pop(0)) if len(fields) == 2 and fields[0].isdigit() else 0
    parts = fields[0].split(":")
    if len(parts) not in {2, 3}:
        return None
    try:
        numbers = [float(part) for part in parts]
    except ValueError:
        return None
    seconds = numbers[-1] + numbers[-2] * 60 + (numbers[-3] * 3600 if len(numbers) == 3 else 0)
    return days * 86400 + seconds


def process_snapshot(rows):
    entries = []
    commands = {}
    for pid, ppid, cpu, rss, elapsed, cpu_time, command in rows:
        # ps does not quote argv. Prefer the executable token, while retaining
        # support for an unquoted toolchain directory containing spaces.
        executable = Path(command.split()[0]).name if command.split() else ""
        suffix_name = Path(command).name.split(" ", 1)[0]
        name = executable if executable in {"swift-frontend", "swiftc", "swift", "clang", "clang++", "xcodebuild"} else suffix_name
        if name not in {"swift-frontend", "swiftc", "swift", "clang", "clang++", "xcodebuild"}:
            continue
        entries.append({"pid": pid, "ppid": ppid, "cpu_percent": cpu,
                        "rss_kib": rss, "name": name})
        commands[pid] = (command, elapsed, cpu_time)
    entries.sort(key=lambda entry: entry["rss_kib"], reverse=True)
    counts = {}
    for entry in entries:
        counts[entry["name"]] = counts.get(entry["name"], 0) + 1
    largest = entries[:MAX_PROCESSES]
    for entry in largest:
        if entry["name"] == "swift-frontend":
            command, elapsed, cpu_time = commands[entry["pid"]]
            entry.update({"cpu_seconds": cpu_time, "elapsed_seconds": elapsed,
                          **swift_frontend_identity(command)})
    return {"counts": counts, "total_rss_kib": sum(entry["rss_kib"] for entry in entries),
            "largest": largest}


def processes(runner=subprocess.run):
    """Only bounded resource summaries; frontend identities are selected from argv in memory."""
    result = output(["ps", "-axo", "pid=,ppid=,%cpu=,rss=,etime=,time=,args="], runner=runner)
    rows = []
    for line in result.get("stdout", "").splitlines():
        match = re.match(r"\s*(\d+)\s+(\d+)\s+(\S+)\s+(\d+)\s+(\S+)\s+(\S+)\s+(.+)$", line)
        if match:
            rows.append((int(match.group(1)), int(match.group(2)), match.group(3),
                         int(match.group(4)), duration(match.group(5)),
                         duration(match.group(6)), match.group(7)))
    return process_snapshot(rows)


def track_frontends(history, entries, now):
    """Bound PID-reuse-aware identities to the existing in-memory observation window."""
    for entry in entries:
        if entry["name"] != "swift-frontend":
            continue
        identity = {key: entry[key] for key in ("target_module", "primary_source", "output")}
        current = history.get(entry["pid"])
        reused = current is not None and (
            identity != current["identity"] or entry["cpu_seconds"] < current["last_cpu_seconds"]
            or entry["elapsed_seconds"] < current["last_elapsed_seconds"])
        if current is None or reused:
            if current is None and len(history) >= MAX_FRONTEND_LIFETIMES:
                continue
            current = {"pid": entry["pid"], "first_seen_monotonic": now,
                       "first_cpu_seconds": entry["cpu_seconds"],
                       "first_elapsed_seconds": entry["elapsed_seconds"], "sample_count": 0,
                       "identity": identity, "pid_reuse_observed": reused,
                       "prior_lifetimes": ((current.get("prior_lifetimes", []) + [current])[-4:]
                                           if reused else [])}
            history[entry["pid"]] = current
        current.update({"last_seen_monotonic": now, "last_cpu_seconds": entry["cpu_seconds"],
                        "last_elapsed_seconds": entry["elapsed_seconds"]})
        current["sample_count"] += 1
    return history


def open_directory(path, *, parent_fd=None):
    """Open one explicit directory without following its symbolic link."""
    try:
        return os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent_fd)
    except OSError:
        return None


def open_build_logs(derived_data):
    if not isinstance(derived_data, Path) or not derived_data.is_absolute():
        return None
    root = open_directory(derived_data)
    if root is None:
        return None
    try:
        logs = open_directory("Logs", parent_fd=root)
        if logs is None:
            return None
        try:
            return open_directory("Build", parent_fd=logs)
        finally:
            os.close(logs)
    finally:
        os.close(root)


def list_activity_logs(directory):
    """Allowlist existing names from an already-open Build directory."""
    names = sorted(os.listdir(directory))[:MAX_ACTIVITY_LOG_CANDIDATES]
    paths = []
    for name in names:
        try:
            info = os.stat(name, dir_fd=directory, follow_symlinks=False)
        except OSError:
            continue
        if (Path(name).name == name and name.endswith(".xcactivitylog")
                and len(name) <= 128 and stat.S_ISREG(info.st_mode)):
            paths.append({"name": name, "size_bytes": info.st_size})
            if len(paths) == MAX_ACTIVITY_LOGS:
                break
    return paths


def activity_log_paths(derived_data):
    """Allowlist existing build-log names, never export host paths."""
    directory = open_build_logs(derived_data)
    if directory is None:
        return []
    try:
        return list_activity_logs(directory)
    finally:
        os.close(directory)


def write_json(destination, label, record):
    path = destination / (label + ".json")
    temporary = destination / (label + ".pending")
    temporary.write_text(json.dumps(record, indent=2) + "\n")
    temporary.replace(path)


def sample(destination, label, runner=subprocess.run, *, final=False, derived_data=None,
           frontend_history=None, monotonic=time.monotonic):
    if final:
        # A bounded marker exists even if a collector must be reaped.
        write_json(destination, label, {
            "captured_at": datetime.now(timezone.utc).isoformat(),
            "unavailable": "final collection incomplete; no pressure verdict"})
    memory = output(["memory_pressure"], runner=runner)
    pages = output(["vm_stat"], runner=runner)
    swap = output(["sysctl", "vm.swapusage"], runner=runner)
    process = processes(runner)
    if frontend_history is not None:
        track_frontends(frontend_history, process["largest"], monotonic())
    # Keep only the evidence needed to compare resource pressure over time. Full
    # pressure/process logs make a 30-minute build produce tens of megabytes.
    record = {"captured_at": datetime.now(timezone.utc).isoformat(),
              "memory_free_percent": next((line.strip() for line in memory.get("stdout", "").splitlines()
                                           if "memory free percentage" in line), None),
              "pages_used_by_compressor": next((line.strip() for line in pages.get("stdout", "").splitlines()
                                               if "occupied by compressor" in line), None),
              "swap": swap.get("stdout", "").strip(),
              "processes": process,
              "xcactivitylogs": activity_log_paths(derived_data)}
    if memory.get("exit") != 0 or pages.get("exit") != 0 or swap.get("exit") != 0:
        record["unavailable"] = "resource command failed; no pressure verdict"
    if final and frontend_history is not None:
        record["swift_frontend_lifetimes"] = sorted(frontend_history.values(), key=lambda entry: entry["pid"])
    write_json(destination, label, record)
    return record


def observe(destination, seconds, stop_path, runner=subprocess.run, monotonic=time.monotonic, sleep=time.sleep,
            derived_data=None):
    destination.mkdir(parents=True, exist_ok=True)
    deadline = monotonic() + seconds
    (destination / "host-metadata.json").write_text(json.dumps(metadata(runner), indent=2) + "\n")
    frontend_history = {}
    count = 0
    try:
        while not stop_path.exists() and monotonic() < deadline and count < MAX_PERIODIC_SAMPLES:
            sample(destination, f"sample-{count:03d}", runner, derived_data=derived_data,
                   frontend_history=frontend_history, monotonic=monotonic)
            count += 1
            sleep(max(0, min(SAMPLE_SECONDS, deadline - monotonic())))
        sample(destination, "final", runner, final=True, derived_data=derived_data,
               frontend_history=frontend_history, monotonic=monotonic)
        (destination / "observer.status.txt").write_text(
            f"{count} periodic samples; resource evidence only, no scheduling change or root-cause verdict\n")
    except KeyboardInterrupt:
        sample(destination, "final", runner, final=True, derived_data=derived_data,
               frontend_history=frontend_history, monotonic=monotonic)
        (destination / "observer.status.txt").write_text("stopped; final sample retained\n")
    finally:
        if not (destination / "observer.status.txt").exists():
            (destination / "observer.status.txt").write_text(
                "final collection incomplete; bounded evidence retained, no pressure verdict\n")


def interrupted(signum, _frame):
    raise KeyboardInterrupt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--stop", type=Path, required=True)
    parser.add_argument("--seconds", type=float, required=True)
    parser.add_argument("--derived-data", type=Path)
    args = parser.parse_args()
    if args.derived_data is not None and not args.derived_data.is_absolute():
        parser.error("DerivedData must be the absolute path already selected by the build")
    if not 0 < args.seconds <= MAX_SECONDS:
        parser.error("resource observation must fit within the selected lane's build deadline")
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, interrupted)
    observe(args.output, args.seconds, args.stop, derived_data=args.derived_data)


if __name__ == "__main__":
    main()
