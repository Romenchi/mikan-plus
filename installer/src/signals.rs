//! Sections that must not be cut short: from the moment an update changes .env until the
//! new version is up or the old one is back, and a restore from `compose down` to the
//! start. Ctrl+C, a dropped ssh session (SIGHUP) and `kill` do nothing in them; the
//! operation ends first and the signal's work waits for the next one.

use std::sync::{Mutex, PoisonError};

const SIGNALS: [libc::c_int; 3] = [libc::SIGINT, libc::SIGHUP, libc::SIGTERM];

struct State {
    depth: usize,
    before: [libc::sighandler_t; 3],
}

static STATE: Mutex<State> = Mutex::new(State { depth: 0, before: [0; 3] });

/// Signals are ignored while it lives; they come back when the last one is dropped.
pub struct Critical(());

pub fn critical() -> Critical {
    let mut s = STATE.lock().unwrap_or_else(PoisonError::into_inner);
    if s.depth == 0 {
        for (i, sig) in SIGNALS.iter().enumerate() {
            // SAFETY: SIG_IGN installs no handler code, so nothing runs in signal context.
            s.before[i] = unsafe { libc::signal(*sig, libc::SIG_IGN) };
        }
    }
    s.depth += 1;
    Critical(())
}

impl Drop for Critical {
    fn drop(&mut self) {
        let mut s = STATE.lock().unwrap_or_else(PoisonError::into_inner);
        s.depth = s.depth.saturating_sub(1);
        if s.depth == 0 {
            for (i, sig) in SIGNALS.iter().enumerate() {
                if s.before[i] != libc::SIG_ERR {
                    // SAFETY: puts back the disposition signal() returned for this signal.
                    unsafe { libc::signal(*sig, s.before[i]) };
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // Without the guard a SIGHUP ends the test process, so passing is the proof.
    #[test]
    fn signals_do_nothing_inside() {
        // SAFETY: reads SIGHUP's disposition by setting it to ignore, then sets it back.
        let orig = unsafe {
            let o = libc::signal(libc::SIGHUP, libc::SIG_IGN);
            libc::signal(libc::SIGHUP, o);
            o
        };
        let outer = critical();
        let inner = critical();
        for sig in SIGNALS {
            // SAFETY: raises a signal at this process, which ignores it.
            assert_eq!(unsafe { libc::raise(sig) }, 0);
        }
        drop(inner);
        // still ignored while the outer one lives
        // SAFETY: as above.
        assert_eq!(unsafe { libc::raise(libc::SIGHUP) }, 0);
        drop(outer);
        // The default is back: asking for it again returns what is set now.
        // SAFETY: signal() with SIG_IGN reads the previous disposition and sets the same one.
        let now = unsafe { libc::signal(libc::SIGHUP, libc::SIG_IGN) };
        // SAFETY: puts back what it returned.
        unsafe { libc::signal(libc::SIGHUP, now) };
        assert_eq!(now, orig, "the disposition before the guard is back");
    }
}
