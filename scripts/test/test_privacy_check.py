from contextlib import redirect_stderr
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


SCRIPTS = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("privacy_check", SCRIPTS / "privacy_check.py")
pc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pc)

PUBLIC_URL = "https://registry.npmjs.org/sample/-/sample-1.0.0.tgz"
UNAPPROVED_URL = "https://registry.example.invalid/sample/-/sample-1.0.0.tgz"
# Split so the repository scan of this file does not flag it.
PRIVATE_IP = "10." + "20.30.40"


def lockfile(source=PUBLIC_URL, version=3, link=False):
    package = {"version": "1.0.0", "resolved": source}
    if link:
        package["link"] = True
    if version == 1:
        return {"lockfileVersion": version, "dependencies": {"sample": package}}
    return {"lockfileVersion": version, "packages": {"node_modules/sample": package}}


class NpmSourceTests(unittest.TestCase):
    def test_public_registry(self):
        for url in [PUBLIC_URL, PUBLIC_URL.replace("sample/-/sample", "@scope/sample/-/sample"),
                    PUBLIC_URL.replace("registry.npmjs.org", "REGISTRY.NPMJS.ORG:443")]:
            with self.subTest(url=url):
                self.assertTrue(pc.npm_source_allowed(url))

    def test_unapproved_remote_sources(self):
        urls = [UNAPPROVED_URL, PUBLIC_URL.replace("https:", "http:"),
                PUBLIC_URL.replace("registry.npmjs.org", "registry.npmjs.org.example.invalid"),
                PUBLIC_URL.replace("registry.npmjs.org", "registry%2enpmjs.org"),
                PUBLIC_URL.replace("registry.npmjs.org", "registry.npmjs.org:8443"),
                PUBLIC_URL.replace("registry.npmjs.org", "registry.npmjs.org:invalid"),
                PUBLIC_URL.replace("registry.npmjs.org", "user:secret-placeholder" + "@" + "registry.npmjs.org"),
                PUBLIC_URL + "?token=secret-placeholder", PUBLIC_URL + "#secret-placeholder",
                "//registry.example.invalid/sample.tgz", "https://localhost/sample.tgz",
                "git+ssh://git@code.example.invalid/sample.git", "https://[invalid/sample.tgz",
                "https://registry.npmjs.org／registry.example.invalid/sample.tgz"]
        for url in urls:
            with self.subTest(url=url):
                self.assertFalse(pc.npm_source_allowed(url))
                self.assertFalse(pc.npm_source_allowed(url, link=True))

    def test_control_characters_and_backslashes(self):
        for char in ["\n", "\r", "\t", "\0", "\x7f", "\\"]:
            with self.subTest(char=repr(char)):
                self.assertFalse(pc.npm_source_allowed(PUBLIC_URL + char))
                self.assertFalse(pc.npm_source_allowed("https://" + char + "registry.npmjs.org/sample.tgz"))

    def test_relative_file_dependencies(self):
        for source in ["file:../sample", "file:vendor/sample", "file:../space%20name"]:
            with self.subTest(source=source):
                self.assertTrue(pc.npm_source_allowed(source))

    def test_workspace_links(self):
        for source in ["packages/sample", "../sample"]:
            with self.subTest(source=source):
                self.assertFalse(pc.npm_source_allowed(source))
                self.assertTrue(pc.npm_source_allowed(source, link=True))

    def test_file_sources_cannot_hide_remote_or_absolute_paths(self):
        for source in ["file://registry.example.invalid/sample", "file:///sample", "file:/sample",
                       "file:%2f%2fregistry.example.invalid/sample", "file:%5c%5cregistry.example.invalid/sample",
                       "file:%252f%252fregistry.example.invalid/sample", "file:https://registry.example.invalid/sample",
                       "file:../sample?token=secret-placeholder", "file:../sample#secret-placeholder",
                       "//registry.example.invalid/sample", "%2f%2fregistry.example.invalid/sample",
                       "file:../bad%0aname", "file:../bad%7fname"]:
            with self.subTest(source=source):
                self.assertFalse(pc.npm_source_allowed(source, link=True))

    def test_empty_or_non_string_source(self):
        for source in ["", None, False, 1, {}, []]:
            with self.subTest(source=source):
                self.assertFalse(pc.npm_source_allowed(source))


class CamelJoinTests(unittest.TestCase):
    # A made-up listed word that two ordinary words spell when joined in camel case (#3819).
    WORD = "lamp" + "post"

    def test_a_camel_join_is_not_the_word(self):
        for line in ['static let lampPost = "light.max"', "case .open: Theme.Icon.lampPost", "LampPost.shared"]:
            with self.subTest(line=line):
                self.assertEqual(pc.problems(line, [self.WORD]), [])

    def test_the_word_itself_is_still_found(self):
        for line in ["deploy to lamppost", "host LAMPPOST-2", "Lamppost team", "myLamppost", "lamp" "post.example"]:
            with self.subTest(line=line):
                self.assertEqual(pc.problems(line, [self.WORD]), [self.WORD])

    def test_one_plain_use_beside_a_camel_join_is_found(self):
        self.assertEqual(pc.problems("lampPost and lamppost", [self.WORD]), [self.WORD])

    def test_a_word_listed_with_its_camel_step_matches_every_casing(self):
        listed = "Lamp" + "Post"
        for line in ["lampPost", "lamppost", "LAMPPOST"]:
            with self.subTest(line=line):
                self.assertEqual(pc.problems(line, [listed]), [listed])

    def test_words_with_other_characters_are_unchanged(self):
        host = "lamp" + "-post"
        self.assertEqual(pc.problems("at lamp-Post", [host]), [host])


class BannedNameTests(unittest.TestCase):
    # Built from pieces so the repository scan of this file does not flag it.
    NAMES = [("aster", "gate"), ("open", "lark"), ("open", "wechat"), ("extensions", "internal"), ("corp", "link")]

    def spellings(self, a, b):
        return [a + b, a.upper() + b.upper(), a.capitalize() + b.capitalize(), a + "-" + b, a + "_" + b.upper(),
                a.capitalize() + " " + b.capitalize(), "x" + a + b + "y"]

    def test_every_spelling_is_found_without_configuration(self):
        for a, b in self.NAMES:
            for text in self.spellings(a, b):
                with self.subTest(text=text):
                    found = pc.problems("see " + text + " here", [])
                    self.assertEqual(len(found), 1, found)
                    self.assertTrue(pc.private_kind(found[0]).startswith("banned name"))

    def test_the_parts_alone_are_not_names(self):
        for line in ["the open gate", "an aster", "lark and wechat", "internal extensions", "corporate links"]:
            with self.subTest(line=line):
                self.assertEqual([w for w in pc.problems(line, []) if pc.BANNED_NAMES.fullmatch(w)], [])

    def test_a_name_inside_a_longer_word_is_still_found(self):
        name = "open" + "-" + "lark"
        self.assertEqual(pc.problems(name + "s?", []), [name])

    def test_the_pattern_does_not_spell_itself(self):
        self.assertIsNone(pc.BANNED_NAMES.search(pc.BANNED_NAMES.pattern))


class LoopbackTests(unittest.TestCase):
    def test_both_config_and_private_word_profiles_are_scanned(self):
        with tempfile.TemporaryDirectory(prefix="privacy-check-test-") as temp:
            home = Path(temp)
            directory = home / '.config/persona'
            directory.mkdir(parents=True)
            (directory / 'config.json').write_text(json.dumps({'machine_label': 'persona-private-label'}))
            (directory / 'private-words.txt').write_text('persona-private-word\n')
            former = home / '.config' / ('jar' + 'vis')
            former.mkdir()
            (former / 'config.json').write_text(json.dumps({'machine_label': 'former-private-label'}))
            (former / 'private-words.txt').write_text('former-private-word\n')
            with patch.object(pc, "HOME", str(home)), patch.object(pc, "ROOT", str(home)):
                words, _ = pc.terms()
            self.assertIn('persona-private-label', words)
            self.assertIn('persona-private-word', words)
            self.assertIn('former-private-label', words)
            self.assertIn('former-private-word', words)

    def test_loopback_values(self):
        for value in ["127.0.0.1", "127.1.2.3", "::1", "[::1]", "0:0:0:0:0:0:0:1",
                      "127.0.0.1:18661", "http://127.0.0.1:18660", "http://127.0.0.1:18660/x", "https://127.0.0.1/",
                      "[::1]:18660", "http://[::1]:18660/", "localhost:8080", "http://localhost:8080/x",
                      " 127.0.0.1:18661 "]:
            with self.subTest(value=value):
                self.assertTrue(pc.loopback(value))
        for value in [PRIVATE_IP, "192.0.2.1", "0.0.0.0", "hub-host", "", PRIVATE_IP + ":8080",
                      "http://" + PRIVATE_IP + ":9000", "http://" + PRIVATE_IP + ":9000/x", "[fd00::1]:18660",
                      "hub-host:18660", "http://hub-host:18660", "127.0.0.1:port", "127.0.0.1:99999",
                      "user@127.0.0.1", "ssh://user@127.0.0.1:22", "localhost.example.com:8080"]:
            with self.subTest(value=value):
                self.assertFalse(pc.loopback(value))

    def test_local_notes_loopback_with_port_is_not_a_word(self):
        with tempfile.TemporaryDirectory(prefix="privacy-check-test-") as temp:
            home = Path(temp)
            spans = ["127.0.0.1:18661", "http://127.0.0.1:18660", "[::1]:18660", PRIVATE_IP + ":8080",
                     "http://" + PRIVATE_IP + ":9000"]
            (home / "CLAUDE.local.md").write_text("".join("- `" + span + "`\n" for span in spans))
            with patch.object(pc, "HOME", str(home)), patch.object(pc, "ROOT", str(home)):
                words, _ = pc.terms()
        for span in spans[:3]:
            self.assertNotIn(span, words)
        self.assertIn(PRIVATE_IP + ":8080", words)
        self.assertIn("http://" + PRIVATE_IP + ":9000", words)
        self.assertEqual(pc.problems("db at 127.0.0.1:18661, hub at http://127.0.0.1:18660/admin", words), [])
        self.assertTrue(pc.problems("db at " + PRIVATE_IP + ":8080", words))

    def test_config_loopback_is_not_a_word(self):
        with tempfile.TemporaryDirectory(prefix="privacy-check-test-") as temp:
            home = Path(temp)
            config = home / ".config/persona/config.json"
            config.parent.mkdir(parents=True)
            config.write_text(json.dumps({"hub": {"url": "http://127.0.0.1:18660"}, "lan_host": PRIVATE_IP,
                                          "local_ipv6": "[::1]"}))
            with patch.object(pc, "HOME", str(home)), patch.object(pc, "ROOT", str(home)):
                words, _ = pc.terms()
        self.assertNotIn("127.0.0.1", words)
        self.assertNotIn("[::1]", words)
        self.assertIn(PRIVATE_IP, words)
        self.assertEqual(pc.problems("hub at http://127.0.0.1:18660 or [::1]", words), [])
        self.assertIn(PRIVATE_IP, pc.problems("other machine at " + PRIVATE_IP, words))

    def test_standard_xcode_path_is_not_a_word(self):
        with tempfile.TemporaryDirectory(prefix="privacy-check-test-") as temp:
            home = Path(temp)
            config = home / ".config/persona/config.json"
            config.parent.mkdir(parents=True)
            own = "/Users/someone/Apps/Xcode-beta.app"
            config.write_text(json.dumps({"xcode": "/Applications/Xcode.app", "machines": [{"xcode": own}],
                                          "label": "/Applications/Private-Label.app"}))
            with patch.object(pc, "HOME", str(home)), patch.object(pc, "ROOT", str(home)):
                words, _ = pc.terms()
        self.assertNotIn("/Applications/Xcode.app", words)
        self.assertIn(own, words)
        self.assertIn("/Applications/Private-Label.app", words)
        self.assertEqual(pc.problems("sudo xcode-select -s /Applications/Xcode.app", words), [])

    def test_ietf_protocol_block_is_not_private(self):
        self.assertEqual(pc.problems('new Set(["100.100.100.200","168.63.129.16","192.0.0.192"])', []), [])
        self.assertIn(PRIVATE_IP, pc.problems("at " + PRIVATE_IP, []))
        home_lan = "192.168." + "1.10"
        self.assertIn(home_lan, pc.problems("at " + home_lan, []))

    def test_a_period_next_to_an_address_does_not_hide_it(self):
        # A sentence that ends on an address, or an ellipsis before one (#3924).
        for text in ("hub is at %s." % PRIVATE_IP, "hub is at %s..." % PRIVATE_IP, "...%s" % PRIVATE_IP,
                     "(%s.)" % PRIVATE_IP):
            self.assertIn(PRIVATE_IP, pc.problems(text, []), text)
        # A longer dotted run is a version or an OID, not an address.
        for text in ("v%s.5" % PRIVATE_IP, "1.%s" % PRIVATE_IP, "oid 1.3.%s.7" % PRIVATE_IP):
            self.assertEqual(pc.problems(text, []), [], text)


class ReservedDomainTests(unittest.TestCase):
    """Example domains (RFC 2606, RFC 6761) name nobody: a config that holds one as a placeholder must not make every
    me@example.com in the repository private (#6217)."""

    def test_reserved_values(self):
        for value in ["example.com", "https://example.com", "relay.example.com", "https://relay.example.com:443/x",
                      "EXAMPLE.ORG", "hub.Example.Net:18660", "example", "a.example", "foo.test", "bar.invalid",
                      "x.localhost", "example.com/privacy", " https://example.com/ ", "app.example.com",
                      "com.example.persona.test", "registry.example.invalid"]:
            with self.subTest(value=value):
                self.assertTrue(pc.reserved(value))
        # Lookalikes, a user, a bad port, and bundle ids that end in a reserved TLD (they still name their owner).
        for value in ["notexample.com", "example.com.cn", "example.co", "examples.com", "https://example.com.cn/x",
                      "foo.testing", "hub-host", "", PRIVATE_IP, "ssh://user@relay.example.com",
                      "me@example.com", "relay.example.com:port", "com.someone.persona.test", "com.someone.example"]:
            with self.subTest(value=value):
                self.assertFalse(pc.reserved(value))

    def test_config_reserved_domains_are_not_words(self):
        values = {"publish": {"site_url": "https://example.com"},
                  "app": {"push_relay": "relay.example.com", "bundle_id": "com.someone.persona.test"},
                  "android": {"application_id": "com.example.persona.test"},
                  "endpoint": "https://relay.example.com:443/x",
                  "hosts": ["foo.test", "bar.invalid", "notexample.com", "example.com.cn"]}
        with tempfile.TemporaryDirectory(prefix="privacy-check-test-") as temp:
            home = Path(temp)
            config = home / ".config/persona/config.json"
            config.parent.mkdir(parents=True)
            config.write_text(json.dumps(values))
            with patch.object(pc, "HOME", str(home)), patch.object(pc, "ROOT", str(home)):
                words, _ = pc.terms()
        for value in ["example.com", "relay.example.com", "https://relay.example.com:443/x", "foo.test", "bar.invalid",
                      "com.example.persona.test"]:
            self.assertNotIn(value, words)
        for value in ["notexample.com", "example.com.cn", "com.someone.persona.test"]:
            self.assertIn(value, words)
        self.assertEqual(pc.problems("write to me@example.com, see https://relay.example.com/x", words), [])
        self.assertEqual(pc.problems("the hub is notexample.com", words), ["notexample.com"])


class OrdinaryWordTests(unittest.TestCase):
    """A config value that is an ordinary word (a machine id named after a room) matches every variable, comment and
    environment variable of that name. A `!word` line in private-words.txt marks such a value as not private, and every
    hit names where its value comes from, so a false positive can be told from a leak without printing the value (#1065)."""

    WORD = "attic"  # a made-up machine id
    CODE = ['attic := os.Getenv("PERSONA_ATTIC")', "// the attic machine runs the node", "let attic_id = attic"]

    def read(self, read, config, words):
        """`read()` (pc.terms or pc.term_sources) with only this config and private-words.txt on the machine."""
        with tempfile.TemporaryDirectory(prefix="privacy-check-test-") as temp:
            home = Path(temp)
            directory = home / ".config/persona"
            directory.mkdir(parents=True)
            (directory / "config.json").write_text(json.dumps(config))
            (directory / "private-words.txt").write_text(words)
            env = {k: v for k, v in os.environ.items() if k not in ("PERSONA_CONFIG", "JARVIS_CONFIG")}
            env["XDG_CONFIG_HOME"] = str(home / ".config")
            with patch.object(pc, "HOME", str(home)), patch.object(pc, "ROOT", str(home)), \
                    patch.dict(os.environ, env, clear=True):
                return read()[0]

    def words(self, config, words=""):
        return self.read(pc.terms, config, words)

    def sources(self, config, words=""):
        return self.read(pc.term_sources, config, words)

    def machines(self, *ids):
        return {"hub": {"machine": ids[0]}, "machines": [{"id": i, "ssh": i} for i in ids]}

    def test_a_config_word_is_found_without_a_waiver(self):
        words = self.words(self.machines(self.WORD))
        self.assertIn(self.WORD, words)
        for line in self.CODE:
            with self.subTest(line=line):
                self.assertEqual(pc.problems(line, words), [self.WORD])

    def test_a_waived_config_word_is_not_searched(self):
        words = self.words(self.machines(self.WORD, "Cellar"), "# machine ids\n!attic\n !cellar \n")
        self.assertNotIn(self.WORD, words)
        self.assertNotIn("Cellar", words)
        for line in self.CODE + ["the Cellar build"]:
            with self.subTest(line=line):
                self.assertEqual(pc.problems(line, words), [])

    def test_a_waiver_only_takes_back_a_value_made_of_letters(self):
        values = ["attic2", "attic-box", "attic.lan", "/srv/attic", "attic box", "Küche"]
        stderr = io.StringIO()
        with redirect_stderr(stderr):
            words = self.words({"machines": [{"id": v} for v in values]}, "".join("!" + v + "\n" for v in values))
        for value in values:
            with self.subTest(value=value):
                self.assertIn(value, words)
                # A line that is not a waiver keeps what it did before: it is searched as written.
                self.assertIn("!" + value, words)
        self.assertIn("private-words.txt:1:", stderr.getvalue())
        self.assertNotIn("attic", stderr.getvalue())

    def test_a_waiver_does_not_take_back_a_listed_word(self):
        words = self.words(self.machines(self.WORD), "attic\n!attic\n")
        self.assertIn(self.WORD, words)

    def test_every_hit_names_where_its_value_came_from(self):
        config = self.machines(self.WORD)
        config["lan_host"] = "attic.lan"
        config["publish"] = {"site_url": "https://attic.lan/privacy"}
        sources = self.sources(config, "attic-team\n")
        words = list(sources)
        self.assertEqual(sources[self.WORD], ["config hub.machine", "config machines.id", "config machines.ssh"])
        self.assertEqual(sources["attic.lan"], ["config lan_host", "config publish.site_url"])
        self.assertEqual(sources["attic-team"], ["private-words.txt"])
        home = [w for w in words if w.startswith("/Users/")]
        self.assertEqual([sources[w] for w in home], [["the home directory"]])
        self.assertEqual(pc.private_kind(self.WORD, sources),
                         "private value (5 chars) from config hub.machine, config machines.id, config machines.ssh")
        self.assertEqual(pc.private_kind("attic-team", sources), "private value (10 chars) from private-words.txt")
        self.assertEqual(pc.private_kind("other-value", sources), "private value (11 chars)")

    def test_a_key_that_holds_a_value_is_not_printed(self):
        bundle = "com.zorblax.persona"  # a made-up bundle id
        config = self.machines(self.WORD)
        config["app"] = {"bundle_id": bundle}
        config["app_store"] = {"provisioning_profiles": {bundle + ".share": "Zorblax Share Profile"}}
        config["aliases"] = {self.WORD: "zorblax-box", "build.simulator": "zorblax-sim"}
        sources = self.sources(config)
        self.assertEqual(sources["Zorblax Share Profile"], ["config app_store.provisioning_profiles.*"])
        self.assertEqual(sources["zorblax-box"], ["config aliases.*"])
        # The rules that look at a key (os, simulator, url, path) see the whole key, dots and all.
        self.assertEqual(sources["zorblax-sim"], ["config aliases.*"])
        for where in sum(sources.values(), []):
            with self.subTest(where=where):
                self.assertNotIn("zorblax", where)
                self.assertNotIn(self.WORD, where.replace("config ", ""))

    def test_the_waiver_hint_only_follows_a_hit_a_waiver_can_take_back(self):
        config = self.machines(self.WORD, "Cellar")
        config["lan_host"] = "attic.lan"
        config["profile"] = "Zorb Share Prod"
        sources = self.sources(config, "Cellar\n")
        self.assertTrue(pc.waivable(self.WORD, sources))
        for value in ["attic.lan", "Zorb Share Prod", "Cellar", "other"]:
            with self.subTest(value=value):
                self.assertFalse(pc.waivable(value, sources))


class NpmLockTests(unittest.TestCase):
    def test_public_lockfiles(self):
        for version in [1, 2, 3]:
            with self.subTest(version=version):
                self.assertEqual(pc.npm_lock_problems(json.dumps(lockfile(version=version))), [])

    def test_unapproved_lockfiles(self):
        for version in [1, 2, 3]:
            with self.subTest(version=version):
                self.assertEqual(pc.npm_lock_problems(json.dumps(lockfile(UNAPPROVED_URL, version))),
                                 [(1, "unapproved npm source")])

    def test_nested_dependencies_and_both_v2_sections(self):
        nested = lockfile(version=1)
        nested["dependencies"]["sample"]["dependencies"] = {"nested": {"resolved": UNAPPROVED_URL}}
        self.assertEqual(len(pc.npm_lock_problems(json.dumps(nested))), 1)
        combined = lockfile(UNAPPROVED_URL, version=2)
        combined["dependencies"] = nested["dependencies"]
        self.assertEqual(len(pc.npm_lock_problems(json.dumps(combined))), 2)

    def test_file_and_workspace_locks(self):
        self.assertEqual(pc.npm_lock_problems(json.dumps(lockfile("file:../sample"))), [])
        self.assertEqual(pc.npm_lock_problems(json.dumps(lockfile("packages/sample", link=True))), [])
        self.assertTrue(pc.npm_lock_problems(json.dumps(lockfile("packages/sample"))))

    def test_source_line_numbers(self):
        text = json.dumps(lockfile(UNAPPROVED_URL), indent=2)
        expected = next(n for n, line in enumerate(text.splitlines(), 1) if '"resolved"' in line)
        self.assertEqual(pc.npm_lock_problems(text), [(expected, "unapproved npm source")])

    def test_escaped_keys_and_urls(self):
        text = json.dumps(lockfile(UNAPPROVED_URL))
        text = text.replace('"resolved"', '"\\u0072esolved"')
        text = text.replace("registry.example.invalid", "registry\\u002eexample\\u002einvalid")
        self.assertEqual(pc.npm_lock_problems(text), [(1, "unapproved npm source")])

    def test_strings_that_look_like_keys_do_not_change_locations(self):
        data = lockfile(UNAPPROVED_URL)
        data["description"] = '"resolved": "placeholder"'
        self.assertEqual(len(pc.npm_lock_problems(json.dumps(data))), 1)

    def test_malformed_json_and_root(self):
        for text in ["{", "[]", "null", json.dumps(lockfile()) + "\0", '{"resolved": "unterminated}',
                     '{"unused":NaN}', '{"unused":Infinity}', '{"unused":-Infinity}']:
            with self.subTest(text=text):
                self.assertEqual(pc.npm_lock_problems(text), [(1, "invalid npm lockfile")])

    def test_duplicate_keys(self):
        for text in ['{"resolved": "' + UNAPPROVED_URL + '", "resolved": "' + PUBLIC_URL + '"}',
                     '{"name":"first","name":"second"}', '{"name":"first","\\u006eame":"second"}']:
            with self.subTest(text=text):
                self.assertEqual(pc.npm_lock_problems(text), [(1, "invalid npm lockfile")])

    def test_non_string_resolved_fields(self):
        for value in [None, False, 1, {}, []]:
            with self.subTest(value=value):
                self.assertEqual(pc.npm_lock_problems(json.dumps(lockfile(value))),
                                 [(1, "invalid npm resolved field")])

    def test_nested_non_string_resolution_does_not_hide_source(self):
        text = json.dumps(lockfile({"resolved": UNAPPROVED_URL}))
        self.assertEqual(pc.npm_lock_problems(text),
                         [(1, "invalid npm resolved field"), (1, "unapproved npm source")])

    def test_recursion_failure_fails_closed(self):
        text = json.dumps(lockfile())
        with patch.object(pc.json, "loads", side_effect=RecursionError):
            self.assertEqual(pc.npm_lock_problems(text), [(1, "invalid npm lockfile")])
        with patch.object(pc, "npm_resolutions", side_effect=RecursionError):
            self.assertEqual(pc.npm_lock_problems(text), [(1, "invalid npm lockfile")])


class PrivacyCheckIntegrationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="privacy-check-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        self.home = self.root / "home"
        self.repo.mkdir()
        self.home.mkdir()
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_") and k not in {"PYTHONPATH", "PYTHONHOME"}}
        self.env.update({"HOME": str(self.home), "XDG_CONFIG_HOME": str(self.home / ".config"),
                         "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull,
                         "GIT_AUTHOR_NAME": "Test User", "GIT_AUTHOR_EMAIL": "test@example.com",
                         "GIT_COMMITTER_NAME": "Test User", "GIT_COMMITTER_EMAIL": "test@example.com"})
        self.git("init", "-q")
        self.git("config", "core.hooksPath", "scripts/hooks")
        self.git("config", "commit.gpgSign", "false")
        self.git("config", "core.autocrlf", "false")
        (self.repo / "scripts").mkdir()
        shutil.copy2(SCRIPTS / "privacy_check.py", self.repo / "scripts/privacy_check.py")
        shutil.copytree(SCRIPTS / "hooks", self.repo / "scripts/hooks")
        (self.repo / "config.example.json").write_text("{}\n")
        self.git("add", "scripts", "config.example.json")
        self.lock = self.repo / "web/package-lock.json"
        self.lock.parent.mkdir()

    def run_command(self, *args):
        return subprocess.run(args, cwd=self.repo, env=self.env, capture_output=True, text=True, timeout=30)

    def git(self, *args):
        result = self.run_command("git", *args)
        if result.returncode:
            self.fail(result.stdout + result.stderr)
        return result

    def write_lock(self, source=PUBLIC_URL):
        self.lock.write_text(json.dumps(lockfile(source), indent=2) + "\n")

    def scan(self, *args):
        return self.run_command(sys.executable, str(self.repo / "scripts/privacy_check.py"), *args)

    def assert_blocked(self, result, kind="unapproved npm source"):
        self.assertNotEqual(result.returncode, 0)
        output = result.stdout + result.stderr
        self.assertIn(kind, output)
        self.assertNotIn(UNAPPROVED_URL, output)
        self.assertNotIn("registry.example.invalid", output)

    def test_full_scan_blocks_without_private_configuration(self):
        self.write_lock(UNAPPROVED_URL)
        self.git("add", "web/package-lock.json")
        self.assertFalse((self.home / ".config/persona/config.json").exists())
        self.assertFalse((self.home / ".config/persona/private-words.txt").exists())
        self.assert_blocked(self.scan())

    def test_full_scan_blocks_a_banned_name_in_content_and_path(self):
        name = "aster" + "-" + "gate"
        (self.repo / "notes.md").write_text("Reads quotas from " + name.upper() + ".\n")
        (self.repo / (name + ".txt")).write_text("plain\n")
        self.git("add", "notes.md", name + ".txt")
        result = self.scan()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("notes.md:1: banned name", result.stdout)
        self.assertIn(name + ".txt:path: banned name", result.stdout)

    def test_public_full_scan_passes(self):
        self.write_lock()
        self.git("add", "web/package-lock.json")
        result = self.scan()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_config_loopback_is_not_flagged_but_private_ip_is(self):
        config = self.home / ".config/persona/config.json"
        config.parent.mkdir(parents=True)
        config.write_text(json.dumps({"hub": {"url": "http://127.0.0.1:18660"}, "lan_host": PRIVATE_IP}))
        (self.repo / "notes.md").write_text("The hub listens on 127.0.0.1:18660.\n")
        self.git("add", "notes.md")
        result = self.scan()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        (self.repo / "hosts.md").write_text("The other machine is " + PRIVATE_IP + ".\n")
        self.git("add", "hosts.md")
        result = self.scan()
        self.assertNotEqual(result.returncode, 0)
        output = result.stdout + result.stderr
        self.assertIn("hosts.md:1: private IP", output)
        self.assertNotIn("notes.md", output)
        self.assertNotIn(PRIVATE_IP, output)

    def test_local_notes_loopback_with_port_is_not_flagged_but_private_address_is(self):
        (self.repo / "CLAUDE.local.md").write_text("- `127.0.0.1:18661`\n- `http://127.0.0.1:18660`\n- `[::1]:18660`\n"
                                                   "- `" + PRIVATE_IP + ":8080`\n")
        (self.repo / "db.sh").write_text("# default: 127.0.0.1:18661, hub http://127.0.0.1:18660/admin, [::1]:18660\n")
        self.git("add", "db.sh")
        result = self.scan()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        (self.repo / "hosts.md").write_text("The other machine is " + PRIVATE_IP + ":8080.\n")
        self.git("add", "hosts.md")
        result = self.scan()
        self.assertNotEqual(result.returncode, 0)
        output = result.stdout + result.stderr
        self.assertIn("hosts.md:1:", output)
        self.assertNotIn("db.sh", output)
        self.assertNotIn(PRIVATE_IP, output)

    def test_config_example_domains_are_not_flagged_but_other_hosts_are(self):
        config = self.home / ".config/persona/config.json"
        config.parent.mkdir(parents=True)
        config.write_text(json.dumps({"publish": {"site_url": "https://example.com"},
                                      "app": {"push_relay": "relay.example.com"}, "lan_host": "hub.notexample.com"}))
        (self.repo / "notes.md").write_text("Write to me@example.com; see https://example.com and relay.example.com.\n")
        self.git("add", "notes.md")
        result = self.scan()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        (self.repo / "hosts.md").write_text("The hub is hub.notexample.com.\n")
        self.git("add", "hosts.md")
        result = self.scan()
        self.assertNotEqual(result.returncode, 0)
        output = result.stdout + result.stderr
        self.assertIn("hosts.md:1: private value", output)
        self.assertNotIn("notes.md", output)
        self.assertNotIn("notexample", output)

    def test_a_config_word_hit_names_its_key_and_a_waiver_clears_it(self):
        word = "attic"  # a made-up machine id that is an ordinary word (#1065)
        config = self.home / ".config/persona/config.json"
        config.parent.mkdir(parents=True)
        config.write_text(json.dumps({"machines": [{"id": word, "ssh": word}], "lan_host": PRIVATE_IP}))
        (self.repo / "node.go").write_text('attic := os.Getenv("PERSONA_ATTIC") // the attic machine\n')
        (self.repo / "hosts.md").write_text("The other machine is " + PRIVATE_IP + ".\n")
        self.git("add", "node.go", "hosts.md")
        message = self.repo / ".git/test-message"
        message.write_text("Read the attic id from the environment\n")
        result = self.scan("--message", str(message))
        self.assertNotEqual(result.returncode, 0)
        output = result.stdout + result.stderr
        self.assertIn("node.go:1: private value (5 chars) from config machines.id, config machines.ssh", output)
        self.assertIn("commit message:1: private value (5 chars) from config machines.id", output)
        self.assertIn("hosts.md:1: private IP from config lan_host", output)
        self.assertIn("!<word>", result.stderr)
        self.assertNotIn(word, output.lower())
        self.assertNotIn(PRIVATE_IP, output)
        (self.home / ".config/persona/private-words.txt").write_text("!" + word + "\n!" + PRIVATE_IP + "\n")
        result = self.scan("--message", str(message))
        output = result.stdout + result.stderr
        self.assertNotIn("node.go", output)
        self.assertNotIn("commit message", output)
        self.assertIn("hosts.md:1: private IP from config lan_host", output)
        self.assertIn("private-words.txt:2:", result.stderr)
        self.assertNotIn("!<word>", result.stderr)  # what is left (an address) no waiver can take back
        self.assertNotIn(PRIVATE_IP, output)
        (self.repo / "hosts.md").unlink()
        self.git("rm", "-q", "--cached", "hosts.md")
        result = self.scan("--message", str(message))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_staged_unsafe_worktree_safe(self):
        self.write_lock(UNAPPROVED_URL)
        self.git("add", "web/package-lock.json")
        self.write_lock()
        self.assert_blocked(self.scan("--staged"))
        result = self.scan()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_staged_safe_worktree_unsafe(self):
        self.write_lock()
        self.git("add", "web/package-lock.json")
        self.write_lock(UNAPPROVED_URL)
        result = self.scan("--staged")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assert_blocked(self.scan())

    def test_nul_lockfiles_are_not_skipped_as_binary(self):
        self.write_lock()
        with self.lock.open("ab") as file:
            file.write(b"\0")
        self.git("add", "web/package-lock.json")
        self.assert_blocked(self.scan(), "invalid npm lockfile")
        self.assert_blocked(self.scan("--staged"), "invalid npm lockfile")

    def test_invalid_utf8_lockfile_is_rejected(self):
        self.write_lock()
        data = self.lock.read_bytes().replace(b'"version": "1.0.0"', b'"version": "\xff"')
        self.lock.write_bytes(data)
        self.git("add", "web/package-lock.json")
        self.assert_blocked(self.scan(), "invalid npm lockfile")
        self.assert_blocked(self.scan("--staged"), "invalid npm lockfile")

    def test_shrinkwrap_in_another_directory_is_checked(self):
        self.lock = self.repo / "packages/sample/npm-shrinkwrap.json"
        self.lock.parent.mkdir(parents=True)
        self.write_lock(UNAPPROVED_URL)
        self.git("add", "packages")
        self.assert_blocked(self.scan())
        self.assert_blocked(self.scan("--staged"))

    def test_pre_commit_blocks_index_despite_safe_worktree(self):
        self.write_lock(UNAPPROVED_URL)
        self.git("add", "web/package-lock.json")
        self.write_lock()
        result = self.run_command("git", "commit", "-m", "Test dependency source")
        self.assert_blocked(result)
        self.assertNotEqual(self.run_command("git", "rev-parse", "--verify", "HEAD").returncode, 0)

    def test_pre_commit_and_commit_msg_allow_public_lockfile(self):
        self.write_lock()
        self.git("add", "web/package-lock.json")
        self.git("commit", "-m", "Test public dependency source")

    def test_commit_msg_also_checks_staged_lockfile(self):
        self.write_lock(UNAPPROVED_URL)
        self.git("add", "web/package-lock.json")
        self.write_lock()
        message = self.repo / ".git/test-message"
        message.write_text("Test dependency source\n")
        result = self.run_command(str(self.repo / "scripts/hooks/commit-msg"), str(message))
        self.assert_blocked(result)

    def test_commit_msg_keeps_private_word_check_and_redaction(self):
        self.write_lock()
        self.git("add", "web/package-lock.json")
        words = self.home / ".config/persona/private-words.txt"
        words.parent.mkdir(parents=True)
        marker = "private-marker-example"
        words.write_text(marker + "\n")
        result = self.run_command("git", "commit", "-m", "Test " + marker)
        self.assertNotEqual(result.returncode, 0)
        output = result.stdout + result.stderr
        self.assertIn("commit message:1: private value", output)
        self.assertNotIn(marker, output)

    @unittest.skipUnless(shutil.which("npm"), "npm unavailable")
    def test_web_prebuild_blocks_unapproved_lockfile(self):
        shutil.copy2(SCRIPTS.parent / "web/package.json", self.repo / "web/package.json")
        self.write_lock(UNAPPROVED_URL)
        self.git("add", "web")
        result = self.run_command("npm", "--prefix", "web", "run", "prebuild")
        self.assert_blocked(result)


class RangeAndPushTests(unittest.TestCase):
    """--range scans what a branch adds on top of its base (and its messages); pre-push runs it on pushed commits."""

    # The integration tests' temporary repository and helpers, without running their tests again.
    run_command = PrivacyCheckIntegrationTests.run_command
    git = PrivacyCheckIntegrationTests.git
    scan = PrivacyCheckIntegrationTests.scan

    def setUp(self):
        PrivacyCheckIntegrationTests.setUp(self)
        words = self.home / ".config/persona/private-words.txt"
        words.parent.mkdir(parents=True)
        words.write_text("secretbox\n")
        (self.repo / "old.md").write_text("an old note about secretbox\n")  # on master already: not this branch's doing
        self.git("add", "old.md")
        self.git("commit", "-q", "--no-verify", "-m", "base")
        self.git("branch", "-M", "master")
        self.git("update-ref", "refs/remotes/origin/master", "HEAD")
        self.git("checkout", "-q", "-b", "topic")

    def commit(self, name, text, message="change"):
        (self.repo / name).write_text(text)
        self.git("add", name)
        self.git("commit", "-q", "--no-verify", "-m", message)

    def test_range_scans_only_what_the_branch_changes(self):
        self.commit("clean.md", "nothing private here\n")
        result = self.scan("--range", "origin/master..HEAD")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.commit("new.md", "runs on secretbox\n")
        result = self.scan("--range", "origin/master..HEAD")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("new.md:1: private value", result.stdout)
        self.assertNotIn("old.md", result.stdout)
        self.assertNotIn("secretbox", result.stdout + result.stderr)

    def test_range_reads_the_head_not_the_worktree(self):
        self.commit("new.md", "runs on secretbox\n")
        (self.repo / "new.md").write_text("fixed locally, not committed\n")
        self.assertNotEqual(self.scan("--range", "origin/master..HEAD").returncode, 0)

    def test_range_scans_commit_messages(self):
        self.commit("clean.md", "nothing private here\n", message="tested on secretbox")
        result = self.scan("--range", "origin/master..HEAD")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("message:1: private value", result.stdout)

    def test_range_wants_two_ends(self):
        self.assertNotEqual(self.scan("--range", "HEAD").returncode, 0)

    def push_to(self, remote):
        return self.run_command("git", "push", "-q", str(remote), "topic")

    def test_pre_push_refuses_private_values_and_lets_clean_pushes_through(self):
        remote = self.root / "remote.git"
        self.run_command("git", "init", "-q", "--bare", str(remote))
        self.commit("clean.md", "nothing private here\n")
        result = self.push_to(remote)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.commit("new.md", "runs on secretbox\n")
        result = self.push_to(remote)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("pre-push: refused", result.stderr)
        self.assertNotIn("secretbox", result.stdout + result.stderr)


class GitBatchTests(unittest.TestCase):
    """Index/commit reads retain exact git-show bytes without one process per file."""

    run_command = PrivacyCheckIntegrationTests.run_command
    git = PrivacyCheckIntegrationTests.git

    def setUp(self):
        PrivacyCheckIntegrationTests.setUp(self)
        self.payloads = {"notes with spaces.md": b"first\nsecond\n", "empty.txt": b"",
                         "binary.dat": b"head\0body\xff\ntail", "odd\nname.md": b"linebreak filename\n"}
        for name, data in self.payloads.items():
            (self.repo / name).write_bytes(data)
        self.git("add", *self.payloads)
        self.git("commit", "-q", "--no-verify", "-m", "batch fixture")

    def test_exact_index_and_commit_bytes_even_when_worktree_changes(self):
        for prefix in (":", "HEAD:"):
            with self.subTest(prefix=prefix), patch.object(pc, "ROOT", str(self.repo)):
                for name in self.payloads:
                    (self.repo / name).write_bytes(b"worktree changed\n")
                actual = pc.git_file_data(list(self.payloads) + ["missing.txt"], prefix)
                self.assertEqual(actual, self.payloads)
                for name, expected in actual.items():
                    self.assertEqual(pc.git("show", prefix + name), expected)

    def test_regular_paths_use_one_git_process(self):
        names = [name for name in self.payloads if "\n" not in name]
        with patch.object(pc, "ROOT", str(self.repo)), patch.object(pc.subprocess, "run", wraps=subprocess.run) as run:
            self.assertEqual(pc.git_file_data(names, "HEAD:"), {name: self.payloads[name] for name in names})
        self.assertEqual(run.call_count, 1)
        self.assertIn("--batch", run.call_args.args[0])
        self.assertEqual(run.call_args.kwargs["timeout"], 120)

    def test_truncated_or_invalid_batch_output_is_rejected(self):
        for output in (b"", b"invalid\n", b"oid blob 5\nabc\n", b"oid blob 0\nx", b"oid blob 0\n\nextra"):
            with self.subTest(output=output), patch.object(pc.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, output)):
                with self.assertRaises(ValueError):
                    pc.git_file_data(["one.txt"], "HEAD:")

    def test_main_fails_closed_if_batch_read_fails(self):
        with patch.object(pc, "git_file_data", side_effect=ValueError("broken batch")), \
                patch.object(pc, "term_sources", return_value=({}, set())), patch.object(pc, "module_paths", return_value=None), \
                patch.object(pc, "git", return_value=b"one.txt\0"), patch.object(sys, "argv", ["privacy_check", "--staged"]):
            with self.assertRaisesRegex(SystemExit, "scan did not complete"):
                pc.main()


class URLTermTests(unittest.TestCase):
    def test_a_shared_host_keeps_the_path(self):
        self.assertEqual(pc.url_term("https://docs.google.com/document/d/abc123/edit?usp=sharing"),
                         "docs.google.com/document/d/abc123/edit")
        self.assertEqual(pc.url_term("https://docs.google.com/"), "")

    def test_an_own_host_is_the_host(self):
        self.assertEqual(pc.url_term("https://hub.example.test:18660/path"), "hub.example.test")
        self.assertEqual(pc.url_term("someone.github.io/privacy"), "someone.github.io")


if __name__ == "__main__":
    unittest.main()
