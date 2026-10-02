//! Backups and restore. A backup is a tar.gz of what mikan keeps in /opt/mikan: .env,
//! compose.yaml, a consistent copy of the panel's database, its certificates and the node's
//! data. It holds every secret of the server, so it is born private: the directory is 0700
//! and the archive is created 0600 before tar writes a byte into it.
//!
//! A restore does not trust the archive (it may be somebody else's file): the names and
//! types are checked, it is unpacked into a directory of its own, and only then, with the
//! containers stopped and the current data in a snapshot, does it replace anything.

use std::fs::{self, File, OpenOptions};
use std::io::ErrorKind;
use std::os::unix::fs::{DirBuilderExt, OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::Duration;

use anyhow::{Context, Result, bail};

use crate::clock::Utc;
use crate::envfile::EnvFile;
use crate::lock::{self, Wait};
use crate::ops::Install;
use crate::panelfs::{self, Dir};
use crate::{DIR, docker, setup, signals};

/// Backups an update makes on its own are kept this many; the admin's own are never removed.
const KEEP_UPDATE: usize = 5;
const KEEP_RESTORE: usize = 3;

/// What a restore may put back, below /opt/mikan: the settings, and the data of the panel
/// and the node. Anything else in an archive is not a mikan backup.
const TOP: [&str; 2] = [".env", "compose.yaml"];

/// The backups' directory, private.
fn backups_dir(root: &Path) -> Result<PathBuf> {
    let dir = root.join("backups");
    match fs::DirBuilder::new().mode(0o700).create(&dir) {
        Ok(()) => {}
        Err(e) if e.kind() == ErrorKind::AlreadyExists => fs::set_permissions(&dir, fs::Permissions::from_mode(0o700))?,
        Err(e) => return Err(e).with_context(|| format!("create {}", dir.display())),
    }
    Ok(dir)
}

/// A new file kind-<time>.tar.gz in dir, for its owner only; never one that is there.
fn create(dir: &Path, kind: &str) -> Result<(File, PathBuf)> {
    let stamp = Utc::now().stamp();
    for n in 0..100 {
        let name = if n == 0 { format!("{kind}-{stamp}.tar.gz") } else { format!("{kind}-{stamp}-{n}.tar.gz") };
        let path = dir.join(name);
        match OpenOptions::new().write(true).create_new(true).mode(0o600).custom_flags(libc::O_NOFOLLOW).open(&path) {
            Ok(f) => return Ok((f, path)),
            Err(e) if e.kind() == ErrorKind::AlreadyExists => {}
            Err(e) => return Err(e).with_context(|| format!("create {}", path.display())),
        }
    }
    bail!("no free name for a backup in {}", dir.display())
}

/// tar of members (paths below root) into a new private archive in dir.
fn pack(root: &Path, dir: &Path, kind: &str, members: &[String]) -> Result<PathBuf> {
    let (file, path) = create(dir, kind)?;
    let out = Command::new("tar")
        .args(["-czf", "-", "-C"])
        .arg(root)
        .args(members)
        .stdin(Stdio::null())
        .stdout(Stdio::from(file))
        .stderr(Stdio::piped())
        .output();
    match out {
        // GNU tar exits 1 when a file changed while it was read (a node's counters): the
        // archive is whole, that one file is as of a moment ago.
        Ok(o)
            if o.status.success()
                || (o.status.code() == Some(1) && String::from_utf8_lossy(&o.stderr).contains("changed as we read it")) =>
        {
            Ok(path)
        }
        Ok(o) => {
            let _ = fs::remove_file(&path);
            bail!("tar failed ({}): {}", o.status, String::from_utf8_lossy(&o.stderr).trim())
        }
        Err(e) => {
            let _ = fs::remove_file(&path);
            Err(e).context("run tar")
        }
    }
}

/// Removes the oldest archives of a kind beyond keep.
fn prune(dir: &Path, kind: &str, keep: usize) {
    let prefix = format!("{kind}-");
    let mut names: Vec<String> = fs::read_dir(dir)
        .map(|d| {
            d.flatten()
                .filter_map(|e| e.file_name().into_string().ok())
                .filter(|n| n.starts_with(&prefix) && n.ends_with(".tar.gz"))
                .collect()
        })
        .unwrap_or_default();
    names.sort();
    let excess = names.len().saturating_sub(keep);
    for name in names.into_iter().take(excess) {
        let _ = fs::remove_file(dir.join(name));
    }
}

fn exists(root: &Path, rel: &str) -> bool {
    fs::symlink_metadata(root.join(rel)).is_ok()
}

/// The backup the admin asks for.
pub fn backup(say: &mut dyn FnMut(&str)) -> Result<PathBuf> {
    backup_as("mikan", say)
}

/// A backup of the database (a consistent copy while the panel runs), certificates and
/// settings, in /opt/mikan/backups. kind names the file: an update's own are rotated.
pub fn backup_as(kind: &str, say: &mut dyn FnMut(&str)) -> Result<PathBuf> {
    let _lock = lock::acquire(Wait::Block, say)?;
    let install = Install::load()?;
    let root = Path::new(DIR);
    let dir = backups_dir(root)?;
    let mut members: Vec<String> = TOP.iter().map(|s| (*s).to_owned()).collect();
    let panel = if install.node {
        None
    } else {
        let panel = Dir::open(root, "data/panel", false)?.context("data/panel is not there")?;
        // What an interrupted backup left would make the database copy fail.
        panel.discard("backup.db")?;
        docker::check(docker::admin(&["backup", "/data/panel/backup.db"], None)?)?;
        members.push("data/panel/backup.db".into());
        if exists(root, "data/panel/tls") {
            members.push("data/panel/tls".into());
        }
        Some(panel)
    };
    if exists(root, "data/node") {
        members.push("data/node".into());
    }
    let packed = pack(root, &dir, kind, &members);
    if let Some(panel) = &panel {
        let _ = panel.discard("backup.db");
    }
    let file = packed?;
    match kind {
        "pre-update" => prune(&dir, kind, KEEP_UPDATE),
        "pre-restore" => prune(&dir, kind, KEEP_RESTORE),
        _ => {}
    }
    Ok(file)
}

/// The archives in /opt/mikan/backups, the newest first.
pub fn backups() -> Vec<PathBuf> {
    let mut list: Vec<(std::time::SystemTime, PathBuf)> = fs::read_dir(Path::new(DIR).join("backups"))
        .map(|d| {
            d.flatten()
                .filter(|e| e.path().extension().is_some_and(|x| x == "gz"))
                .filter_map(|e| Some((e.metadata().ok()?.modified().ok()?, e.path())))
                .collect()
        })
        .unwrap_or_default();
    list.sort_by(|a, b| b.cmp(a));
    list.into_iter().map(|(_, p)| p).collect()
}

/// Whether an archive path is one a restore takes: relative, without "..", and one of the
/// settings files or inside the panel's or the node's data.
fn allowed_name(name: &str) -> bool {
    let n = name.strip_prefix("./").unwrap_or(name).trim_end_matches('/');
    if n.is_empty() || n.split('/').any(|c| c.is_empty() || c == ".." || c == ".") {
        return false;
    }
    TOP.contains(&n) || ["data", "data/panel", "data/node"].contains(&n) || n.starts_with("data/panel/") || n.starts_with("data/node/")
}

/// Judges the two listings of an archive (`tar -tzf`, names; `tar -tvzf`, with types).
/// Only plain files and directories pass: a link could lead the unpacking out of the
/// directory it is meant for, a device or a FIFO has no place in a backup.
fn check_listing(names: &str, verbose: &str) -> Result<()> {
    let names: Vec<&str> = names.lines().collect();
    if names.is_empty() {
        bail!("the archive is empty");
    }
    // A name with a line break would make the two listings disagree.
    if names.len() != verbose.lines().count() {
        bail!("the archive has file names with line breaks");
    }
    if let Some(l) = verbose.lines().find(|l| !matches!(l.chars().next(), Some('-' | 'd'))) {
        bail!("the archive has an entry that is not a plain file or directory: {}", l.trim());
    }
    if let Some(n) = names.iter().find(|n| !allowed_name(n)) {
        bail!("the archive has a path a mikan backup does not: {n}");
    }
    Ok(())
}

fn check_archive(file: &Path) -> Result<()> {
    let list = |flags: &str| -> Result<String> {
        let out = Command::new("tar").arg(flags).arg(file).stdin(Stdio::null()).output().context("run tar")?;
        if !out.status.success() {
            bail!("{} is not a readable tar.gz archive: {}", file.display(), String::from_utf8_lossy(&out.stderr).trim());
        }
        Ok(String::from_utf8_lossy(&out.stdout).into_owned())
    };
    check_listing(&list("-tzf")?, &list("-tvzf")?)
}

/// A directory of its own for unpacking, removed when done.
struct Stage(PathBuf);

impl Stage {
    fn new(root: &Path) -> Result<Self> {
        let nanos = std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_nanos()).unwrap_or(0);
        let path = root.join(format!(".restore-{}-{nanos}", std::process::id()));
        fs::DirBuilder::new().mode(0o700).create(&path).with_context(|| format!("create {}", path.display()))?;
        Ok(Self(path))
    }
}

impl Drop for Stage {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn extract(file: &Path, into: &Path) -> Result<()> {
    let out = Command::new("tar")
        .args(["-xzf"])
        .arg(file)
        .arg("-C")
        .arg(into)
        .args(["--no-same-owner", "--no-same-permissions"])
        .stdin(Stdio::null())
        .output()
        .context("run tar")?;
    if !out.status.success() {
        bail!("tar could not unpack {}: {}", file.display(), String::from_utf8_lossy(&out.stderr).trim());
    }
    Ok(())
}

/// What was unpacked is plain files and directories, none with a setuid bit: checked
/// again on the disk, whatever the listing said.
fn verify_tree(dir: &Path, depth: usize) -> Result<()> {
    if depth > 32 {
        bail!("the archive is nested too deep");
    }
    for entry in fs::read_dir(dir)? {
        let entry = entry?;
        let meta = fs::symlink_metadata(entry.path())?;
        if meta.permissions().mode() & 0o7000 != 0 {
            bail!("{} has a setuid, setgid or sticky bit", entry.path().display());
        }
        if meta.is_dir() {
            verify_tree(&entry.path(), depth + 1)?;
        } else if !meta.is_file() {
            bail!("{} is not a plain file", entry.path().display());
        }
    }
    Ok(())
}

/// What an unpacked archive puts back: (where it is, where it goes below /opt/mikan).
fn plan(stage: &Path) -> Result<Vec<(PathBuf, &'static str)>> {
    let mut items = Vec::new();
    for rel in [".env", "compose.yaml", "data/panel/tls", "data/node"] {
        if fs::symlink_metadata(stage.join(rel)).is_ok() {
            items.push((stage.join(rel), rel));
        }
    }
    if !items.iter().any(|(_, r)| *r == ".env") {
        bail!("the archive has no .env: it is not a mikan backup");
    }
    // The panel's database comes as the consistent copy a backup makes, or as the file a
    // snapshot took with the panel stopped.
    for db in ["data/panel/backup.db", "data/panel/mikan.db"] {
        if fs::symlink_metadata(stage.join(db)).is_ok() {
            items.push((stage.join(db), "data/panel/mikan.db"));
            break;
        }
    }
    Ok(items)
}

/// Moves the items in, each current one aside first; when one step fails, everything that
/// moved goes back.
fn swap(root: &Path, items: &[(PathBuf, &str)]) -> Result<()> {
    let aside = root.join(format!(".restore-old-{}", std::process::id()));
    let _ = fs::remove_dir_all(&aside);
    fs::DirBuilder::new().mode(0o700).create(&aside)?;
    let mut parked: Vec<(PathBuf, PathBuf)> = Vec::new();
    let mut placed: Vec<PathBuf> = Vec::new();
    let result = (|| -> Result<()> {
        let mut away = |rel: &str| -> Result<()> {
            let to = root.join(rel);
            if fs::symlink_metadata(&to).is_ok() {
                let spot = aside.join(rel.replace('/', "__"));
                fs::rename(&to, &spot).with_context(|| format!("move {} aside", to.display()))?;
                parked.push((to, spot));
            }
            Ok(())
        };
        for (from, rel) in items {
            away(rel)?;
            if *rel == "data/panel/mikan.db" {
                // The journal of the old database does not belong to the new one.
                away("data/panel/mikan.db-wal")?;
                away("data/panel/mikan.db-shm")?;
            }
            let to = root.join(rel);
            if let Some(parent) = to.parent() {
                fs::create_dir_all(parent)?;
            }
            fs::rename(from, &to).with_context(|| format!("put {} in place", to.display()))?;
            placed.push(to);
        }
        Ok(())
    })();
    if result.is_err() {
        for p in placed.iter().rev() {
            let _ = if fs::symlink_metadata(p).is_ok_and(|m| m.is_dir()) { fs::remove_dir_all(p) } else { fs::remove_file(p) };
        }
        for (orig, spot) in parked.iter().rev() {
            let _ = fs::rename(spot, orig);
        }
    }
    let _ = fs::remove_dir_all(&aside);
    result
}

/// The current data as it is, with the containers stopped, in a pre-restore archive.
fn snapshot(root: &Path) -> Result<PathBuf> {
    let dir = backups_dir(root)?;
    let members: Vec<String> = [
        ".env",
        "compose.yaml",
        "data/panel/mikan.db",
        "data/panel/mikan.db-wal",
        "data/panel/mikan.db-shm",
        "data/panel/tls",
        "data/node",
    ]
    .iter()
    .filter(|m| exists(root, m))
    .map(|m| (*m).to_owned())
    .collect();
    let file = pack(root, &dir, "pre-restore", &members)?;
    prune(&dir, "pre-restore", KEEP_RESTORE);
    Ok(file)
}

/// Puts the panel's database back from an archive of this installer's own making, with the
/// containers stopped and nothing else touched. The update uses it when the new version did
/// not start and the old one does not run on what the new one did to the database.
pub fn restore_database(file: &Path) -> Result<()> {
    let root = Path::new(DIR);
    check_archive(file)?;
    let stage = Stage::new(root)?;
    extract(file, &stage.0)?;
    verify_tree(&stage.0, 0)?;
    let db = plan(&stage.0)?.into_iter().find(|(_, to)| *to == "data/panel/mikan.db").context("the archive has no database")?;
    docker::compose_run(&["down"])?;
    real_data_dirs(root)?;
    swap(root, &[db])?;
    panelfs::own_dirs(root, true)
}

/// data/panel and data/node are directories, not links the panel put there: the swap
/// below renames paths inside them, and with the containers stopped this cannot change
/// between this look and the swap.
fn real_data_dirs(root: &Path) -> Result<()> {
    for part in ["data/panel", "data/node"] {
        Dir::open(root, part, false)?;
    }
    Ok(())
}

/// Replaces the data with a backup's. The archive is checked first and unpacked aside; the
/// current data go to a pre-restore archive before anything changes, and a restore that
/// fails half way puts them back.
pub fn restore(file: &Path, say: &mut dyn FnMut(&str)) -> Result<()> {
    let _lock = lock::acquire(Wait::Block, say)?;
    let install = Install::load()?;
    let root = Path::new(DIR);
    if !file.is_file() {
        bail!("no file {}", file.display());
    }
    if !install.node && Dir::open(root, "data/panel", false)?.is_none() {
        bail!("{} is missing", root.join("data/panel").display());
    }
    say("Checking the archive");
    check_archive(file)?;
    let stage = Stage::new(root)?;
    extract(file, &stage.0)?;
    verify_tree(&stage.0, 0)?;
    let items = plan(&stage.0)?;
    let archived = EnvFile::load(stage.0.join(".env"))?;
    if (archived.get("MIKAN_MODE") == Some("node")) != install.node {
        bail!(
            "the backup is a {}'s, this server is a {}",
            if install.node { "panel" } else { "node" },
            if install.node { "node" } else { "panel" }
        );
    }

    let _critical = signals::critical();
    say("Stopping mikan");
    docker::compose_run(&["down"])?;
    let again = |say: &mut dyn FnMut(&str)| {
        if let Err(e) = docker::compose_run(&["up", "-d"]) {
            say(&format!("mikan did not start again: {e:#}"));
        }
    };
    // With the containers down nothing can swap a directory for a link any more: this is
    // the look that counts (the one before it only fails early).
    if let Err(e) = real_data_dirs(root) {
        again(say);
        return Err(e);
    }
    let snap = match snapshot(root) {
        Ok(s) => s,
        Err(e) => {
            again(say);
            return Err(e.context("the current data could not be saved first, so nothing was replaced"));
        }
    };
    say(&format!("The current data are saved in {}", snap.display()));
    if let Err(e) = swap(root, &items) {
        again(say);
        return Err(e.context("the data were not replaced"));
    }
    let started = (|| -> Result<()> {
        // The backup may carry the compose file of an older mikan: it gets the current one.
        docker::ensure_compose(root, install.node)?;
        panelfs::layout(root, !install.node)?;
        docker::compose_run(&["up", "-d"])?;
        setup::wait_ready(Install::load()?.node_port(), Duration::from_secs(90))
    })();
    if let Err(e) = started {
        return Err(e.context(format!(
            "the backup is in place but mikan does not start; the data before it are in {}: mikan restore {}",
            snap.display(),
            snap.display()
        )));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::symlink;

    fn tmpdir(name: &str) -> PathBuf {
        let d = std::env::temp_dir().join(format!("backup-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&d);
        fs::create_dir_all(&d).unwrap();
        d
    }

    fn file(root: &Path, rel: &str, text: &str) {
        let p = root.join(rel);
        fs::create_dir_all(p.parent().unwrap()).unwrap();
        fs::write(p, text).unwrap();
    }

    // The secrets of the server are in the archive: nobody but root may see the file
    // while tar writes it, and the directory keeps the names private too.
    #[test]
    fn a_backup_is_private_from_its_first_byte() {
        let root = tmpdir("private");
        file(&root, ".env", "MIKAN_IMAGE=x\n");
        file(&root, "compose.yaml", "name: mikan\n");
        file(&root, "data/panel/backup.db", "db");
        file(&root, "data/node/state.json", "{}");
        let dir = backups_dir(&root).unwrap();
        assert_eq!(fs::metadata(&dir).unwrap().permissions().mode() & 0o777, 0o700);
        // an old 0755 directory is made private too
        fs::set_permissions(&dir, fs::Permissions::from_mode(0o755)).unwrap();
        backups_dir(&root).unwrap();
        assert_eq!(fs::metadata(&dir).unwrap().permissions().mode() & 0o777, 0o700);
        let a =
            pack(&root, &dir, "mikan", &[".env".into(), "compose.yaml".into(), "data/panel/backup.db".into(), "data/node".into()]).unwrap();
        let b = pack(&root, &dir, "mikan", &[".env".into()]).unwrap();
        assert_ne!(a, b, "two backups of one second must not share a name");
        assert_eq!(fs::metadata(&a).unwrap().permissions().mode() & 0o777, 0o600);
        // the archive is what restore accepts
        check_archive(&a).unwrap();
        // a failed tar leaves no file
        let before = fs::read_dir(&dir).unwrap().count();
        assert!(pack(&root, &dir, "mikan", &["no-such-file".into()]).is_err());
        assert_eq!(fs::read_dir(&dir).unwrap().count(), before, "a broken archive stayed");
        fs::remove_dir_all(&root).unwrap();
    }

    #[test]
    fn automatic_backups_are_rotated_and_the_admins_stay() {
        let dir = tmpdir("prune");
        for n in 1..=8 {
            fs::write(dir.join(format!("pre-update-2026100{n}-000000.tar.gz")), "x").unwrap();
        }
        fs::write(dir.join("mikan-20260101-000000.tar.gz"), "mine").unwrap();
        fs::write(dir.join("pre-restore-20260101-000000.tar.gz"), "snap").unwrap();
        prune(&dir, "pre-update", KEEP_UPDATE);
        let mut left: Vec<String> = fs::read_dir(&dir).unwrap().map(|e| e.unwrap().file_name().into_string().unwrap()).collect();
        left.sort();
        assert_eq!(left.iter().filter(|n| n.starts_with("pre-update-")).count(), 5);
        assert!(left.contains(&"pre-update-20261008-000000.tar.gz".to_string()), "the newest stays");
        assert!(!left.contains(&"pre-update-20261001-000000.tar.gz".to_string()), "the oldest goes");
        assert!(
            left.contains(&"mikan-20260101-000000.tar.gz".to_string()) && left.contains(&"pre-restore-20260101-000000.tar.gz".to_string())
        );
        fs::remove_dir_all(&dir).unwrap();
    }

    #[test]
    fn only_mikan_backups_pass_the_listing() {
        let names = ".env\ncompose.yaml\ndata/\ndata/panel/\ndata/panel/backup.db\ndata/panel/tls/\ndata/panel/tls/panel.key\ndata/node/\ndata/node/state.json\n";
        let verbose: String = names
            .lines()
            .map(|n| format!("{} root/root 1 2026-10-01 00:00 {n}\n", if n.ends_with('/') { "drwx------" } else { "-rw-------" }))
            .collect();
        check_listing(names, &verbose).unwrap();
        let with = |n: &str, kind: &str| {
            let names = format!("{names}{n}\n");
            let verbose = format!("{verbose}{kind}rwxrwxrwx root/root 0 2026-10-01 00:00 {n}\n");
            check_listing(&names, &verbose)
        };
        assert!(with("data/panel/ok", "-").is_ok());
        for (n, why) in [
            ("/etc/passwd", "absolute"),
            ("../etc/passwd", "outside"),
            ("data/../../etc/cron.d/x", "a way out inside"),
            ("etc/passwd", "somewhere else"),
            ("data/addons/state.json", "the root's own"),
            ("data/panel/../../x", "nested dots"),
            ("evil", "a stranger"),
        ] {
            assert!(with(n, "-").is_err(), "{why}: {n}");
        }
        for kind in ["l", "h", "c", "b", "p"] {
            assert!(with("data/panel/link", kind).is_err(), "type {kind} passed");
        }
        assert!(check_listing("", "").is_err(), "an empty archive");
        // a name with a line break makes the listings disagree: refused
        assert!(check_listing(".env\ndata/panel/a\n../../etc/x\n", "-rw 1 .env\n-rw 1 data/panel/a\n../../etc/x\n").is_err());
    }

    #[test]
    fn a_hostile_archive_is_refused_before_it_is_unpacked() {
        let d = tmpdir("hostile");
        let src = d.join("src");
        file(&src, ".env", "MIKAN_MODE=panel\n");
        fs::create_dir_all(src.join("data/panel")).unwrap();
        symlink("/etc", src.join("data/panel/tls")).unwrap();
        let tgz = d.join("evil.tar.gz");
        let ok = Command::new("tar").args(["-czf"]).arg(&tgz).arg("-C").arg(&src).args([".env", "data"]).status().unwrap().success();
        assert!(ok);
        let err = check_archive(&tgz).unwrap_err();
        assert!(format!("{err:#}").contains("not a plain file"), "{err:#}");
        // not an archive at all
        file(&d, "plain.tar.gz", "this is not gzip");
        assert!(check_archive(&d.join("plain.tar.gz")).is_err());
        fs::remove_dir_all(&d).unwrap();
    }

    #[test]
    fn an_unpacked_tree_is_plain_files_only() {
        let d = tmpdir("tree");
        file(&d, "a/b/c", "x");
        verify_tree(&d, 0).unwrap();
        symlink("/etc/passwd", d.join("a/link")).unwrap();
        assert!(verify_tree(&d, 0).is_err());
        fs::remove_file(d.join("a/link")).unwrap();
        fs::set_permissions(d.join("a/b/c"), fs::Permissions::from_mode(0o4755)).unwrap();
        assert!(verify_tree(&d, 0).is_err(), "setuid");
        fs::remove_dir_all(&d).unwrap();
    }

    #[test]
    fn what_a_backup_puts_back() {
        let d = tmpdir("plan");
        assert!(plan(&d).is_err(), "no .env, not a backup");
        file(&d, ".env", "MIKAN_MODE=node\n");
        file(&d, "data/node/state.json", "{}");
        let items = plan(&d).unwrap();
        assert_eq!(items.iter().map(|(_, r)| *r).collect::<Vec<_>>(), [".env", "data/node"]);
        file(&d, "data/panel/backup.db", "db");
        file(&d, "data/panel/tls/panel.key", "k");
        let items = plan(&d).unwrap();
        let to: Vec<&str> = items.iter().map(|(_, r)| *r).collect();
        assert!(to.contains(&"data/panel/mikan.db") && to.contains(&"data/panel/tls"), "{to:?}");
        fs::remove_dir_all(&d).unwrap();
    }

    fn server(root: &Path) {
        file(root, ".env", "OLD=1\n");
        file(root, "compose.yaml", "old compose");
        file(root, "data/panel/mikan.db", "old db");
        file(root, "data/panel/mikan.db-wal", "old wal");
        file(root, "data/panel/tls/stale.key", "stale");
        file(root, "data/panel/update/status.json", "{}");
        file(root, "data/node/old", "old node");
    }

    #[test]
    fn the_swap_replaces_what_the_backup_has_and_keeps_the_rest() {
        let root = tmpdir("swap");
        server(&root);
        let stage = root.join("stage");
        file(&stage, ".env", "NEW=1\n");
        file(&stage, "data/panel/backup.db", "new db");
        file(&stage, "data/panel/tls/fresh.key", "fresh");
        file(&stage, "data/node/new", "new node");
        let items = plan(&stage).unwrap();
        swap(&root, &items).unwrap();
        assert_eq!(fs::read_to_string(root.join(".env")).unwrap(), "NEW=1\n");
        assert_eq!(fs::read_to_string(root.join("compose.yaml")).unwrap(), "old compose", "not in the backup: stays");
        assert_eq!(fs::read_to_string(root.join("data/panel/mikan.db")).unwrap(), "new db");
        assert!(!root.join("data/panel/mikan.db-wal").exists(), "the old database's journal is gone");
        assert!(root.join("data/panel/tls/fresh.key").exists() && !root.join("data/panel/tls/stale.key").exists(), "no stale certificate");
        assert!(root.join("data/node/new").exists() && !root.join("data/node/old").exists());
        assert!(root.join("data/panel/update/status.json").exists(), "what the backup does not hold stays");
        assert!(
            fs::read_dir(&root).unwrap().flatten().all(|e| !e.file_name().to_string_lossy().starts_with(".restore-old")),
            "the parked files are cleaned"
        );
        fs::remove_dir_all(&root).unwrap();
    }

    // The step that fails puts everything back: the server is what it was.
    #[test]
    fn a_swap_that_fails_half_way_puts_the_data_back() {
        let root = tmpdir("rollback");
        server(&root);
        let stage = root.join("stage");
        file(&stage, ".env", "NEW=1\n");
        file(&stage, "data/node/new", "new node");
        let mut items = plan(&stage).unwrap();
        items.push((stage.join("gone"), "data/panel/tls"));
        assert!(swap(&root, &items).is_err());
        assert_eq!(fs::read_to_string(root.join(".env")).unwrap(), "OLD=1\n");
        assert_eq!(fs::read_to_string(root.join("data/node/old")).unwrap(), "old node");
        assert!(!root.join("data/node/new").exists());
        assert_eq!(fs::read_to_string(root.join("data/panel/tls/stale.key")).unwrap(), "stale");
        assert_eq!(fs::read_to_string(root.join("data/panel/mikan.db")).unwrap(), "old db");
        fs::remove_dir_all(&root).unwrap();
    }

    #[test]
    fn a_snapshot_holds_the_current_data() {
        let root = tmpdir("snapshot");
        server(&root);
        let snap = snapshot(&root).unwrap();
        assert!(snap.starts_with(root.join("backups")));
        let out = Command::new("tar").arg("-tzf").arg(&snap).output().unwrap();
        let names = String::from_utf8_lossy(&out.stdout);
        for want in [".env", "compose.yaml", "data/panel/mikan.db", "data/panel/mikan.db-wal", "data/panel/tls/stale.key", "data/node/old"]
        {
            assert!(names.lines().any(|n| n == want), "{want} is not in {names}");
        }
        // a snapshot is a backup restore takes
        check_archive(&snap).unwrap();
        let stage = tmpdir("snapshot-stage");
        extract(&snap, &stage).unwrap();
        assert!(plan(&stage).unwrap().iter().any(|(_, r)| *r == "data/panel/mikan.db"));
        fs::remove_dir_all(&stage).unwrap();
        fs::remove_dir_all(&root).unwrap();
    }
}
