"""native/app-core/scripts/build-apple.sh: the shared XCFramework cache and the simulator-only build (#10713).

Runs the real script in disposable checkouts with fake cargo, rustup, rustc, xcrun and xcodebuild on PATH, so it
needs no Rust toolchain or Xcode and runs on Linux too.
"""
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / 'native/app-core/scripts/build-apple.sh'
SIM_TARGET = {'arm64': 'aarch64-apple-ios-sim', 'aarch64': 'aarch64-apple-ios-sim', 'x86_64': 'x86_64-apple-ios'}.get(
    platform.machine())
HOST = 'aarch64-apple-darwin'

FAKES = {
    # Records each build and leaves a static library named after its target where the script looks for it.
    'cargo': '''#!/bin/sh
target=
while [ $# -gt 0 ]; do [ "$1" = --target ] && target=$2; shift; done
echo "$target" >> "$FAKE_LOG/cargo"
mkdir -p "$CARGO_TARGET_DIR/$target/release"
echo "lib $target" > "$CARGO_TARGET_DIR/$target/release/libpersona_core.a"
''',
    'rustup': '''#!/bin/sh
echo "$*" >> "$FAKE_LOG/rustup"
''',
    'rustc': '''#!/bin/sh
echo "host: %s"
''' % HOST,
    'xcrun': '''#!/bin/sh
case "$*" in
  *--show-sdk-build-version*) echo "${FAKE_SDK_BUILD:-23A100}" ;;
  *--show-sdk-path*) echo /fake/sdk ;;
esac
''',
    # -create-xcframework: one folder per library, named after the target, and an Info.plist.
    'xcodebuild': '''#!/bin/sh
[ "$1" = -create-xcframework ] || exit 0
shift
output=
libraries=
while [ $# -gt 0 ]; do
  case "$1" in
    -library) libraries="$libraries $2"; shift ;;
    -headers) shift ;;
    -output) output=$2; shift ;;
  esac
  shift
done
[ ! -e "$output" ] || { echo "exists: $output" >&2; exit 1; }
mkdir -p "$output"
for library in $libraries; do
  slice=$(basename "$(dirname "$(dirname "$library")")")
  mkdir -p "$output/$slice"
  cp "$library" "$output/$slice/"
done
echo plist > "$output/Info.plist"
echo "$output" >> "$FAKE_LOG/xcframework"
''',
}


@unittest.skipUnless(SIM_TARGET and shutil.which('shasum'), 'needs shasum and an arm64 or x86_64 machine')
class AppleCoreBuildTests(unittest.TestCase):
    def setUp(self):
        self.temp = Path(tempfile.mkdtemp(prefix='persona-core-apple-test-'))
        self.addCleanup(shutil.rmtree, self.temp)
        self.cache = self.temp / 'cache'
        self.logs = self.temp / 'logs'
        self.logs.mkdir()
        bin_dir = self.temp / 'bin'
        bin_dir.mkdir()
        for name, body in FAKES.items():
            (bin_dir / name).write_text(body)
            (bin_dir / name).chmod(0o755)
        self.env = {
            'PATH': '%s:/usr/bin:/bin' % bin_dir,
            'HOME': str(self.temp / 'home'),
            'TMPDIR': str(self.temp),
            'FAKE_LOG': str(self.logs),
            'PERSONA_APP_CORE_CACHE': str(self.cache),
        }

    def checkout(self, name, source='pub fn core() {}\n'):
        """A checkout with the files the script hashes, the script itself among them."""
        root = self.temp / name
        core = root / 'native/app-core'
        (core / 'scripts').mkdir(parents=True)
        shutil.copy2(SCRIPT, core / 'scripts/build-apple.sh')
        shutil.copy2(ROOT / 'native/app-core/rust-toolchain.toml', core / 'rust-toolchain.toml')
        (core / 'Cargo.toml').write_text('[workspace]\n')
        (core / 'Cargo.lock').write_text('# lock\n')
        for crate in ('persona-core', 'persona-core-runtime', 'persona-core-ffi'):
            (core / 'crates' / crate / 'src').mkdir(parents=True)
            (core / 'crates' / crate / 'src/lib.rs').write_text(source)
        (core / 'crates/persona-core-ffi/include').mkdir()
        (core / 'crates/persona-core-ffi/include/persona_core.h').write_text('int persona_core(void);\n')
        (root / 'apple/Packages/PersonaCore').mkdir(parents=True)
        return root

    def run_script(self, root, *args, **env):
        result = subprocess.run(['bash', str(root / 'native/app-core/scripts/build-apple.sh'), *args],
                                env={**self.env, **env}, capture_output=True, text=True, timeout=120)
        return result

    def build(self, root, *args, **env):
        result = self.run_script(root, *args, **env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result.stdout

    def built(self):
        """The targets cargo built since the last call."""
        log = self.logs / 'cargo'
        targets = log.read_text().split() if log.exists() else []
        log.unlink(missing_ok=True)
        return targets

    def framework(self, root):
        dest = root / 'apple/Packages/PersonaCore/build/PersonaCoreC.xcframework'
        slices = sorted(p.name for p in dest.iterdir() if p.is_dir())
        stamp = (root / 'apple/Packages/PersonaCore/build/Inputs.stamp').read_text().split()
        return slices, stamp

    def entries(self):
        return sorted(p.name for p in self.cache.iterdir()) if self.cache.exists() else []

    def test_a_second_worktree_at_the_same_inputs_copies_the_cached_framework(self):
        first, second = self.checkout('first'), self.checkout('second')
        out = self.build(first, '--simulator')
        self.assertEqual(self.built(), [SIM_TARGET], 'a simulator request builds only its slice')
        self.assertIn('XCFramework (simulator) ready', out)
        self.assertEqual(self.framework(first), ([SIM_TARGET], [self.entries()[0], 'simulator']))

        out = self.build(first, '--simulator')
        self.assertIn('XCFramework is current', out)
        self.assertEqual(self.built(), [])

        # Another checkout path: the key holds paths relative to the checkout, so it is the same entry.
        out = self.build(second, '--simulator')
        self.assertEqual(self.built(), [], 'the second worktree must reuse the cached framework')
        self.assertIn('from the shared cache', out)
        self.assertEqual(self.framework(second), self.framework(first))
        self.assertEqual(len(self.entries()), 1)

    def test_a_full_request_builds_every_slice_and_serves_later_simulator_requests(self):
        first, second = self.checkout('first'), self.checkout('second')
        self.build(first, '--simulator')
        self.built()
        # The worktree has only the simulator slice: a full request (release, CI, swift test) builds all three.
        self.build(first)
        self.assertEqual(sorted(self.built()), sorted(['aarch64-apple-ios', SIM_TARGET, HOST]))
        slices, stamp = self.framework(first)
        self.assertEqual(slices, sorted(['aarch64-apple-ios', SIM_TARGET, HOST]))
        self.assertEqual(stamp[1], 'all')
        # A full worktree copy satisfies a simulator request as it is.
        self.assertIn('XCFramework is current', self.build(first, '--simulator'))
        # Another worktree asking for everything gets the full entry without building.
        self.build(second)
        self.assertEqual(self.built(), [])
        self.assertEqual(self.framework(second), self.framework(first))
        key = self.entries()[0]
        self.assertEqual(sorted(p.name for p in (self.cache / key).iterdir()), ['all', 'simulator'])

    def test_a_simulator_request_takes_a_full_entry_when_it_is_the_only_one(self):
        first, second = self.checkout('first'), self.checkout('second')
        self.build(first)
        self.built()
        out = self.build(second, '--simulator')
        self.assertEqual(self.built(), [])
        self.assertIn('XCFramework (all) from the shared cache', out)
        self.assertEqual(self.framework(second)[1][1], 'all')

    def test_changed_inputs_build_again_and_only_the_newest_keys_stay(self):
        first = self.checkout('first')
        # Something else in the cache folder is neither counted nor removed.
        (self.cache / 'notes').mkdir(parents=True)
        self.build(first, '--simulator', PERSONA_APP_CORE_CACHE_KEEP='2')
        old = [name for name in self.entries() if name != 'notes']
        for n in range(2):
            (first / 'native/app-core/crates/persona-core/src/lib.rs').write_text('pub fn core() { %d }\n' % n)
            self.build(first, '--simulator', PERSONA_APP_CORE_CACHE_KEEP='2')
            self.assertEqual(self.built(), [SIM_TARGET, SIM_TARGET] if n == 0 else [SIM_TARGET])
        self.assertEqual(len(self.entries()), 3)
        self.assertIn('notes', self.entries())
        self.assertNotIn(old[0], self.entries(), 'the oldest key is pruned')
        self.assertIn(self.framework(first)[1][0], self.entries())

    def test_the_environment_that_changes_the_output_is_part_of_the_key(self):
        first = self.checkout('first')
        self.build(first, '--simulator')
        self.build(first, '--simulator', FAKE_SDK_BUILD='24A200')
        self.build(first, '--simulator', RUSTFLAGS='-C opt-level=1')
        self.assertEqual(self.built(), [SIM_TARGET] * 3)
        self.assertEqual(len(self.entries()), 3)

    def test_the_cache_can_be_turned_off(self):
        first, second = self.checkout('first'), self.checkout('second')
        self.build(first, '--simulator', PERSONA_APP_CORE_CACHE='0')
        self.build(second, '--simulator', PERSONA_APP_CORE_CACHE='0')
        self.assertEqual(self.built(), [SIM_TARGET, SIM_TARGET])
        self.assertFalse(self.cache.exists())
        self.assertFalse((self.temp / 'home').exists(), 'nothing is written to the default cache either')

    def test_unknown_arguments_and_a_bad_keep_count_are_refused(self):
        first = self.checkout('first')
        self.assertEqual(self.run_script(first, '--device').returncode, 2)
        result = self.run_script(first, PERSONA_APP_CORE_CACHE_KEEP='0')
        self.assertEqual(result.returncode, 1)
        self.assertIn('PERSONA_APP_CORE_CACHE_KEEP', result.stderr)
        self.assertEqual(self.built(), [])

    def test_no_scratch_is_left_behind(self):
        first, second = self.checkout('first'), self.checkout('second')
        self.build(first, '--simulator')
        self.build(second)
        for root in (first, second):
            self.assertEqual(sorted(p.name for p in (root / 'apple/Packages/PersonaCore/build').iterdir()),
                             ['Inputs.stamp', 'PersonaCoreC.xcframework'])
        for entry in self.cache.iterdir():
            self.assertEqual(sorted(p.name for p in entry.iterdir()), ['all', 'simulator'])
        self.assertEqual([p.name for p in self.temp.iterdir() if p.name.startswith('persona-core-apple-headers')], [])


if __name__ == '__main__':
    unittest.main()
