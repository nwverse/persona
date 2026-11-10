"""make_persona.py tiers build: a tiered package from each tier's poses (docs/apple/persona.md "Tiered avatars"), offline."""
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "assets" / "make_persona.py"

try:
    import numpy  # noqa: F401
    from PIL import Image
    HAVE_PIL = True
except ImportError:
    HAVE_PIL = False


def load_script():
    spec = importlib.util.spec_from_file_location("make_persona_under_test", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@unittest.skipUnless(HAVE_PIL, "Pillow and numpy draw the frames")
class TiersBuildTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory(prefix="persona-tiers-")
        root = Path(self.dir.name)
        self.work, self.out = root / "work", root / "out" / "demo-tiers"
        mp = load_script()
        # The original placeholder's poses for both tiers (no network, nothing drawn by AI).
        poses = mp.demo_poses(size=320)
        for key in ("shell", "space"):
            (self.work / key / "poses").mkdir(parents=True)
            for name, img in poses.items():
                img.save(self.work / key / "poses" / (name + ".png"))
        self.spec = root / "spec.json"
        self.spec.write_text(json.dumps({
            "id": "demo-tiers", "name": "演示", "about": "演示等级",
            "source": {"kind": "nwverse-board", "url": "https://board.example/board.json"},
            "default": "shell", "center": "weight", "style": "", "identity": "", "refs": [],
            "tiers": [
                {"key": "shell", "name": "蛋", "when": {"newbie": "failed"}, "theme": "hearts",
                 "primary": "#F6C23E", "secondary": "#F3DFB5", "framing": {"*": {"scale": 0.8}}},
                {"key": "space", "name": "宇宙", "when": {"level": 0}, "theme": "cosmic", "motion": "float",
                 "primary": "#8E7CF0", "secondary": "#FAB219", "accent_light": "#5B45D6", "accent_dark": "#8170FF"},
            ],
        }, ensure_ascii=False))

    def tearDown(self):
        self.dir.cleanup()

    def build(self):
        return subprocess.run([sys.executable, str(SCRIPT), "tiers", "build", "--spec", str(self.spec),
                               "--work", str(self.work), "--out", str(self.out)], capture_output=True, text=True)

    def test_builds_a_package_hubs_and_phones_take(self):
        result = self.build()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        root = json.loads((self.out / "persona.json").read_text())
        # Its own manifest is the default tier's (a hub without tiers shows that), plus the tiers.
        self.assertEqual(root["subtitle"], "蛋")
        self.assertEqual(root["tiers"]["default"], "shell")
        self.assertEqual([t["key"] for t in root["tiers"]["levels"]], ["shell", "space"])
        for level in root["tiers"]["levels"]:
            m = json.loads((self.out / level["manifest"]).read_text())
            self.assertEqual((m["id"], m["name"], m["about"], m["subtitle"]), ("demo-tiers", "演示", "演示等级", level["name"]))
            self.assertNotIn("tiers", m)
            # Files named from the package's root, under the tier; no 512 avatar in a package built into personad.
            self.assertEqual(sorted(m["avatar"]["sizes"]), ["128", "256", "64"])
            for f in list(m["avatar"]["sizes"].values()) + [s["file"] for s in m["dot"]["states"].values()]:
                self.assertTrue(f.startswith("tiers/%s/" % level["key"]), f)
                self.assertTrue((self.out / f).is_file(), f)
            # The dot's frames share one palette (a third of the bytes) and still move.
            with Image.open(self.out / m["dot"]["states"]["working"]["file"]) as dot:
                self.assertEqual(dot.mode, "P")
                self.assertGreater(dot.n_frames, 1)
        review = (self.work / "review.html").read_text()
        self.assertIn("蛋", review)
        self.assertIn("宇宙", review)
        self.assertIn("data:image/png;base64,", review)

    def test_weight_centering_keeps_a_tail_in_the_circle(self):
        from PIL import ImageDraw
        mp = load_script()
        # A tadpole: the head on the left, a long tail out to the right.
        img = Image.new("RGBA", (320, 320), (0, 0, 0, 0))
        d = ImageDraw.Draw(img)
        d.ellipse((60, 80, 180, 200), fill=(240, 190, 60, 255))
        d.rectangle((180, 125, 235, 155), fill=(240, 190, 60, 255))
        W = 400  # big enough for the whole of it: what is cut off has no weight to measure

        def across(center):
            placed = mp.affine(img, W, mp.placements({"avatar": img}, W, {}, center)["avatar"])
            return mp.weight_center(placed) / W

        self.assertGreater(across("head"), 0.53)  # the head in the middle, the tail pulls it right
        self.assertAlmostEqual(across("weight"), 0.5, delta=0.01)

    def test_vertical_ground_and_weight(self):
        from PIL import ImageDraw
        mp = load_script()

        def pose(top):
            """A round body sitting on the same floor, its top at `top` (sinking into a shell lowers it)."""
            img = Image.new("RGBA", (320, 320), (0, 0, 0, 0))
            ImageDraw.Draw(img).ellipse((100, top, 220, 260), fill=(240, 190, 60, 255))
            return img
        poses = {"avatar": pose(100), "sleep": pose(180)}
        W = 400
        place = mp.placements(poses, W, {"*": {"scale": 0.8}}, "weight", "ground")
        placed = {n: mp.affine(img, W, place[n]) for n, img in poses.items()}
        bottoms = {n: mp.rows_of(img)[1] for n, img in placed.items()}
        # One ground line for every pose, and the avatar's height centered in the circle.
        self.assertLessEqual(abs(bottoms["avatar"] - bottoms["sleep"]), 2)
        top, bottom = mp.rows_of(placed["avatar"])
        self.assertAlmostEqual((top + bottom) / 2 / W, 0.5, delta=0.01)
        # weight: each pose's weight at the middle.
        place = mp.placements(poses, W, {"*": {"scale": 0.8}}, "weight", "weight")
        for n, img in poses.items():
            self.assertAlmostEqual(mp.weight_center(mp.affine(img, W, place[n]), 1) / W, 0.5, delta=0.01, msg=n)

    def test_motions_keep_frame_zero_near_rest(self):
        mp = load_script()
        for name, motion in mp.MOTIONS.items():
            for state in mp.STATES:
                move = motion(state, 0, 12)
                self.assertLess(abs(move.get("dx", 0)) + abs(move.get("dy", 0)), 0.05, (name, state))

    def test_check_refuses_a_tier_missing_a_file(self):
        self.assertEqual(self.build().returncode, 0)
        (self.out / "tiers" / "space" / "dot" / "idle.png").unlink()
        result = subprocess.run([sys.executable, str(SCRIPT), "check", str(self.out)], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("tier space", result.stderr)


if __name__ == "__main__":
    unittest.main()
