//! The installer and the menu in the terminal (ratatui).

mod menu;
mod widgets;
mod wizard;

use std::io::IsTerminal;
use std::path::Path;
use std::time::Duration;

use anyhow::{Result, bail};
use ratatui::crossterm::event::{self, DisableBracketedPaste, EnableBracketedPaste, Event, KeyEvent, KeyEventKind};
use ratatui::{DefaultTerminal, Frame};

use crate::setup::Options;

pub fn interactive() -> bool {
    std::io::stdin().is_terminal() && std::io::stdout().is_terminal()
}

/// `mikan` without a command: the installer on a fresh server, the menu on an installed one.
pub fn start() -> Result<()> {
    let installed = Path::new(crate::DIR).join(".env").exists();
    if !interactive() {
        if installed {
            bail!("the menu needs a terminal; the commands are in mikan --help");
        }
        bail!("the installer needs a terminal; for scripts: mikan install --yes (see mikan install --help)");
    }
    if crate::setup::unfinished_install() {
        // The menu of a half-installed server has nothing to show: the install goes on.
        return crate::setup::install(Options::default());
    }
    if installed { menu() } else { wizard(Options::default()) }
}

pub fn wizard(opts: Options) -> Result<()> {
    let mut w = wizard::Wizard::new(opts);
    run(&mut w)?;
    if let Some(text) = w.farewell() {
        println!("{text}");
    }
    w.result()
}

pub fn menu() -> Result<()> {
    let mut m = menu::Menu::new()?;
    run(&mut m)?;
    if let Some(text) = m.farewell() {
        println!("{text}");
    }
    Ok(())
}

/// A screen the loop drives.
pub trait Screen {
    fn draw(&mut self, f: &mut Frame);
    /// Takes a key; true ends the screen.
    fn key(&mut self, k: KeyEvent) -> bool;
    /// Picks up background work; runs before every frame.
    fn tick(&mut self);
    /// Takes pasted text (a terminal in bracketed paste mode sends it whole, not as keys).
    fn paste(&mut self, _text: &str) {}
}

fn run(s: &mut dyn Screen) -> Result<()> {
    let mut t = ratatui::init();
    // Without it a pasted line break reads as Enter.
    let _ = ratatui::crossterm::execute!(std::io::stdout(), EnableBracketedPaste);
    let r = drive(&mut t, s);
    let _ = ratatui::crossterm::execute!(std::io::stdout(), DisableBracketedPaste);
    ratatui::restore();
    r
}

fn drive(t: &mut DefaultTerminal, s: &mut dyn Screen) -> Result<()> {
    loop {
        s.tick();
        t.draw(|f| s.draw(f))?;
        if event::poll(Duration::from_millis(80))? {
            match event::read()? {
                Event::Key(k) if k.kind == KeyEventKind::Press && s.key(k) => return Ok(()),
                Event::Paste(text) => s.paste(&text),
                _ => {}
            }
        }
    }
}
