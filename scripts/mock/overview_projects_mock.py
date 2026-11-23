"""Overview by project in the mock hub (#13019, docs/web/web.md "Overview by project"), a domain of
scripts/mock/mock_domains.py with no switch of its own: with MOCK_KB_TREE=1 and MOCK_TODO_SORT=1 together, the
knowledge base's projects stay the project tree's (project_tree_mock.py; the sort's reset had replaced them with its
three flat ones) and the sort's fictional to-dos get company on the tree's projects and two marked Temporary, so
Overview's cards, Temporary and Unsorted all have something to show. MOCK_WATCH_CARDS=1 adds the watch cards (on the
same project slugs) and MOCK_QUESTION_RAIL=1 the main chat's questions. Every name is made up.
scripts/mock/test_overview_projects_mock.py drives it.
"""
import os

import project_tree_mock
import todo_sort_mock

HUB = None
ON = bool(os.environ.get("MOCK_KB_TREE")) and bool(os.environ.get("MOCK_TODO_SORT"))
HELP = "MOCK_KB_TREE=1 with MOCK_TODO_SORT=1  Overview by project: the tree's projects with to-dos on them, Temporary and Unsorted ones (fictional; #13019)"

# id, title, project, temporary, done
TODOS = [
    ("todo_p1", "Fix the crash on older phones", "garden-app", False, False),
    ("todo_p2", "Pick the rain threshold for skipping a reminder", "watering-reminders", False, False),
    ("todo_p3", "Ask testers about the reminder times", "watering-reminders", False, False),
    ("todo_p4", "Write the calendars for the last two regions", "seed-planner", False, True),
    ("todo_p5", "Export the press kit photos", "press-kit", False, False),
    ("todo_p6", "Confirm the launch date with the stores", "q4-launch", False, False),
    ("todo_p7", "Rewrite the store listing's first line", "store-listing", False, False),
    ("todo_t1", "Renew the domain name", "", True, False),
    ("todo_t2", "Return the borrowed tripod", "", True, False),
]


def reset():
    project_tree_mock.reset()
    for i, (tid, title, project, temporary, done) in enumerate(TODOS):
        t = todo_sort_mock.todo(tid, title, project, len(todo_sort_mock.TODOS) + i)
        if temporary:
            t["temporary"] = True
        if done:
            t["done_at"] = HUB.ago(hours=2)
        HUB.TODOS.append(t)


def register(d):
    global HUB
    HUB = d.hub
    d.help(HELP)
    if not ON:
        return
    d.on_reset(reset)
    reset()
