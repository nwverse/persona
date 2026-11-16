//! Fence, table and final-block-marker projection of Apple's `StreamRepair.repair` and its end state.
//!
//! This deliberately does not implement inline markup, math blocks, rendering or `plainOpenFence`.

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

#[derive(Debug, Clone)]
struct Table {
    header: usize,
    divided: bool,
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

fn columns(line: &str) -> usize {
    let mut result = 0;
    for character in line.chars() {
        match character {
            ' ' => result += 1,
            '\t' => result += 4 - result % 4,
            _ => return result,
        }
    }
    result
}

fn indent(line: &str) -> &str {
    let end = line
        .char_indices()
        .find_map(|(index, character)| (!matches!(character, ' ' | '\t')).then_some(index))
        .unwrap_or(line.len());
    &line[..end]
}

fn cell_texts(line: &str) -> Vec<&str> {
    let mut parts = Vec::new();
    let mut start = 0;
    let mut characters = line.char_indices().peekable();
    while let Some((index, character)) = characters.next() {
        if character == '\\' {
            characters.next();
        } else if character == '|' {
            parts.push(&line[start..index]);
            start = index + 1;
        }
    }
    parts.push(&line[start..]);
    if line.starts_with('|') && !parts.is_empty() {
        parts.remove(0);
    }
    if line.ends_with('|') && line.len() > 1 && !parts.is_empty() {
        parts.pop();
    }
    parts
}

fn cells(line: &str) -> usize {
    cell_texts(line).len()
}

fn is_divider(line: &str) -> bool {
    // Foundation `.whitespaces`: Unicode Zs plus tab and ASCII space, not the ASCII newline characters, NEL or
    // line/paragraph separators. Unlike Rust `trim`, this excludes most `char::is_whitespace` scalars.
    let cells = cell_texts(line);
    !cells.is_empty()
        && cells.iter().all(|cell| {
            let mut value = cell.trim_matches(|character: char| {
                character == '\t'
                    || unicode_general_category::get_general_category(character)
                        == unicode_general_category::GeneralCategory::SpaceSeparator
            });
            if let Some(shorter) = value.strip_prefix(':') {
                value = shorter;
            }
            if let Some(shorter) = value.strip_suffix(':') {
                value = shorter;
            }
            !value.is_empty() && value.chars().all(|character| character == '-')
        })
}

fn is_delimiterish(line: &str) -> bool {
    !line.is_empty()
        && line.chars().all(|character| "|-: \t".contains(character))
        && (line.contains('-') || line.contains('|'))
}

fn is_closed_pipe_row(line: &str) -> bool {
    line.len() >= 2 && line.starts_with('|') && line.ends_with('|') && cells(line) > 0
}

fn is_heading(line: &str) -> bool {
    let count = line.chars().take_while(|character| *character == '#').count();
    if !(1..=6).contains(&count) {
        return false;
    }
    line[count..].is_empty() || line[count..].starts_with([' ', '\t'])
}

fn starts_list_item(line: &str) -> bool {
    let Some(first) = line.chars().next() else {
        return false;
    };
    if "-+*".contains(first) {
        return line.len() == 1 || line[1..].starts_with([' ', '\t']);
    }
    let digits = line.bytes().take_while(u8::is_ascii_digit).count();
    if !(1..=9).contains(&digits) {
        return false;
    }
    let rest = &line[digits..];
    if !rest.starts_with(['.', ')']) {
        return false;
    }
    rest.len() == 1 || rest[1..].starts_with([' ', '\t'])
}

fn starts_non_table_block(line: &str) -> bool {
    starts_list_item(line) || line.starts_with('>') || is_heading(line)
}

fn content_column(line: &str) -> usize {
    let value = trimmed(line);
    let marker = if value.starts_with(['-', '+', '*']) {
        1
    } else {
        value.bytes().take_while(u8::is_ascii_digit).count() + 1
    };
    let gap = value[marker..]
        .chars()
        .take_while(|character| *character == ' ')
        .count();
    columns(line) + marker + if (1..=4).contains(&gap) { gap } else { 1 }
}

struct Scan<'a> {
    lines: Vec<&'a str>,
    ends_with_newline: bool,
    fence: Option<Fence>,
    closed_last: bool,
    block_start: usize,
}

impl<'a> Scan<'a> {
    fn new(text: &'a str) -> Self {
        // Apple has no synthetic empty line after a final newline.
        let lines = text.split_terminator('\n').collect::<Vec<_>>();
        let ends_with_newline = text.ends_with('\n');
        let mut fence: Option<Fence> = None;
        let mut closed_last = false;
        let mut block_start = 0;
        for (index, line) in lines.iter().enumerate() {
            let value = trimmed(line);
            if let Some(open) = &fence {
                if closes(open, value) {
                    fence = None;
                    block_start = index + 1;
                    closed_last = index + 1 == lines.len();
                }
                continue;
            }
            if let Some((character, count)) = opening(value) {
                fence = Some(Fence {
                    character,
                    count,
                    line: index,
                });
                block_start = index;
                continue;
            }
            if value.is_empty() {
                block_start = index + 1;
            }
        }
        Self {
            lines,
            ends_with_newline,
            fence,
            closed_last,
            block_start,
        }
    }

    fn prefix_before(&self, line: usize) -> String {
        let mut prefix = self.lines[..line].join("\n");
        if line > 0 {
            prefix.push('\n');
        }
        prefix
    }

    fn continues_lazily(&self, index: usize) -> bool {
        for previous in (self.block_start..index).rev() {
            let value = trimmed(self.lines[previous]);
            if is_heading(value) {
                return false;
            }
            if value.starts_with('>') {
                return true;
            }
            if starts_list_item(value) {
                return columns(self.lines[index]) < content_column(self.lines[previous]);
            }
        }
        false
    }

    /// The table at the end, following Apple's `Scan.table`: a matching delimiter/header pair, or an unfinished
    /// delimiter beneath a closed pipe header. A lazy continuation of a list item or quote is not a table header.
    fn table(&self) -> Option<Table> {
        let last = self.lines.len() - 1;
        if self.block_start > last {
            return None;
        }
        let mut index = last;
        while index > self.block_start && !starts_non_table_block(trimmed(self.lines[index])) {
            let value = trimmed(self.lines[index]);
            let head = trimmed(self.lines[index - 1]);
            if value.contains('|')
                && is_divider(value)
                && !head.is_empty()
                && !is_delimiterish(head)
                && !starts_non_table_block(head)
                && cells(head) == cells(value)
                && !self.continues_lazily(index - 1)
            {
                return Some(Table {
                    header: index - 1,
                    divided: true,
                });
            }
            index -= 1;
        }
        let header = if self.ends_with_newline {
            last
        } else {
            last.checked_sub(1)?
        };
        if header < self.block_start {
            return None;
        }
        let head = trimmed(self.lines[header]);
        if !head.starts_with('|')
            || is_delimiterish(head)
            || self.continues_lazily(header)
            || header != self.block_start && trimmed(self.lines[header - 1]).starts_with('|')
        {
            return None;
        }
        let valid = if header == last {
            is_closed_pipe_row(head)
        } else {
            is_delimiterish(trimmed(self.lines[last]))
        };
        valid.then_some(Table { header, divided: false })
    }

    /// Hold back a half-written last row, or add the header's delimiter with the header's indentation.
    fn repair_table(&self, text: &str) -> Option<String> {
        let table = self.table()?;
        let last = self.lines.len() - 1;
        if table.divided {
            if self.ends_with_newline || last <= table.header + 1 || !trimmed(self.lines[last]).starts_with('|') {
                return None;
            }
            return Some(self.prefix_before(last));
        }
        let head = self.lines[table.header];
        let row = format!("{}|{}", indent(head), " --- |".repeat(cells(trimmed(head))));
        if table.header == last {
            Some(format!("{text}{row}"))
        } else {
            Some(format!("{}{row}", self.prefix_before(last)))
        }
    }
}

/// Apple `Scan.markersOnly`: the unfinished line can still become a list item, heading, quote or rule. A marker
/// followed by real text is not held back, and a line that already ended never reaches this predicate.
fn markers_only(line: &str) -> bool {
    let scalars = line.chars().collect::<Vec<_>>();
    let Some(first) = scalars
        .iter()
        .position(|character| !matches!(character, ' ' | '\t' | '\r'))
    else {
        return false;
    };
    let mut saw = false;
    let mut index = 0;
    while index < scalars.len() {
        let character = scalars[index];
        if matches!(character, ' ' | '\t' | '\r') {
            index += 1;
        } else if "#-=*+_>|`~:[]".contains(character) {
            saw = true;
            index += 1;
        } else if matches!(character, 'x' | 'X') && index > 0 && scalars[index - 1] == '[' {
            index += 1;
        } else if character.is_ascii_digit() {
            let mut end = index;
            while end < scalars.len() && scalars[end].is_ascii_digit() {
                end += 1;
            }
            if end - index > 9 {
                return false;
            }
            if end == scalars.len() {
                if index != first {
                    return false;
                }
            } else if !matches!(scalars[end], '.' | ')') {
                return false;
            }
            saw = true;
            index = (end + 1).min(scalars.len());
        } else {
            return false;
        }
    }
    saw
}

fn hold_markers(text: &str) -> Option<String> {
    if text.ends_with('\n') {
        return None;
    }
    let Some((prefix, last)) = text.rsplit_once('\n') else {
        return markers_only(text).then(String::new);
    };
    markers_only(last).then(|| format!("{prefix}\n"))
}

fn project_repair(text: &str) -> String {
    if text.is_empty() {
        return String::new();
    }
    if let Some(shorter) = without_indent_so_far(text) {
        return project_repair(shorter);
    }
    let scan = Scan::new(text);
    if let Some(open) = &scan.fence {
        let marker = format!(
            "{}{}",
            indent(scan.lines[open.line]),
            open.character.to_string().repeat(open.count)
        );
        if !scan.ends_with_newline {
            let last = scan.lines.len() - 1;
            if open.line == last {
                return scan.prefix_before(last);
            }
            let last_value = trimmed(scan.lines[last]);
            if !last_value.is_empty() && last_value.chars().all(|character| character == open.character) {
                return format!("{}{marker}", scan.prefix_before(last));
            }
        }
        return format!("{text}{}{marker}", if scan.ends_with_newline { "" } else { "\n" });
    }
    if scan.closed_last {
        return text.to_owned();
    }
    if let Some(table) = scan.repair_table(text) {
        return table;
    }
    if scan.table().is_some() || scan.ends_with_newline {
        return text.to_owned();
    }
    hold_markers(text).unwrap_or_else(|| text.to_owned())
}

fn project_end(text: &str) -> &'static str {
    if text.is_empty() {
        return "text";
    }
    if let Some(shorter) = without_indent_so_far(text) {
        return project_end(shorter);
    }
    let scan = Scan::new(text);
    if scan.fence.is_some() || scan.closed_last && !scan.ends_with_newline {
        "code"
    } else if scan.table().is_some() {
        "table"
    } else {
        "text"
    }
}

/// Repairs only fences and tables, and reports this slice's prose/code/table end state.
pub fn project(input: &Input) -> Projection {
    Projection {
        repaired: project_repair(&input.text),
        end: project_end(&input.text),
    }
}
