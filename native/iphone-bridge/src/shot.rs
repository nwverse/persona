//! Turning what the phone captured into the screenshot asked for: upright, at most `max_dimension`, in the
//! requested format. The phone's bytes pass through untouched when nothing needs changing.

use std::io::Cursor;

use image::{DynamicImage, ImageFormat as Format, ImageReader};

use crate::error::BridgeError;
use crate::geometry::{Orientation, Screen, screenshot_turns};
use crate::ops::{ImageFormat, ShotOptions};

pub struct Prepared {
    pub bytes: Vec<u8>,
    pub mime: &'static str,
    pub width: u32,
    pub height: u32,
}

fn unreadable(e: impl std::fmt::Display) -> BridgeError {
    BridgeError::internal(format!("screenshot from the phone is not a readable image: {e}"))
}

fn reader(bytes: &[u8]) -> Result<ImageReader<Cursor<&[u8]>>, BridgeError> {
    ImageReader::new(Cursor::new(bytes))
        .with_guessed_format()
        .map_err(unreadable)
}

pub fn dimensions(bytes: &[u8]) -> Result<(u32, u32), BridgeError> {
    reader(bytes)?.into_dimensions().map_err(unreadable)
}

/// The panel as a capture shows it, when the phone does not describe its screen: portrait, and the scale
/// iPhones use for that width (2 up to 828 pixels wide, 3 above).
pub fn screen_from_capture(w: u32, h: u32) -> Screen {
    let (pixel_width, pixel_height) = (w.min(h), w.max(h));
    Screen {
        pixel_width,
        pixel_height,
        scale: if pixel_width > 828 { 3.0 } else { 2.0 },
    }
}

pub fn prepare(raw: Vec<u8>, opts: &ShotOptions, o: Orientation) -> Result<Prepared, BridgeError> {
    let r = reader(&raw)?;
    let source = r.format();
    let (w, h) = r.into_dimensions().map_err(unreadable)?;
    let (want, mime) = match opts.format {
        ImageFormat::Png => (Format::Png, "image/png"),
        ImageFormat::Jpeg => (Format::Jpeg, "image/jpeg"),
    };
    let turns = screenshot_turns(w, h, o);
    let too_big = opts.max_dimension > 0 && w.max(h) > opts.max_dimension;
    if turns == 0 && !too_big && source == Some(want) {
        return Ok(Prepared {
            bytes: raw,
            mime,
            width: w,
            height: h,
        });
    }
    let mut img = reader(&raw)?.decode().map_err(unreadable)?;
    img = match turns {
        1 => img.rotate90(),
        2 => img.rotate180(),
        3 => img.rotate270(),
        _ => img,
    };
    if too_big {
        img = img.resize(
            opts.max_dimension,
            opts.max_dimension,
            image::imageops::FilterType::Triangle,
        );
    }
    let (width, height) = (img.width(), img.height());
    let mut bytes = Vec::new();
    let encoded = match want {
        Format::Jpeg => {
            let rgb = DynamicImage::ImageRgb8(img.to_rgb8());
            let enc = image::codecs::jpeg::JpegEncoder::new_with_quality(&mut bytes, opts.quality);
            rgb.write_with_encoder(enc)
        }
        _ => img.write_to(&mut Cursor::new(&mut bytes), Format::Png),
    };
    encoded.map_err(|e| BridgeError::internal(format!("encode screenshot: {e}")))?;
    Ok(Prepared {
        bytes,
        mime,
        width,
        height,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use image::{Rgba, RgbaImage};

    /// A made-up capture: red on top, so a turn shows where the top went.
    fn capture(w: u32, h: u32, format: Format) -> Vec<u8> {
        let img = RgbaImage::from_fn(w, h, |_, y| {
            if y < h / 4 {
                Rgba([255, 0, 0, 255])
            } else {
                Rgba([0, 0, 255, 255])
            }
        });
        let mut out = Vec::new();
        DynamicImage::ImageRgba8(img)
            .to_rgb8()
            .write_to(&mut Cursor::new(&mut out), format)
            .unwrap();
        out
    }

    fn opts(format: ImageFormat, max: u32) -> ShotOptions {
        ShotOptions {
            format,
            quality: 80,
            max_dimension: max,
            out_file: None,
        }
    }

    #[test]
    fn passes_through_when_nothing_changes() {
        let raw = capture(40, 80, Format::Png);
        let p = prepare(raw.clone(), &opts(ImageFormat::Png, 0), Orientation::Portrait).unwrap();
        assert_eq!(p.bytes, raw);
        assert_eq!((p.width, p.height, p.mime), (40, 80, "image/png"));
    }

    #[test]
    fn converts_scales_and_turns() {
        let raw = capture(40, 80, Format::Png);
        let p = prepare(raw.clone(), &opts(ImageFormat::Jpeg, 0), Orientation::Portrait).unwrap();
        assert_eq!(p.mime, "image/jpeg");
        assert_eq!(dimensions(&p.bytes).unwrap(), (40, 80));

        let p = prepare(raw.clone(), &opts(ImageFormat::Png, 40), Orientation::Portrait).unwrap();
        assert_eq!((p.width, p.height), (20, 40));

        // Home on the right: the panel's top ends up on the user's left.
        let p = prepare(raw, &opts(ImageFormat::Png, 0), Orientation::LandscapeRight).unwrap();
        assert_eq!((p.width, p.height), (80, 40));
        let img = image::load_from_memory(&p.bytes).unwrap().to_rgb8();
        assert_eq!(img.get_pixel(2, 20).0, [255, 0, 0]);
        assert_eq!(img.get_pixel(77, 20).0, [0, 0, 255]);
    }

    #[test]
    fn rejects_garbage() {
        assert!(
            prepare(
                b"not an image".to_vec(),
                &opts(ImageFormat::Png, 0),
                Orientation::Portrait
            )
            .is_err()
        );
    }

    #[test]
    fn guesses_the_scale() {
        assert_eq!(screen_from_capture(2556, 1179).scale, 3.0);
        assert_eq!(screen_from_capture(750, 1334).scale, 2.0);
        assert_eq!(screen_from_capture(2556, 1179).pixel_width, 1179);
    }
}
