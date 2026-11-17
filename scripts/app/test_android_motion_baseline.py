import tempfile
import unittest
from pathlib import Path

from android_motion_baseline import baseline_of, count, literal_hits, regressions, scan
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
        self.assertEqual(
            ['tween', 'tween', 'spring', 'snap', 'AnimatedVisibility-default', 'Crossfade-default'],
            kinds,
        )
        self.assertEqual(6, count(source))

    def test_new_file_or_rise_fails(self):
        baseline = baseline_of({'version': 1, 'files': {'a.kt': 2}})
        self.assertEqual({'version': 1, 'files': {'a.kt': 2}}, baseline)
        self.assertEqual(1, len(regressions({'files': {'a.kt': 3}}, baseline)))
        self.assertEqual(1, len(regressions({'files': {'b.kt': 1}}, baseline)))
        self.assertEqual([], regressions({'files': {'a.kt': 1}}, baseline))
        self.assertEqual([], regressions({'files': {}}, baseline))

    def test_scan_covers_main_sources_not_tests_or_tokens(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            files = {
                'android/app/src/main/java/a/App.kt': 'val a = tween(180)',
                'android/core/designsystem/src/main/kotlin/d/Parts.kt': 'val c = tween(900)',
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
