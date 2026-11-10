//! Points in what the user sees, and the touchscreen's own coordinates.
//!
//! The wire speaks points in the current interface orientation, origin top-left. The touchscreen takes
//! normalized 16-bit coordinates (0 = top / left, 65535 = bottom / right) of the panel as it is built, in
//! portrait. Not verified on a real device: the rotation for the landscape orientations below.

use crate::error::BridgeError;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Orientation {
    Portrait,
    PortraitUpsideDown,
    /// Home button (or home indicator) on the right.
    LandscapeRight,
    /// Home button (or home indicator) on the left.
    LandscapeLeft,
}

impl Orientation {
    pub fn as_str(self) -> &'static str {
        match self {
            Orientation::Portrait => "portrait",
            Orientation::PortraitUpsideDown => "portrait_upside_down",
            Orientation::LandscapeRight => "landscape_right",
            Orientation::LandscapeLeft => "landscape_left",
        }
    }

    pub fn is_landscape(self) -> bool {
        matches!(self, Orientation::LandscapeLeft | Orientation::LandscapeRight)
    }
}

/// The panel, in portrait as built.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Screen {
    pub pixel_width: u32,
    pub pixel_height: u32,
    pub scale: f64,
}

impl Screen {
    /// The size in points of what the user sees in `o`.
    pub fn frame(&self, o: Orientation) -> (f64, f64) {
        let w = f64::from(self.pixel_width) / self.scale;
        let h = f64::from(self.pixel_height) / self.scale;
        if o.is_landscape() { (h, w) } else { (w, h) }
    }
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Point {
    pub x: f64,
    pub y: f64,
}

/// Maps a point the user sees to the touchscreen. Fails when it lies outside the screen.
pub fn to_touch(p: Point, screen: &Screen, o: Orientation) -> Result<(u16, u16), BridgeError> {
    let (fw, fh) = screen.frame(o);
    if !p.x.is_finite() || !p.y.is_finite() || p.x < 0.0 || p.y < 0.0 || p.x > fw || p.y > fh {
        return Err(BridgeError::bad_request(format!(
            "({}, {}) is outside the screen ({fw:.0} × {fh:.0} points)",
            p.x, p.y
        )));
    }
    let (x, y) = (p.x / fw, p.y / fh);
    let (u, v) = match o {
        Orientation::Portrait => (x, y),
        Orientation::PortraitUpsideDown => (1.0 - x, 1.0 - y),
        // The panel's top edge is on the user's left: its top-left corner is at the bottom-left.
        Orientation::LandscapeRight => (1.0 - y, x),
        // The panel's top edge is on the user's right: its top-left corner is at the top-right.
        Orientation::LandscapeLeft => (y, 1.0 - x),
    };
    Ok((normalize(u), normalize(v)))
}

fn normalize(f: f64) -> u16 {
    (f.clamp(0.0, 1.0) * f64::from(u16::MAX)).round() as u16
}

/// The finger's positions for a move from `a` to `b` taking `duration_ms`: one sample per frame (about 60 a
/// second), at least two, ending exactly at `b`.
pub fn path(a: Point, b: Point, duration_ms: u64) -> Vec<Point> {
    let steps = (duration_ms / 16).max(2) as usize;
    (1..=steps)
        .map(|i| {
            let t = i as f64 / steps as f64;
            Point {
                x: a.x + (b.x - a.x) * t,
                y: a.y + (b.y - a.y) * t,
            }
        })
        .collect()
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Direction {
    Up,
    Down,
    Left,
    Right,
}

impl Direction {
    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "up" => Some(Direction::Up),
            "down" => Some(Direction::Down),
            "left" => Some(Direction::Left),
            "right" => Some(Direction::Right),
            _ => None,
        }
    }
}

/// A swipe given by direction: from `origin` (default the center) `distance` points (default half the
/// screen along that axis) the way the finger moves, kept on screen.
pub fn swipe_ends(dir: Direction, origin: Option<Point>, distance: Option<f64>, frame: (f64, f64)) -> (Point, Point) {
    let (fw, fh) = frame;
    let from = origin.unwrap_or(Point {
        x: fw / 2.0,
        y: fh / 2.0,
    });
    let along = match dir {
        Direction::Up | Direction::Down => fh,
        Direction::Left | Direction::Right => fw,
    };
    let d = distance.unwrap_or(along / 2.0);
    let (dx, dy) = match dir {
        Direction::Up => (0.0, -d),
        Direction::Down => (0.0, d),
        Direction::Left => (-d, 0.0),
        Direction::Right => (d, 0.0),
    };
    let to = Point {
        x: (from.x + dx).clamp(0.0, fw),
        y: (from.y + dy).clamp(0.0, fh),
    };
    (from, to)
}

/// How many quarter turns clockwise a screenshot needs so it shows what the user sees: the device may
/// capture the panel as built (portrait) while the interface is landscape. Not verified on a real device.
pub fn screenshot_turns(image_w: u32, image_h: u32, o: Orientation) -> u8 {
    let image_portrait = image_h >= image_w;
    match o {
        Orientation::LandscapeRight if image_portrait => 3,
        Orientation::LandscapeLeft if image_portrait => 1,
        _ => 0,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const PHONE: Screen = Screen {
        pixel_width: 1179,
        pixel_height: 2556,
        scale: 3.0,
    };

    fn pt(x: f64, y: f64) -> Point {
        Point { x, y }
    }

    #[test]
    fn frames_follow_the_orientation() {
        assert_eq!(PHONE.frame(Orientation::Portrait), (393.0, 852.0));
        assert_eq!(PHONE.frame(Orientation::LandscapeLeft), (852.0, 393.0));
    }

    #[test]
    fn portrait_maps_straight() {
        assert_eq!(to_touch(pt(0.0, 0.0), &PHONE, Orientation::Portrait).unwrap(), (0, 0));
        assert_eq!(
            to_touch(pt(393.0, 852.0), &PHONE, Orientation::Portrait).unwrap(),
            (65535, 65535)
        );
        assert_eq!(
            to_touch(pt(196.5, 426.0), &PHONE, Orientation::Portrait).unwrap(),
            (32768, 32768)
        );
        assert_eq!(
            to_touch(pt(0.0, 0.0), &PHONE, Orientation::PortraitUpsideDown).unwrap(),
            (65535, 65535)
        );
    }

    #[test]
    fn landscape_rotates_onto_the_panel() {
        // Home on the right: the user's bottom-left corner is the panel's top-left.
        assert_eq!(
            to_touch(pt(0.0, 393.0), &PHONE, Orientation::LandscapeRight).unwrap(),
            (0, 0)
        );
        assert_eq!(
            to_touch(pt(0.0, 0.0), &PHONE, Orientation::LandscapeRight).unwrap(),
            (65535, 0)
        );
        // Home on the left: the user's top-right corner is the panel's top-left.
        assert_eq!(
            to_touch(pt(852.0, 0.0), &PHONE, Orientation::LandscapeLeft).unwrap(),
            (0, 0)
        );
        assert_eq!(
            to_touch(pt(852.0, 393.0), &PHONE, Orientation::LandscapeLeft).unwrap(),
            (65535, 0)
        );
    }

    #[test]
    fn rejects_points_off_screen() {
        assert!(to_touch(pt(-1.0, 10.0), &PHONE, Orientation::Portrait).is_err());
        assert!(to_touch(pt(10.0, 900.0), &PHONE, Orientation::Portrait).is_err());
        assert!(to_touch(pt(f64::NAN, 10.0), &PHONE, Orientation::Portrait).is_err());
        // 800 fits across a landscape screen, not a portrait one.
        assert!(to_touch(pt(800.0, 10.0), &PHONE, Orientation::Portrait).is_err());
        assert!(to_touch(pt(800.0, 10.0), &PHONE, Orientation::LandscapeLeft).is_ok());
    }

    #[test]
    fn paths_end_where_asked() {
        let p = path(pt(0.0, 0.0), pt(100.0, 50.0), 500);
        assert_eq!(p.len(), 31);
        assert_eq!(*p.last().unwrap(), pt(100.0, 50.0));
        assert_eq!(path(pt(0.0, 0.0), pt(1.0, 1.0), 0).len(), 2);
    }

    #[test]
    fn swipes_default_to_half_the_screen_from_the_center() {
        let (a, b) = swipe_ends(Direction::Up, None, None, (400.0, 800.0));
        assert_eq!((a, b), (pt(200.0, 400.0), pt(200.0, 0.0)));
        let (_, b) = swipe_ends(Direction::Right, Some(pt(350.0, 10.0)), Some(200.0), (400.0, 800.0));
        assert_eq!(b, pt(400.0, 10.0));
        assert_eq!(Direction::parse("sideways"), None);
    }

    #[test]
    fn turns_a_portrait_capture_of_a_landscape_screen() {
        assert_eq!(screenshot_turns(1179, 2556, Orientation::Portrait), 0);
        assert_eq!(screenshot_turns(1179, 2556, Orientation::LandscapeRight), 3);
        assert_eq!(screenshot_turns(1179, 2556, Orientation::LandscapeLeft), 1);
        assert_eq!(screenshot_turns(2556, 1179, Orientation::LandscapeLeft), 0);
    }
}
