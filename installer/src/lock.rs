//! One lock for everything that changes the server: an update (the daily timer's, the
//! panel's button, the admin's), the payment adapters, a backup, a restore, an install.
//! Two of them at once would rewrite .env and the containers under each other.
//!
//! The lock is an flock on a file in /run, so a crash or a kill frees it; a command that
//! needs the lock inside another that holds it (an update that backs up first) takes it
//! again for free.

use std::cell::Cell;
use std::fs::{OpenOptions, TryLockError};
use std::marker::PhantomData;
use std::os::unix::fs::OpenOptionsExt;
use std::path::Path;
use std::thread;
use std::time::Duration;

use anyhow::{Context, Result};

pub const FILE: &str = "/run/mikan.lock";

/// Set for the child of a process that holds the lock, which is only ever mikan itself
/// (the new installer after an update): it works under its parent's lock.
pub const HELD_ENV: &str = "MIKAN_LOCK_HELD";

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Wait {
    /// Give up at once: the daily check, which comes again tomorrow.
    Skip,
    /// Wait for the other operation to finish.
    Block,
}

thread_local! {
    static DEPTH: Cell<usize> = const { Cell::new(0) };
}

/// Holds the lock until it is dropped. It belongs to the thread that took it.
pub struct Guard {
    _file: Option<std::fs::File>,
    _thread: PhantomData<*const ()>,
}

impl Drop for Guard {
    fn drop(&mut self) {
        DEPTH.with(|d| d.set(d.get().saturating_sub(1)));
    }
}

/// Takes the lock. None only with Wait::Skip when another operation holds it.
pub fn acquire(wait: Wait, say: &mut dyn FnMut(&str)) -> Result<Option<Guard>> {
    acquire_at(Path::new(FILE), wait, say)
}

pub fn acquire_at(path: &Path, wait: Wait, say: &mut dyn FnMut(&str)) -> Result<Option<Guard>> {
    let nested = DEPTH.with(|d| d.get()) > 0 || std::env::var_os(HELD_ENV).is_some();
    let file = if nested {
        None
    } else {
        let file = OpenOptions::new()
            .create(true)
            .write(true)
            .truncate(false)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW)
            .open(path)
            .with_context(|| format!("open {}", path.display()))?;
        let mut told = false;
        loop {
            match file.try_lock() {
                Ok(()) => break,
                Err(TryLockError::WouldBlock) if wait == Wait::Skip => return Ok(None),
                Err(TryLockError::WouldBlock) => {
                    if !told {
                        say("Another mikan operation is running: waiting for it to finish.");
                        told = true;
                    }
                    thread::sleep(Duration::from_secs(1));
                }
                Err(TryLockError::Error(e)) => return Err(e).with_context(|| format!("lock {}", path.display())),
            }
        }
        Some(file)
    };
    DEPTH.with(|d| d.set(d.get() + 1));
    Ok(Some(Guard { _file: file, _thread: PhantomData }))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn path(name: &str) -> std::path::PathBuf {
        std::env::temp_dir().join(format!("lock-{name}-{}", std::process::id()))
    }

    #[test]
    fn one_at_a_time_and_nested_for_free() {
        let p = path("one");
        let quiet = &mut |_: &str| {};
        let first = acquire_at(&p, Wait::Skip, quiet).unwrap().expect("free");
        // the same thread again (an update that backs up first) passes
        let inner = acquire_at(&p, Wait::Skip, quiet).unwrap();
        assert!(inner.is_some());
        drop(inner);
        // another thread is another operation: it must wait, or skip
        let other = p.clone();
        let skipped = thread::spawn(move || acquire_at(&other, Wait::Skip, &mut |_| {}).unwrap().is_none()).join().unwrap();
        assert!(skipped, "a second operation took the lock");
        drop(first);
        // A process forked by a test running at the same moment holds a copy of the file
        // for the instant before its exec: freed means freed within moments.
        let other = p.clone();
        let taken = thread::spawn(move || {
            (0..30).any(|_| {
                let got = acquire_at(&other, Wait::Skip, &mut |_| {}).unwrap().is_some();
                if !got {
                    thread::sleep(Duration::from_millis(100));
                }
                got
            })
        })
        .join()
        .unwrap();
        assert!(taken, "the lock was not freed");
        let _ = std::fs::remove_file(&p);
    }

    #[test]
    fn a_waiting_operation_gets_it_when_the_holder_is_done() {
        let p = path("wait");
        let first = acquire_at(&p, Wait::Skip, &mut |_| {}).unwrap().unwrap();
        let other = p.clone();
        let waiter = thread::spawn(move || {
            let mut said = String::new();
            let g = acquire_at(&other, Wait::Block, &mut |l| said = l.to_owned()).unwrap();
            (g.is_some(), said)
        });
        thread::sleep(Duration::from_millis(1500));
        drop(first);
        let (got, said) = waiter.join().unwrap();
        assert!(got);
        assert!(said.contains("waiting"), "the waiter says why it waits: {said:?}");
        let _ = std::fs::remove_file(&p);
    }
}
