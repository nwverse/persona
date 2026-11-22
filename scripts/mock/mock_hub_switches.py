#!/usr/bin/env python3
"""The MOCK_* switches scripts/mock/mock_hub.py reads, frozen (#12737).

mock_hub.py is the file nearly every mock change edits, and most of what it has gained came with a new MOCK_* switch,
so it takes no new one. The names it may read are the ones in mock_hub_switches.txt beside this file; test_mock_hub.py
fails on any other, and on an environment read whose name it cannot see. A new switch, or a new value on an existing
one, goes into a domain module on the registry, scripts/mock/mock_domains.py (docs/operations/testing.md "Mock hub
domains"). A switch that moves into a domain module comes off the list; until then the test prints a notice.

    python3 scripts/mock/mock_hub_switches.py pending            # the names open PRs add to mock_hub.py (gh, git)
    python3 scripts/mock/mock_hub_switches.py pending --write    # the whole list rewritten, the day the freeze lands

`pending` reads each open PR's head, fetched from origin by its commit (objects only: no ref or FETCH_HEAD is written),
and reports the names its mock_hub.py reads that its merge base with --base did not, leaving out the ones --base reads.
A draft whose head commit is more than --idle-days old is left out.

`--write` rewrites both parts of the list on the day the freeze lands, after origin/main is merged in: the names --base
reads, then the pending ones. A listed pending name --base now reads joins the first part (its PR merged), one whose PRs
have all closed comes off, one whose PR is still open stays, and the names found now are added. Once the list is on
--base it writes nothing: it is not how a later PR gets a name on the list.
"""
import argparse
import ast
from datetime import date, datetime, timedelta, timezone
import json
from pathlib import Path
import re
import subprocess
import sys

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
HUB = HERE / "mock_hub.py"
LIST = HERE / "mock_hub_switches.txt"
HUB_PATH = "scripts/mock/mock_hub.py"  # in the repository, for git show
LIST_PATH = "scripts/mock/mock_hub_switches.txt"

NAME = re.compile(r"MOCK_[A-Z0-9_]+")
ENTRY = re.compile(r"(?P<name>\S+)(?:[ \t]+(?P<prs>#\d+(?:[ \t]+#\d+)*))?")
KEYED = {"get", "setdefault", "pop"}  # os.environ.<method>(name, ...)
COPIES = {"copy", "items"}  # the whole environment, passed on to a child process
HINT = ('mock_hub.py takes no new MOCK_* switch (scripts/mock/mock_hub_switches.txt). Put the switch, and what it '
        'serves, in a domain module: register(d) and one line in DOMAINS in scripts/mock/mock_domains.py '
        '(docs/operations/testing.md "Mock hub domains"). A new value on an existing switch is a new switch too: '
        'register the variant in a domain module whose route answers first and returns False otherwise.')


class Reads:
    """What a mock_hub.py source reads from the environment: names ({MOCK_* name: its first line}) and unseen
    ([(line, code)]: a read whose name is not a string literal, or a use of os.environ this cannot follow)."""

    def __init__(self, names, unseen):
        self.names, self.unseen = names, unseen


def reads(source):
    """The MOCK_* names `source` reads through os.environ.get / setdefault / pop, os.environ[...], os.getenv or a
    membership test on os.environ, and any other string that is a whole MOCK_* name (a copy of the environment read
    later, a helper given the name). An `import os as …` or `from os import environ, getenv` is followed."""
    tree = ast.parse(source)
    modules, environs, getenvs = {"os"}, set(), set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            modules |= {alias.asname or alias.name for alias in node.names if alias.name == "os"}
        elif isinstance(node, ast.ImportFrom) and node.module == "os":
            environs |= {alias.asname or alias.name for alias in node.names if alias.name == "environ"}
            getenvs |= {alias.asname or alias.name for alias in node.names if alias.name == "getenv"}

    def named(node, attr, aliases):
        """node is os.environ / os.getenv (attr), or a name it was imported as."""
        if isinstance(node, ast.Attribute):
            return node.attr == attr and isinstance(node.value, ast.Name) and node.value.id in modules
        return isinstance(node, ast.Name) and node.id in aliases

    names, unseen = {}, []

    def code(node):
        text = ast.unparse(node)
        return text if len(text) <= 160 else text[:157] + "..."

    def found(node):
        names[node.value] = min(names.get(node.value, node.lineno), node.lineno)

    def key(node, use):
        """The name a read takes, recorded when it is a literal MOCK_* one; unseen when it is not a literal."""
        if not (isinstance(node, ast.Constant) and isinstance(node.value, str)):
            unseen.append((use.lineno, code(use)))
        elif node.value.startswith("MOCK_"):
            found(node)

    # One pass that carries each node's parent: mock_hub.py has about 340,000 nodes, and a map of every node's parent
    # took several times as long as this.
    stack = [(tree, None)]
    while stack:
        node, parent = stack.pop()
        stack.extend((child, node) for child in ast.iter_child_nodes(node))
        if isinstance(node, ast.Constant):
            if isinstance(node.value, str) and NAME.fullmatch(node.value):
                found(node)
        elif isinstance(node, ast.Attribute) and named(node.value, "environ", environs):
            # os.environ.<attr>: a keyed read, a copy of the whole environment, or something this cannot follow.
            called = isinstance(parent, ast.Call) and parent.func is node
            if called and node.attr in KEYED:
                key(parent.args[0] if parent.args else None, parent)
            elif not (called and node.attr in COPIES and not parent.args) and node.attr != "update":
                unseen.append((node.lineno, code(parent if called else node)))
        elif named(node, "environ", environs):
            if isinstance(parent, ast.Attribute) and parent.value is node:
                continue  # os.environ.<attr>, above
            if isinstance(parent, ast.Subscript) and parent.value is node:
                key(parent.slice, parent)
            elif isinstance(parent, ast.Compare) and any(c is node for c in parent.comparators):
                at = next(i for i, c in enumerate(parent.comparators) if c is node)
                if isinstance(parent.ops[at], (ast.In, ast.NotIn)):
                    key(parent.left if at == 0 else parent.comparators[at - 1], parent)
                else:
                    unseen.append((node.lineno, code(parent)))
            elif not (isinstance(parent, ast.Call) and isinstance(parent.func, ast.Name) and parent.func.id == "dict"
                      and len(parent.args) == 1 and parent.args[0] is node and not parent.keywords
                      or isinstance(parent, ast.keyword) and parent.arg == "env"
                      or isinstance(parent, ast.Dict) and any(v is node and k is None
                                                              for k, v in zip(parent.keys, parent.values))):
                # Not a copy for a child process (dict(os.environ), env=os.environ, {**os.environ}).
                unseen.append((node.lineno, code(parent)))
        elif named(node, "getenv", getenvs):
            if isinstance(parent, ast.Call) and parent.func is node:
                key(parent.args[0] if parent.args else None, parent)
            else:
                unseen.append((node.lineno, code(parent)))
    return Reads(names, sorted(set(unseen)))


class Entry:
    """A line of the list: the name, its line and, for a pending one, the open PRs that added it (["#4512"])."""

    def __init__(self, line, name, prs):
        self.line, self.name, self.prs = line, name, prs


def read_list(text):
    """(entries, problems) of mock_hub_switches.txt's text. Comment lines start with #; an entry is one MOCK_* name, and
    a pending one is followed by its PRs (MOCK_X #4512). Names stay sorted and unique within each part, the names
    mock_hub.py read when the freeze landed and then the pending ones."""
    entries, problems = [], []
    for number, raw in enumerate(text.splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        match = ENTRY.fullmatch(line)
        if match is None or not NAME.fullmatch(match["name"]):
            problems.append("line %d is neither a MOCK_* name nor a pending one with its PRs (MOCK_X #123): %r"
                            % (number, raw))
            continue
        entries.append(Entry(number, match["name"], (match["prs"] or "").split()))
    seen = {}
    for entry in entries:
        if entry.name in seen:
            problems.append("line %d repeats %s (line %d)" % (entry.line, entry.name, seen[entry.name]))
        seen.setdefault(entry.name, entry.line)
    first_pending = next((i for i, e in enumerate(entries) if e.prs), len(entries))
    problems += ["line %d (%s) has no PR after the pending entries: those come last" % (e.line, e.name)
                 for e in entries[first_pending:] if not e.prs]
    for part in (entries[:first_pending], entries[first_pending:]):
        problems += ["line %d (%s) is out of order: it belongs before %s (keep the names, and the pending ones, sorted)"
                     % (e.line, e.name, before.name) for before, e in zip(part, part[1:]) if e.name < before.name]
    return entries, problems


def check(source, text):
    """(failures, notices) of a mock_hub.py source against the list's text."""
    found = reads(source)
    entries, problems = read_list(text)
    listed = {e.name: e for e in entries}
    failures = ["mock_hub_switches.txt: " + problem for problem in problems]
    failures += ["mock_hub.py:%d reads %s, which is not on the list. %s" % (line, name, HINT)
                 for name, line in sorted(found.names.items(), key=lambda item: item[1]) if name not in listed]
    failures += ["mock_hub.py:%d reads the environment without a literal name, so the list cannot be checked: %s. "
                 "Name the switch as a literal (os.environ.get(\"MOCK_X\")); for a new one: %s" % (line, text, HINT)
                 for line, text in found.unseen]
    notices = ["mock_hub.py no longer reads %s: delete line %d of scripts/mock/mock_hub_switches.txt" % (e.name, e.line)
               for e in entries if e.name not in found.names and not e.prs]
    waiting = ["%s (%s)" % (e.name, " ".join(e.prs)) for e in entries if e.name not in found.names and e.prs]
    if waiting:
        notices.append("pending, not read by mock_hub.py yet (delete a line once its PRs have closed): "
                       + ", ".join(waiting))
    return failures, notices


# ---- pending: the names open PRs add ----

QUERY = """
query($owner: String!, $repo: String!, $endCursor: String) {
  repository(owner: $owner, name: $repo) {
    pullRequests(states: OPEN, first: 40, after: $endCursor) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number isDraft headRefOid
        commits(last: 1) { nodes { commit { committedDate } } }
        files(first: 100) { totalCount nodes { path } }
      }
    }
  }
}
"""


def run(args, timeout, tries=1):
    """args' standard output; the last error after `tries` attempts (GitHub drops connections now and then)."""
    for attempt in range(tries):
        try:
            return subprocess.run(args, cwd=ROOT, check=True, capture_output=True, text=True, timeout=timeout).stdout
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
            if attempt == tries - 1:
                raise RuntimeError("%s failed: %s" % (" ".join(args[:3]), getattr(error, "stderr", "") or error))


def open_prs():
    """Each open PR: (number, draft, head commit, head commit's date, maybe touches mock_hub.py)."""
    out = run(["gh", "api", "graphql", "--paginate", "-F", "owner={owner}", "-F", "repo={repo}", "-f",
               "query=" + QUERY, "--jq", ".data.repository.pullRequests.nodes[]"], 300, tries=3)
    prs = []
    for pr in map(json.loads, out.splitlines()):
        files = pr["files"]
        # A PR with more files than one page lists may touch mock_hub.py: read its head to see.
        touches = files["totalCount"] > len(files["nodes"]) or any(f["path"] == HUB_PATH for f in files["nodes"])
        committed = datetime.fromisoformat(pr["commits"]["nodes"][0]["commit"]["committedDate"].replace("Z", "+00:00"))
        prs.append((pr["number"], pr["isDraft"], pr["headRefOid"], committed, touches))
    return prs


def names_at(revision):
    """The MOCK_* names mock_hub.py reads at a commit, or None when it has no mock_hub.py there."""
    try:
        source = run(["git", "show", "%s:%s" % (revision, HUB_PATH)], 60)
    except RuntimeError:
        return None
    try:
        return set(reads(source).names)
    except SyntaxError:
        return None


def pending(base, today, prs, idle_days, log=sys.stderr):
    """{name: [PR numbers]}: the names each counted PR of `prs` (open_prs()) reads at its head that its merge base with
    base did not, without the ones base reads (`today`)."""
    now = datetime.now(timezone.utc)
    counted = []
    for number, draft, head, committed, touches in prs:
        if not touches:
            continue
        if draft and now - committed > timedelta(days=idle_days):
            print("#%d: left out, a draft whose head commit is from %s" % (number, committed.date()), file=log)
        else:
            counted.append((number, head))
    for start in range(0, len(counted), 20):
        run(["git", "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin"]
            + [head for _, head in counted[start:start + 20]], 600, tries=3)
    found = {}
    for number, head in counted:
        merge_base = run(["git", "merge-base", head, base], 60).strip()
        added = (names_at(head) or set()) - (names_at(merge_base) or set()) - today
        for name in added:
            found.setdefault(name, []).append(number)
        print("#%d: %s" % (number, " ".join(sorted(added)) or "no new name"), file=log)
    return {name: sorted(numbers) for name, numbers in sorted(found.items())}


PENDING_HEADER = """\
# Pending: the names the PRs open when the freeze landed were adding to mock_hub.py, each with its PRs
# (python3 scripts/mock/mock_hub_switches.py pending --write, {day}; drafts idle more than {idle} days left
# out). A name stays when its PR merges; delete it once its PRs have all closed unmerged. No PR opened later gets a
# line here."""


def rewrite(text, today, found, still_open, day, idle_days):
    """The list's text rewritten as the freeze lands: its leading comment, the names mock_hub.py reads at the base
    (`today`), then the pending entries. Those are the listed pending names the base does not read whose PRs are not
    all closed, with the PRs still open (`still_open`, a set of numbers), and `found`'s ({name: [PR numbers]}). A listed
    pending name the base reads is one of its names now, since its PR merged, so no name the base reads is dropped."""
    lead = []
    for line in text.splitlines():
        if line.strip() and not line.lstrip().startswith("#"):
            break
        lead.append(line)
    while lead and not lead[-1].strip():
        lead.pop()
    waiting = {}
    for entry in read_list(text)[0]:
        open_ones = {int(pr[1:]) for pr in entry.prs} & still_open
        if entry.prs and entry.name not in today and open_ones:
            waiting[entry.name] = open_ones
    for name, numbers in found.items():
        if name not in today:
            waiting.setdefault(name, set()).update(numbers)
    lines = lead + sorted(today)
    if waiting:
        lines += [""] + PENDING_HEADER.format(day=day, idle=idle_days).splitlines()
        lines += ["%s %s" % (name, " ".join("#%d" % n for n in sorted(waiting[name]))) for name in sorted(waiting)]
    return "\n".join(lines) + "\n"


def changes(old, new):
    """What a rewrite changed, for the person landing the freeze: one line for each part of the list."""
    def parts(text):
        entries = read_list(text)[0]
        return {e.name for e in entries if not e.prs}, {e.name for e in entries if e.prs}

    (old_read, old_waiting), (new_read, new_waiting) = parts(old), parts(new)

    def line(label, before, after):
        moved = ["+" + name for name in sorted(after - before)] + ["-" + name for name in sorted(before - after)]
        return "%s: %d" % (label, len(after)) + "".join(" " + name for name in moved)

    return [line("names mock_hub.py reads", old_read, new_read), line("pending", old_waiting, new_waiting)]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    sub = parser.add_subparsers(dest="command", required=True)
    cmd = sub.add_parser("pending", help="the names open PRs add to mock_hub.py")
    cmd.add_argument("--base", default="origin/main", help="what the names are counted against (default origin/main)")
    cmd.add_argument("--idle-days", type=int, default=14, help="a draft whose head commit is older is left out")
    cmd.add_argument("--write", action="store_true",
                     help="rewrite mock_hub_switches.txt, both parts, the day the freeze lands")
    args = parser.parse_args(argv)
    text = LIST.read_text()
    if args.write:
        if subprocess.run(["git", "cat-file", "-e", "%s:%s" % (args.base, LIST_PATH)], cwd=ROOT,
                          capture_output=True).returncode == 0:
            print("%s has the list already: the freeze has landed, and --write is for the day it lands. Delete the "
                  "lines test_mock_hub.py's notices name by hand." % args.base, file=sys.stderr)
            return 1
        problems = read_list(text)[1]
        if problems:
            print("fix mock_hub_switches.txt first:\n" + "\n".join(problems), file=sys.stderr)
            return 1
    today = names_at(args.base)
    if today is None:
        print("%s has no readable %s" % (args.base, HUB_PATH), file=sys.stderr)
        return 1
    prs = open_prs()
    found = pending(args.base, today, prs, args.idle_days)
    for name, numbers in found.items():
        print("%s %s" % (name, " ".join("#%d" % n for n in numbers)))
    print("pending: %d names from %d PRs" % (len(found), len({n for numbers in found.values() for n in numbers})),
          file=sys.stderr)
    if args.write:
        new = rewrite(text, today, found, {number for number, *_ in prs}, date.today().isoformat(), args.idle_days)
        LIST.write_text(new)
        for line in changes(text, new):
            print("mock_hub_switches.txt, " + line, file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
