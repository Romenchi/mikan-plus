//! The pieces every screen is made of: the card, text inputs, lists, check marks, the
//! QR code. Colors are from the 256-color palette, which terminals over SSH show right.

use std::sync::mpsc::{self, Receiver, TryRecvError};
use std::thread;

use ratatui::Frame;
use ratatui::crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use ratatui::layout::{Constraint, Layout, Position, Rect};
use ratatui::style::{Color, Modifier, Style, Stylize};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Clear, Padding, Paragraph};

use crate::system::Level;

pub const ACCENT: Color = Color::Indexed(208);
pub const DIM: Color = Color::Indexed(245);
pub const FAINT: Color = Color::Indexed(239);
pub const OK: Color = Color::Indexed(114);
pub const WARN: Color = Color::Indexed(221);
pub const ERR: Color = Color::Indexed(203);

pub fn dim(s: impl Into<String>) -> Span<'static> {
    Span::styled(s.into(), Style::new().fg(DIM))
}

pub fn level_span(l: Level) -> Span<'static> {
    match l {
        Level::Ok => Span::styled("✓", Style::new().fg(OK)),
        Level::Warn => Span::styled("!", Style::new().fg(WARN).bold()),
        Level::Error => Span::styled("✗", Style::new().fg(ERR).bold()),
    }
}

pub fn level_color(l: Level) -> Color {
    match l {
        Level::Ok => OK,
        Level::Warn => WARN,
        Level::Error => ERR,
    }
}

const SPIN: [&str; 10] = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];

pub fn spinner(tick: usize) -> Span<'static> {
    Span::styled(SPIN[tick % SPIN.len()], Style::new().fg(ACCENT))
}

/// Splits text into lines of at most width characters, at spaces; a word longer than a
/// line (a key, a link) is cut into pieces, so every character stays on screen.
pub fn wrap(text: &str, width: usize) -> Vec<String> {
    let width = width.max(8);
    let mut out = Vec::new();
    for para in text.split('\n') {
        let mut line = String::new();
        for word in para.split(' ') {
            let mut chars: Vec<char> = word.chars().collect();
            while chars.len() > width {
                if !line.is_empty() {
                    out.push(std::mem::take(&mut line));
                }
                out.push(chars.drain(..width).collect());
            }
            let word: String = chars.into_iter().collect();
            let len = line.chars().count();
            if len > 0 && len + 1 + word.chars().count() > width {
                out.push(std::mem::take(&mut line));
            }
            if !line.is_empty() {
                line.push(' ');
            }
            line.push_str(&word);
        }
        out.push(line);
    }
    out
}

/// The card every screen draws in: the brand and step on top, the title and its lead,
/// the keys at the bottom. Returns the body's area; None when the terminal is too small.
pub struct Card<'a> {
    pub title: &'a str,
    pub lead: &'a str,
    pub step: Option<(usize, usize)>,
    pub keys: &'a [(&'a str, &'a str)],
    body: Option<u16>,
}

/// A card's width at most; its body is 8 columns narrower (borders and padding).
const CARD_WIDTH: u16 = 84;

impl<'a> Card<'a> {
    pub fn new(title: &'a str, lead: &'a str, keys: &'a [(&'a str, &'a str)]) -> Self {
        Self { title, lead, step: None, keys, body: None }
    }

    pub fn step(mut self, i: usize, n: usize) -> Self {
        self.step = Some((i, n));
        self
    }

    /// Makes the card as tall as a body of lines lines, not the whole terminal.
    pub fn fit(mut self, lines: usize) -> Self {
        self.body = Some(lines.max(3) as u16);
        self
    }

    /// The width of a card's body on this terminal, to lay out lines before drawing.
    pub fn body_width(area: Rect) -> usize {
        CARD_WIDTH.min(area.width).saturating_sub(8) as usize
    }

    pub fn draw(&self, f: &mut Frame) -> Option<Rect> {
        let area = f.area();
        if area.width < 60 || area.height < 20 {
            let msg = Paragraph::new(vec![Line::from("mikan".fg(ACCENT).bold()), Line::from(dim("Make the terminal at least 60×20."))]);
            f.render_widget(msg, area);
            return None;
        }
        let width = CARD_WIDTH.min(area.width);
        let lead = if self.lead.is_empty() { Vec::new() } else { wrap(self.lead, width.saturating_sub(8) as usize) };
        let head_h = 1 + lead.len() as u16;
        let height = match self.body {
            Some(b) => (head_h + b + 5).max(12).min(area.height),
            None => area.height.min(32),
        };
        let card = center(area, width, height);
        f.render_widget(Clear, card);
        let mut block = Block::bordered()
            .border_type(BorderType::Rounded)
            .border_style(Style::new().fg(FAINT))
            .title(Line::from(vec![Span::raw(" "), "●".fg(ACCENT), Span::raw(" "), "mikan".bold(), Span::raw(" ")]))
            .padding(Padding::new(3, 3, 1, 0));
        match self.step {
            Some((i, n)) => block = block.title(Line::from(dim(format!(" {i} of {n} "))).right_aligned()),
            None => block = block.title(Line::from(dim(format!(" {} ", crate::version()))).right_aligned()),
        }
        block = block.title_bottom(keys(self.keys));
        let inner = block.inner(card);
        f.render_widget(block, card);
        let [head, _, body] = Layout::vertical([Constraint::Length(head_h), Constraint::Length(1), Constraint::Min(0)]).areas(inner);
        let mut lines = vec![Line::from(self.title.bold())];
        lines.extend(lead.into_iter().map(|l| Line::from(dim(l))));
        f.render_widget(Paragraph::new(lines), head);
        Some(body)
    }
}

/// The keys a screen takes, for the bottom border: the key bright, what it does dim.
pub fn keys(keys: &[(&str, &str)]) -> Line<'static> {
    let mut spans = vec![Span::raw(" ")];
    for (i, (key, what)) in keys.iter().enumerate() {
        if i > 0 {
            spans.push(Span::styled("  ", Style::new()));
        }
        spans.push(Span::styled(key.to_string(), Style::new().fg(Color::Indexed(252)).add_modifier(Modifier::BOLD)));
        spans.push(Span::raw(" "));
        spans.push(dim(what.to_string()));
    }
    spans.push(Span::raw(" "));
    Line::from(spans)
}

pub fn center(area: Rect, w: u16, h: u16) -> Rect {
    Rect { x: area.x + (area.width - w) / 2, y: area.y + (area.height - h) / 2, width: w, height: h }
}

/// A one-line text field.
#[derive(Default, Clone)]
pub struct Input {
    pub value: String,
    cursor: usize,
}

impl Input {
    pub fn new(v: &str) -> Self {
        Self { value: v.to_owned(), cursor: v.chars().count() }
    }

    pub fn set(&mut self, v: &str) {
        *self = Self::new(v);
    }

    pub fn text(&self) -> String {
        self.value.trim().to_owned()
    }

    /// Takes pasted text: its first line, without control characters. Pasted as keys, the
    /// line break would act as Enter and press Next.
    pub fn paste(&mut self, text: &str) {
        // A terminal sends a line break as CR as often as LF.
        for c in text.split(['\r', '\n']).next().unwrap_or_default().chars().filter(|c| !c.is_control()) {
            self.key(KeyEvent::new(KeyCode::Char(c), KeyModifiers::NONE));
        }
    }

    /// Takes an editing key; false when the key is not for the field.
    pub fn key(&mut self, k: KeyEvent) -> bool {
        let at = |s: &str, i: usize| s.char_indices().nth(i).map_or(s.len(), |(b, _)| b);
        match k.code {
            KeyCode::Char('u') if k.modifiers.contains(KeyModifiers::CONTROL) => self.set(""),
            KeyCode::Char(c) if !k.modifiers.contains(KeyModifiers::CONTROL) && !c.is_control() => {
                let b = at(&self.value, self.cursor);
                self.value.insert(b, c);
                self.cursor += 1;
            }
            KeyCode::Backspace if self.cursor > 0 => {
                self.cursor -= 1;
                let b = at(&self.value, self.cursor);
                self.value.remove(b);
            }
            KeyCode::Delete if self.cursor < self.value.chars().count() => {
                let b = at(&self.value, self.cursor);
                self.value.remove(b);
            }
            KeyCode::Left => self.cursor = self.cursor.saturating_sub(1),
            KeyCode::Right => self.cursor = (self.cursor + 1).min(self.value.chars().count()),
            KeyCode::Home => self.cursor = 0,
            KeyCode::End => self.cursor = self.value.chars().count(),
            KeyCode::Backspace | KeyCode::Delete => {}
            _ => return false,
        }
        true
    }

    /// Draws the field in a 3-line box; the terminal's cursor stands in it when focused.
    pub fn draw(&self, f: &mut Frame, area: Rect, placeholder: &str, focused: bool, color: Option<Color>) {
        let border = color.unwrap_or(if focused { ACCENT } else { FAINT });
        let block =
            Block::bordered().border_type(BorderType::Rounded).border_style(Style::new().fg(border)).padding(Padding::horizontal(1));
        let inner = block.inner(area);
        let width = inner.width.max(1) as usize;
        let skip = self.cursor.saturating_sub(width - 1);
        let shown: String = self.value.chars().skip(skip).take(width).collect();
        let text = if self.value.is_empty() { Line::from(dim(placeholder.to_owned())) } else { Line::from(shown) };
        f.render_widget(Paragraph::new(text).block(block), area);
        if focused {
            f.set_cursor_position(Position::new(inner.x + (self.cursor - skip) as u16, inner.y));
        }
    }
}

/// A line of a list: the marker, the label and a dim hint.
pub fn item(label: &str, hint: &str, selected: bool, width: usize) -> Line<'static> {
    let marker = if selected { Span::styled("› ", Style::new().fg(ACCENT).bold()) } else { Span::raw("  ") };
    let label_style = if selected { Style::new().bold() } else { Style::new() };
    let pad = 18usize.saturating_sub(label.chars().count()).max(2);
    let room = width.saturating_sub(2 + label.chars().count() + pad);
    let hint: String =
        if hint.chars().count() > room { hint.chars().take(room.saturating_sub(1)).chain(['…']).collect() } else { hint.to_owned() };
    Line::from(vec![marker, Span::styled(label.to_owned(), label_style), Span::raw(" ".repeat(pad)), dim(hint)])
}

/// A checkbox line.
pub fn checkbox(label: &str, on: bool, focused: bool) -> Line<'static> {
    let marker = if focused { Span::styled("› ", Style::new().fg(ACCENT).bold()) } else { Span::raw("  ") };
    let mark = if on { Span::styled("[✓] ", Style::new().fg(OK)) } else { dim("[ ] ") };
    let style = if focused { Style::new().bold() } else { Style::new() };
    Line::from(vec![marker, mark, Span::styled(label.to_owned(), style)])
}

/// Label and value in two columns.
pub fn field(label: &str, value: impl Into<String>) -> Line<'static> {
    Line::from(vec![dim(format!("{label:<14}")), Span::raw(value.into())])
}

/// The QR code of text in half blocks, two modules per character; black on white with a
/// quiet zone, so phones read it on any terminal theme.
pub fn qr(text: &str) -> Vec<Line<'static>> {
    let Ok(code) = qrcode::QrCode::new(text.as_bytes()) else { return Vec::new() };
    let w = code.width() as i32;
    let colors = code.to_colors();
    let dark = |x: i32, y: i32| x >= 0 && y >= 0 && x < w && y < w && colors[(y * w + x) as usize] == qrcode::Color::Dark;
    let q = 2;
    let paint = |d: bool| if d { Color::Black } else { Color::White };
    (-q..w + q)
        .step_by(2)
        .map(|y| {
            Line::from(
                (-q..w + q).map(|x| Span::styled("▀", Style::new().fg(paint(dark(x, y))).bg(paint(dark(x, y + 1))))).collect::<Vec<_>>(),
            )
        })
        .collect()
}

/// Work in a thread whose result the screen picks up on its next tick.
pub enum Task<T> {
    Idle,
    Running(Receiver<T>),
    Done(T),
}

impl<T: Send + 'static> Task<T> {
    pub fn start(f: impl FnOnce() -> T + Send + 'static) -> Self {
        let (tx, rx) = mpsc::channel();
        thread::spawn(move || {
            let _ = tx.send(f());
        });
        Task::Running(rx)
    }

    pub fn poll(&mut self) -> bool {
        let next = match self {
            Task::Running(rx) => match rx.try_recv() {
                Ok(v) => Some(Task::Done(v)),
                Err(TryRecvError::Empty) => None,
                Err(TryRecvError::Disconnected) => Some(Task::Idle),
            },
            _ => None,
        };
        let finished = next.is_some();
        if let Some(n) = next {
            *self = n;
        }
        finished
    }

    pub fn running(&self) -> bool {
        matches!(self, Task::Running(_))
    }

    pub fn idle(&self) -> bool {
        matches!(self, Task::Idle)
    }

    /// The result, leaving the task idle.
    pub fn take(&mut self) -> Option<T> {
        match std::mem::replace(self, Task::Idle) {
            Task::Done(v) => Some(v),
            other => {
                *self = other;
                None
            }
        }
    }

    pub fn done(&self) -> Option<&T> {
        match self {
            Task::Done(v) => Some(v),
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn wraps_at_spaces() {
        assert_eq!(wrap("one two three four", 9), ["one two", "three", "four"]);
        assert_eq!(wrap("a\nb", 20), ["a", "b"]);
        assert_eq!(wrap("key mikan1.abcdefghijkl end", 8), ["key", "mikan1.a", "bcdefghi", "jkl end"]);
    }

    #[test]
    fn edits() {
        let mut i = Input::new("vpn.exmple.com");
        for _ in 0.."mple.com".len() {
            i.key(KeyEvent::from(KeyCode::Left));
        }
        i.key(KeyEvent::from(KeyCode::Char('a')));
        assert_eq!(i.value, "vpn.example.com");
        i.key(KeyEvent::from(KeyCode::End));
        i.key(KeyEvent::from(KeyCode::Backspace));
        assert_eq!(i.value, "vpn.example.co");
        assert!(!i.key(KeyEvent::from(KeyCode::Enter)));
    }

    // A pasted line break would press Enter (Next) if it came as keys: only the first line
    // goes in, whichever way the terminal ends it, and no control character with it.
    #[test]
    fn a_paste_is_one_line() {
        for text in ["vpn.example.com\nsecond", "vpn.example.com\rsecond", "vpn.example.com\r\nsecond", "vpn.example.com"] {
            let mut i = Input::new("");
            i.paste(text);
            assert_eq!(i.value, "vpn.example.com", "{text:?}");
        }
        let mut i = Input::new("ab");
        i.key(KeyEvent::from(KeyCode::Left));
        i.paste("\x1b[31mX\ty");
        assert_eq!(i.value, "a[31mXyb", "an escape is not text");
    }

    #[test]
    fn qr_is_square_with_quiet_zone() {
        let lines = qr("https://vpn.example.com:21355/Kq7vN2xTg4mRz8pLw3YbC5dE/");
        assert!(!lines.is_empty());
        let width = lines[0].spans.len();
        assert!(lines.len() * 2 >= width && lines.len() * 2 <= width + 1);
    }
}
