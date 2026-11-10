//! Text and named keys as HID Keyboard/Keypad usages (US layout), and the hardware buttons.

use crate::error::{BridgeError, Code};

pub const LEFT_SHIFT: u64 = 0xE1;

/// One key press: its usage, and whether Shift is held for it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Stroke {
    pub usage: u64,
    pub shift: bool,
}

const fn plain(usage: u64) -> Stroke {
    Stroke { usage, shift: false }
}

const fn shifted(usage: u64) -> Stroke {
    Stroke { usage, shift: true }
}

/// The key that types `c` on a US keyboard, `None` for anything else.
pub fn stroke_for(c: char) -> Option<Stroke> {
    Some(match c {
        'a'..='z' => plain(0x04 + (c as u64 - 'a' as u64)),
        'A'..='Z' => shifted(0x04 + (c as u64 - 'A' as u64)),
        '1'..='9' => plain(0x1E + (c as u64 - '1' as u64)),
        '0' => plain(0x27),
        '\n' => plain(0x28),
        '\t' => plain(0x2B),
        ' ' => plain(0x2C),
        '!' => shifted(0x1E),
        '@' => shifted(0x1F),
        '#' => shifted(0x20),
        '$' => shifted(0x21),
        '%' => shifted(0x22),
        '^' => shifted(0x23),
        '&' => shifted(0x24),
        '*' => shifted(0x25),
        '(' => shifted(0x26),
        ')' => shifted(0x27),
        '-' => plain(0x2D),
        '_' => shifted(0x2D),
        '=' => plain(0x2E),
        '+' => shifted(0x2E),
        '[' => plain(0x2F),
        '{' => shifted(0x2F),
        ']' => plain(0x30),
        '}' => shifted(0x30),
        '\\' => plain(0x31),
        '|' => shifted(0x31),
        ';' => plain(0x33),
        ':' => shifted(0x33),
        '\'' => plain(0x34),
        '"' => shifted(0x34),
        '`' => plain(0x35),
        '~' => shifted(0x35),
        ',' => plain(0x36),
        '<' => shifted(0x36),
        '.' => plain(0x37),
        '>' => shifted(0x37),
        '/' => plain(0x38),
        '?' => shifted(0x38),
        _ => return None,
    })
}

/// All strokes for `text`, or which character cannot be typed.
pub fn strokes(text: &str) -> Result<Vec<Stroke>, BridgeError> {
    text.chars()
        .map(|c| {
            stroke_for(c).ok_or_else(|| {
                BridgeError::new(
                    Code::Unsupported,
                    format!("cannot type {c:?}: only ASCII text can be typed for now"),
                )
            })
        })
        .collect()
}

/// A named key of `keys`.
pub fn named_key(name: &str) -> Option<u64> {
    Some(match name {
        "return" => 0x28,
        "escape" => 0x29,
        "backspace" => 0x2A,
        "tab" => 0x2B,
        "space" => 0x2C,
        "delete" => 0x4C,
        "right" => 0x4F,
        "left" => 0x50,
        "down" => 0x51,
        "up" => 0x52,
        _ => return None,
    })
}

/// A hardware button: HID Consumer page usage, and how long a plain press holds it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Button {
    pub usage_page: u64,
    pub usage: u64,
    pub press_ms: u64,
}

pub fn button(name: &str) -> Option<Button> {
    const CONSUMER: u64 = 0x0C;
    let (usage, press_ms) = match name {
        "home" => (0x40, 80),
        "lock" => (0x30, 200),
        "volume_up" => (0xE9, 80),
        "volume_down" => (0xEA, 80),
        _ => return None,
    };
    Some(Button {
        usage_page: CONSUMER,
        usage,
        press_ms,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn types_letters_digits_and_symbols() {
        assert_eq!(stroke_for('a'), Some(plain(0x04)));
        assert_eq!(stroke_for('Z'), Some(shifted(0x1D)));
        assert_eq!(stroke_for('1'), Some(plain(0x1E)));
        assert_eq!(stroke_for('0'), Some(plain(0x27)));
        assert_eq!(stroke_for('?'), Some(shifted(0x38)));
        assert_eq!(stroke_for('\n'), Some(plain(0x28)));
        assert_eq!(strokes("Hi!").unwrap().len(), 3);
    }

    #[test]
    fn every_printable_ascii_character_has_a_key() {
        for c in (0x20u8..0x7F).map(char::from) {
            assert!(stroke_for(c).is_some(), "{c:?}");
        }
    }

    #[test]
    fn refuses_what_it_cannot_type() {
        let err = strokes("héllo").unwrap_err();
        assert_eq!(err.code, Code::Unsupported);
        assert!(err.message.contains("'é'"));
    }

    #[test]
    fn knows_named_keys_and_buttons() {
        assert_eq!(named_key("return"), Some(0x28));
        assert_eq!(named_key("f13"), None);
        assert_eq!(button("home").unwrap().usage, 0x40);
        assert_eq!(button("lock").unwrap().press_ms, 200);
        assert!(button("siri").is_none());
    }
}
