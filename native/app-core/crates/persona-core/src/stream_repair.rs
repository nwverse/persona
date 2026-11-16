//! Fence-only projection of Apple's `StreamRepair.repair` and `StreamRepair.end`.
//!
//! This deliberately does not implement tables, inline markup, math blocks, rendering or `plainOpenFence`.

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Deserialize)]
pub struct Input {
    pub text: String,
}

#[derive(Debug, Clone, Serialize)]
pub struct Projection {
    pub repaired: String,
    pub end: &'static str,
}

#[derive(Debug, Clone)]
struct Fence {
    character: char,
    count: usize,
    line: usize,
}

fn trimmed(line: &str) -> &str {
    line.trim_matches([' ', '\t', '\r'])
}

fn opening(line: &str) -> Option<(char, usize)> {
    let character = line.chars().next()?;
    if !matches!(character, '`' | '~') {
        return None;
    }
    let count = line.chars().take_while(|next| *next == character).count();
    if count < 3 || character == '`' && line.chars().skip(count).any(|next| next == '`') {
        return None;
    }
    Some((character, count))
}

fn closes(fence: &Fence, line: &str) -> bool {
    let count = line.chars().take_while(|next| *next == fence.character).count();
    count >= fence.count && line.chars().skip(count).all(|next| matches!(next, ' ' | '\t'))
}

fn without_indent_so_far(text: &str) -> Option<&str> {
    let trimmed = text.trim_end_matches([' ', '\t']);
    if trimmed.len() != text.len() && (trimmed.is_empty() || trimmed.ends_with('\n')) {
        Some(trimmed)
    } else {
        None
    }
}

fn project_repair(text: &str) -> String {
    if text.is_empty() {
        return String::new();
    }
    if let Some(shorter) = without_indent_so_far(text) {
        return project_repair(shorter);
    }
    let lines = text.split('\n').collect::<Vec<_>>();
    let mut fence: Option<Fence> = None;
    for (index, line) in lines.iter().enumerate() {
        let trimmed = trimmed(line);
        if let Some(open) = &fence {
            if closes(open, trimmed) {
                fence = None;
            }
            continue;
        }
        if let Some((character, count)) = opening(trimmed) {
            fence = Some(Fence {
                character,
                count,
                line: index,
            });
        }
    }
    let Some(open) = fence else {
        return text.to_owned();
    };
    let indent = lines[open.line]
        .chars()
        .take_while(|character| matches!(character, ' ' | '\t'))
        .collect::<String>();
    let marker = format!("{indent}{}", open.character.to_string().repeat(open.count));
    if !text.ends_with('\n') {
        if open.line == lines.len() - 1 {
            let mut prefix = lines[..lines.len() - 1].join("\n");
            if open.line > 0 {
                prefix.push('\n');
            }
            return prefix;
        }
        let last = trimmed(lines[lines.len() - 1]);
        if !last.is_empty() && last.chars().all(|character| character == open.character) {
            let mut prefix = lines[..lines.len() - 1].join("\n");
            prefix.push('\n');
            return format!("{prefix}{marker}");
        }
    }
    format!("{text}{}{marker}", if text.ends_with('\n') { "" } else { "\n" })
}

fn project_end(text: &str) -> &'static str {
    if text.is_empty() {
        return "text";
    }
    if let Some(shorter) = without_indent_so_far(text) {
        return project_end(shorter);
    }
    let lines = text.split('\n').collect::<Vec<_>>();
    let mut fence: Option<(char, usize)> = None;
    let mut closed_last = false;
    for (index, line) in lines.iter().enumerate() {
        let trimmed = trimmed(line);
        if let Some((character, count)) = fence {
            if closes(
                &Fence {
                    character,
                    count,
                    line: 0,
                },
                trimmed,
            ) {
                fence = None;
                closed_last = index + 1 == lines.len();
            }
            continue;
        }
        if let Some(open) = opening(trimmed) {
            fence = Some(open);
            continue;
        }
    }
    if fence.is_some() || closed_last && !text.ends_with('\n') {
        "code"
    } else {
        "text"
    }
}

/// Repairs only an open fence and reports whether this fence-only slice sees prose or code at the end.
pub fn project(input: &Input) -> Projection {
    Projection {
        repaired: project_repair(&input.text),
        end: project_end(&input.text),
    }
}
