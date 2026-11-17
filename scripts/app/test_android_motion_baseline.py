import io
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest import mock

from android_motion_baseline import (
    baseline_of,
    count,
    literal_hits,
    main,
    regressions,
    scan,
    update_raises,
)
from android_token_baseline import code_only


class MotionBaselineTest(unittest.TestCase):
    def test_counts_numeric_tween_spring_and_default_visibility(self):
        source = '''
            // tween(900)
            val a = tween(1100, easing = LinearEasing)
            val b = tween(Tokens.Motion.State.duration)
            val c = spring(dampingRatio = Spring.DampingRatioNoBouncy, stiffness = Spring.StiffnessMediumLow)
            val d = IosSpring(300, 0.25f).spec<Float>()
            val e = SharedMotion.reveal<Float>()
            AnimatedVisibility(visible = open) { Text("x") }
            AnimatedVisibility(visible = open, enter = fadeIn(), exit = fadeOut()) { Text("y") }
            Crossfade(targetState = tab) { Text(it) }
            Crossfade(targetState = tab, animationSpec = tween(200)) { Text(it) }
            snap()
            snap(0)
        '''
        kinds = [kind for _, kind in literal_hits(code_only(source))]
        # Order is by line; Crossfade's animationSpec = tween(200) counts as a tween on that line.
        self.assertEqual(
            ['tween', 'spring', 'AnimatedVisibility-default', 'Crossfade-default', 'tween', 'snap'],
            kinds,
        )
        self.assertEqual(6, count(source))

    def test_counts_generic_tween_spring_snap_type_args(self):
        """Glass.kt:227 uses tween<Float>(900); spring/snap may carry a type argument too."""
        source = '''
            val a = tween<Float>(900)
            val b = tween<IntOffset>(durationMillis = 200)
            val c = spring<Float>(dampingRatio = 0.5f, stiffness = 400f)
            val d = snap<Float>(0)
            val e = tween(Tokens.Motion.Reveal.duration)
            val f = spring<Float>(dampingRatio = Tokens.Motion.State.bounce)
        '''
        kinds = [kind for _, kind in literal_hits(code_only(source))]
        self.assertEqual(['tween', 'tween', 'spring', 'snap'], kinds)

    def test_counts_durationMillis_named_arg(self):
        source = 'val a = tween(durationMillis = 200)\nval b = tween(duration = 180)\n'
        kinds = [kind for _, kind in literal_hits(code_only(source))]
        self.assertEqual(['tween', 'tween'], kinds)

    def test_counts_animateAsState_without_animationSpec(self):
        source = '''
            val a by animateFloatAsState(if (pressed) 0.96f else 1f, label = "press")
            val b by animateDpAsState(if (on) 10.dp else 0.dp, label = "thumb")
            val c by animateFloatAsState(target, tween(150), label = "key")
            val d by animateFloatAsState(target, spec, label = "amber")
            val e by animateFloatAsState(target, animationSpec = SharedMotion.reveal(), label = "x")
        '''
        kinds = [kind for _, kind in literal_hits(code_only(source))]
        self.assertEqual(['animateAsState-default', 'animateAsState-default', 'tween'], kinds)

    def test_counts_animateContentSize_without_spec(self):
        source = '''
            Modifier.animateContentSize()
            Modifier.animateContentSize(alignment = Alignment.Top)
            Modifier.animateContentSize(PersonaTheme.motion.size())
            Modifier.animateContentSize(animationSpec = SharedMotion.reveal())
        '''
        kinds = [kind for _, kind in literal_hits(code_only(source))]
        self.assertEqual(['animateContentSize-default', 'animateContentSize-default'], kinds)

    def test_counts_animatedContent_without_transitionSpec(self):
        source = '''
            AnimatedContent(targetState = tab) { Text(it) }
            AnimatedContent(
                targetState = action,
                transitionSpec = { fadeIn() togetherWith fadeOut() },
                label = "composer-action",
            ) { shown -> Text("$shown") }
        '''
        kinds = [kind for _, kind in literal_hits(code_only(source))]
        self.assertEqual(['AnimatedContent-default'], kinds)

    def test_new_file_or_rise_fails(self):
        baseline = baseline_of({'version': 1, 'files': {'a.kt': 2}})
        self.assertEqual({'version': 1, 'files': {'a.kt': 2}}, baseline)
        self.assertEqual(1, len(regressions({'files': {'a.kt': 3}}, baseline)))
        self.assertEqual(1, len(regressions({'files': {'b.kt': 1}}, baseline)))
        self.assertEqual([], regressions({'files': {'a.kt': 1}}, baseline))
        self.assertEqual([], regressions({'files': {}}, baseline))

    def test_update_baseline_refuses_raise_or_new_file(self):
        baseline = {'version': 1, 'files': {'a.kt': 2}}
        self.assertEqual(
            ['a.kt: 3 literal animation specs, baseline 2'],
            update_raises({'files': {'a.kt': 3}}, baseline),
        )
        self.assertEqual(
            ['b.kt: new file with 1 literal animation specs'],
            update_raises({'files': {'b.kt': 1}}, baseline),
        )
        self.assertEqual([], update_raises({'files': {'a.kt': 1}}, baseline))
        self.assertEqual([], update_raises({'files': {}}, baseline))

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = root / 'baseline.json'
            base.write_text('{"version": 1, "files": {"android/app/src/main/java/a/App.kt": 1}}\n')
            (root / 'android/app/src/main/java/a').mkdir(parents=True)
            (root / 'android/app/src/main/java/a/App.kt').write_text('val a = tween(180)\nval b = tween(200)\n')
            with mock.patch('sys.argv', ['android_motion_baseline.py', '--root', str(root),
                                         '--baseline', str(base), '--update-baseline']):
                buf = io.StringIO()
                with redirect_stdout(buf):
                    code = main()
            self.assertEqual(1, code)
            self.assertIn('Refusing to raise', buf.getvalue())
            self.assertIn('App.kt', buf.getvalue())
            # Baseline unchanged.
            self.assertEqual(1, __import__('json').loads(base.read_text())['files'][
                'android/app/src/main/java/a/App.kt'])

    def test_update_baseline_accepts_reduction(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = root / 'baseline.json'
            base.write_text('{"version": 1, "files": {"android/app/src/main/java/a/App.kt": 2}}\n')
            (root / 'android/app/src/main/java/a').mkdir(parents=True)
            (root / 'android/app/src/main/java/a/App.kt').write_text('val a = tween(180)\n')
            with mock.patch('sys.argv', ['android_motion_baseline.py', '--root', str(root),
                                         '--baseline', str(base), '--update-baseline']):
                buf = io.StringIO()
                with redirect_stdout(buf):
                    code = main()
            self.assertEqual(0, code)
            self.assertIn('motion baseline written', buf.getvalue())
            self.assertEqual(1, __import__('json').loads(base.read_text())['files'][
                'android/app/src/main/java/a/App.kt'])

    def test_scan_covers_main_sources_not_tests_or_tokens(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            files = {
                'android/app/src/main/java/a/App.kt': 'val a = tween(180)',
                'android/core/designsystem/src/main/kotlin/d/Parts.kt': 'val c = tween<Float>(900)',
                'android/core/designsystem/src/main/kotlin/persona/core/designsystem/Tokens.kt': 'val c = tween(1)',
                'android/feature/inbox/src/main/kotlin/i/Inbox.kt': 'AnimatedVisibility(visible = true) {}',
                'android/feature/inbox/src/test/kotlin/i/InboxTest.kt': 'val t = tween(500)',
            }
            for name, text in files.items():
                (root / name).parent.mkdir(parents=True, exist_ok=True)
                (root / name).write_text(text)
            current = scan(root)
        self.assertEqual(
            {
                'android/app/src/main/java/a/App.kt': 1,
                'android/core/designsystem/src/main/kotlin/d/Parts.kt': 1,
                'android/feature/inbox/src/main/kotlin/i/Inbox.kt': 1,
            },
            current['files'],
        )


if __name__ == '__main__':
    unittest.main()
