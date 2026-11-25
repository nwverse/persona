#!/usr/bin/env python3
"""Fail when the contract packages or the hub's handlers gain weak types: bare enums and ids, untyped maps, raw JSON.

server/internal/archguard/wirecount counts, with go/parser, the weak shapes in every package of
server/internal/archguard/testdata/contracts.txt (a string field named *State, *Status, *Phase, *Kind, *Type or *Role; a
string or []string *ID field; map[string]any; json.RawMessage; *time.Time, *bool, *string *Error fields) and in the
handler layer (server/internal/{hub,assistant,devices,extmgr}: map[string]any{"type": ...} events, writeJSON(map...)
calls, .Call(ctx, "method", ...) with a literal method). The counts are global and kept as names, never per package:
a field is "<Type>.<Field>", an event is its type string, a writeJSON map is its sorted keys, a call is its method.
A type that moves from one contract package to another (the module slimming does this) keeps its names and so is
neutral; only a name that is new, or occurs more often than the baseline allows, is a rise.

The baseline is scripts/ci/wire_types_baseline.json: {"metrics": {metric: {name: count}}, "exceptions": ...}. A rise
fails with the new names and how to fix them. There is no ignore marker. A weak type that has to stay goes in the
baseline's "exceptions": {metric: {name: {"count": n, "pr": "#N", "why": "..."}}}, which raises that name's allowance
for review to see. --update only lowers: it drops names that are gone and lowers counts, and it refuses to raise or
add a name or to create a baseline. With a base (--base, else CI_BASE_SHA; scripts/ci/ratchet_base.py) the baseline's
own metrics may not rise or gain a name against the merge base. Needs Go. See docs/operations/testing.md ("Count
ratchets") and docs/lessons/testing.md.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parent))
import ratchet_base  # noqa: E402

BASELINE_REL = "scripts/ci/wire_types_baseline.json"
COUNTER_PKG = "./internal/archguard/wirecount/"
GO_TIMEOUT = 300
METRICS = ("bare-enum-state", "bare-enum-kind", "bare-enum-type", "bare-enum-role", "bare-id", "bare-error",
           "map-any", "raw-json", "ptr-time", "ptr-bool", "ptr-error-string", "inline-event", "write-json-map",
           "literal-call")
FIX = {
    "bare-enum-state": "use a named type with a closed set of values (a Go enum with Valid(), a schema enum) instead of string",
    "bare-enum-kind": "use a named type with a closed set of values instead of string",
    "bare-enum-type": "use a named type with a closed set of values instead of string",
    "bare-enum-role": "use a named type with a closed set of values instead of string",
    "bare-id": "use an ID type (agentref.MachineID, SessionID, ... or a new one) instead of string",
    "bare-error": "use a typed failure (a closed code plus message) instead of a bare string",
    "map-any": "use a struct or a named type; decode into it instead of map[string]any",
    "raw-json": "use the typed payload, or a named type that says what the JSON is",
    "ptr-time": "model the states as one closed enum with its own timestamps instead of optional fields",
    "ptr-bool": "model the states as one closed enum instead of an optional flag",
    "ptr-error-string": "use a typed failure instead of an optional error string",
    "inline-event": "define the event as a typed struct in the wire package and send that",
    "write-json-map": "reply with a named response struct from the wire package",
    "literal-call": "call a method descriptor constant from the wire package, not a string literal",
}


class WireTypesError(Exception):
    """The check could not be run (no Go, a broken baseline, or git failed)."""


def parse_baseline(text, label="baseline"):
    """(metrics, exceptions): metrics {metric: {name: count}}, exceptions {metric: {name: count}}."""
    try:
        data = json.loads(text)
    except ValueError as error:
        raise WireTypesError("wire types ratchet: {0} is not JSON: {1}".format(label, error))
    metrics = data.get("metrics") if isinstance(data, dict) else None
    if not isinstance(metrics, dict):
        raise WireTypesError("wire types ratchet: {0} has no \"metrics\" object".format(label))
    exceptions = {}
    for metric, names in (data.get("exceptions") or {}).items():
        for name, entry in names.items():
            if not (isinstance(entry, dict) and isinstance(entry.get("count"), int) and entry["count"] > 0
                    and str(entry.get("pr", "")).startswith("#") and entry.get("why")):
                raise WireTypesError("wire types ratchet: {0}: exception {1} {2} needs count, pr (#N) and why".format(
                    label, metric, name))
            exceptions.setdefault(metric, {})[name] = entry["count"]
    for metric, names in metrics.items():
        if metric not in METRICS:
            raise WireTypesError("wire types ratchet: {0}: unknown metric {1}".format(label, metric))
        for name, count in names.items():
            if not isinstance(count, int) or count < 1:
                raise WireTypesError("wire types ratchet: {0}: {1} {2} has count {3!r}".format(label, metric, name, count))
    return metrics, exceptions


def run_counter(root):
    """The counts as {metric: {name: count}} from the Go counter, run over the repository's server/."""
    return run_counter_in(Path(root) / "server")


def run_counter_in(server):
    """The same, over a server/ directory; the counter's own source is always this checkout's."""
    server = Path(server).resolve()
    with tempfile.TemporaryDirectory(prefix="wire-types-") as temp:
        out = Path(temp) / "counts.json"
        env = dict(os.environ, WIRE_COUNTS_OUT=str(out), WIRE_COUNTS_ROOT=str(server))
        try:
            result = subprocess.run(["go", "test", "-count=1", "-run", "^TestEmit$", COUNTER_PKG],
                                    cwd=str(Path(__file__).resolve().parents[2] / "server"), capture_output=True,
                                    timeout=GO_TIMEOUT, env=env)
        except FileNotFoundError:
            raise WireTypesError("wire types ratchet: go is not on PATH")
        except subprocess.TimeoutExpired:
            raise WireTypesError("wire types ratchet: the counter timed out after {0} s".format(GO_TIMEOUT))
        if result.returncode != 0 or not out.is_file():
            raise WireTypesError("wire types ratchet: the counter failed: "
                                 + (result.stdout + result.stderr).decode("utf-8", "replace").strip())
        counts = json.loads(out.read_text(encoding="utf-8"))["metrics"]
    return {metric: names for metric, names in counts.items() if names}


def violations(actual, metrics, exceptions):
    """Lines for each name that is new or occurs more often than the baseline (plus its exception) allows."""
    lines = []
    for metric in METRICS:
        allowed = metrics.get(metric, {})
        extra = exceptions.get(metric, {})
        risen = []
        for name, count in sorted(actual.get(metric, {}).items()):
            limit = allowed.get(name, 0) + extra.get(name, 0)
            if count > limit:
                risen.append("{0} ({1} -> {2})".format(name, limit, count) if limit else name + " (new)")
        if risen:
            lines.append("wire types ratchet: {0}: {1} new weak name(s): {2}\n  fix: {3}. There is no ignore marker; "
                         "a weak type that must stay goes in {4} \"exceptions\" with a PR reference.".format(
                             metric, len(risen), ", ".join(risen), FIX[metric], BASELINE_REL))
    return lines


def lowerable(actual, metrics):
    return ["{0} {1}: {2} -> {3}".format(metric, name, count, actual.get(metric, {}).get(name, 0))
            for metric in METRICS for name, count in sorted(metrics.get(metric, {}).items())
            if actual.get(metric, {}).get(name, 0) < count]


def raised_against(candidate, previous, label):
    """Lines for baseline entries above the same entry of a previous baseline, or absent from it."""
    lines = []
    for metric in METRICS:
        for name, count in sorted(candidate.get(metric, {}).items()):
            if count > previous.get(metric, {}).get(name, 0):
                lines.append("wire types ratchet: {0} {1} is {2} in the baseline but {3} at {4}; a baseline never "
                             "rises (an exception needs a PR reference)".format(metric, name, count,
                                                                                  previous.get(metric, {}).get(name, 0), label))
    return lines


def render(metrics, exceptions_text):
    data = {"metrics": {metric: dict(sorted(metrics[metric].items())) for metric in METRICS if metrics.get(metric)}}
    if exceptions_text:
        data["exceptions"] = exceptions_text
    return json.dumps(data, indent=1, sort_keys=False) + "\n"


def _base_metrics(root, base, env):
    try:
        merge_base = ratchet_base.resolve(root, base, env)
        if merge_base is None:
            return None
        text = merge_base.texts([BASELINE_REL]).get(BASELINE_REL)
    except ratchet_base.BaseError as error:
        raise WireTypesError("wire types ratchet: " + str(error))
    return (parse_baseline(text, merge_base.label + ":" + BASELINE_REL)[0], merge_base.label) if text else None


def check(root, base=None, env=None, counter=run_counter, notes=None):
    """Violation lines. Empty means pass; notes receives the lowerable lines."""
    env = os.environ if env is None else env
    path = Path(root) / BASELINE_REL
    if not path.is_file():
        return ["wire types ratchet: baseline missing ({0}); refusing to invent counts".format(BASELINE_REL)]
    metrics, exceptions = parse_baseline(path.read_text(encoding="utf-8"), BASELINE_REL)
    actual = counter(root)
    found = violations(actual, metrics, exceptions)
    if notes is not None:
        notes.extend(lowerable(actual, metrics))
    previous = _base_metrics(root, base, env)
    if previous:
        found.extend(raised_against(metrics, previous[0], previous[1]))
    return found


def update(root, base=None, env=None, counter=run_counter):
    """Lower the baseline to the current counts. Refuses to raise, to add a name, or to create the baseline."""
    env = os.environ if env is None else env
    path = Path(root) / BASELINE_REL
    if not path.is_file():
        raise WireTypesError("wire types ratchet: no baseline at {0}; refusing to create one".format(BASELINE_REL))
    metrics, exceptions = parse_baseline(path.read_text(encoding="utf-8"), BASELINE_REL)
    actual = counter(root)
    refusals = violations(actual, metrics, exceptions)
    if refusals:
        return refusals
    planned = {}
    for metric in METRICS:
        for name, count in metrics.get(metric, {}).items():
            now = min(count, actual.get(metric, {}).get(name, 0))
            if now:
                planned.setdefault(metric, {})[name] = now
    raw = json.loads(path.read_text(encoding="utf-8")).get("exceptions")
    rendered = render(planned, raw)
    if path.read_text(encoding="utf-8") != rendered:
        path.write_text(rendered, encoding="utf-8")
        print("wire types ratchet: lowered the baseline to {0} name(s)".format(sum(len(v) for v in planned.values())))
    else:
        print("wire types ratchet: baseline already matches")
    return []


def summary(metrics):
    return ", ".join("{0} {1}".format(metric, sum(metrics.get(metric, {}).values())) for metric in METRICS)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--base", help="base ref (default: CI_BASE_SHA); the baseline may not rise against its merge base")
    parser.add_argument("--root", default=str(Path(__file__).resolve().parents[2]), help="repository root")
    parser.add_argument("--update", action="store_true", help="lower the baseline; refuses to raise or add a name")
    parser.add_argument("--print", action="store_true", help="print the current counts per metric and exit")
    args = parser.parse_args(argv)
    notes = []
    try:
        if args.print:
            print(summary(run_counter(args.root)))
            return 0
        problems = update(args.root, args.base) if args.update else check(args.root, base=args.base, notes=notes)
    except WireTypesError as error:
        print(error, file=sys.stderr)
        return 2
    if problems:
        print("\n".join(problems), file=sys.stderr)
        return 1
    if not args.update:
        ratchet_base.print_notice("wire types ratchet", notes, "python3 scripts/ci/wire_types_ratchet.py --update")
        metrics = parse_baseline((Path(args.root) / BASELINE_REL).read_text(encoding="utf-8"))[0]
        print("wire types ratchet: none above the baseline ({0})".format(summary(metrics)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
