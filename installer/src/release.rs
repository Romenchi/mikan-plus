//! Releases: the signed manifest every release publishes (internal/release in the panel's
//! code). The installer trusts an image and an installer binary only through a manifest
//! with a valid signature of the release key.

use std::collections::BTreeMap;

use anyhow::{Context, Result, bail};
use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use ed25519_dalek::{Signature, VerifyingKey};
use serde::Deserialize;

/// The public half of the release signing key, the same as internal/release.PublicKey.
pub const PUBLIC_KEY: &str = "JCEgib4sIDFPGePPBk4B+zmKwlnpLNoZ8i7vmrJipPo=";

pub const REPO: &str = "Romenchi/mikan-plus";

/// Installs a node of an existing panel on a fresh server (internal/release.JoinCommand).
pub fn join_command(key: &str) -> String {
    format!("curl -fsSL https://github.com/{REPO}/releases/latest/download/install.sh | sudo bash -s -- --join {key}")
}

pub fn latest_url() -> String {
    format!("https://github.com/{REPO}/releases/latest/download/manifest.json")
}

#[derive(Deserialize, Debug, Clone)]
#[serde(deny_unknown_fields)]
pub struct Manifest {
    pub version: String,
    pub published: String,
    pub image: String,
    pub digest: String,
    pub installer: BTreeMap<String, Asset>,
    #[serde(default)]
    pub notes: BTreeMap<String, String>,
}

#[derive(Deserialize, Debug, Clone)]
#[serde(deny_unknown_fields)]
pub struct Asset {
    pub url: String,
    pub sha256: String,
}

impl Manifest {
    /// The image pinned to the released digest.
    pub fn reference(&self) -> String {
        format!("{}@{}", self.image, self.digest)
    }

    /// This server's installer binary.
    pub fn installer(&self) -> Option<&Asset> {
        self.installer.get(std::env::consts::ARCH)
    }
}

/// The newest release, verified.
pub fn latest() -> Result<Manifest> {
    let url = latest_url();
    let data = crate::net::get_opt(&url, 1 << 20).context("download the release manifest")?.context("no release is published yet")?;
    let sig = crate::net::get(&format!("{url}.sig"), 4096).context("download the manifest's signature")?;
    parse(&data, &String::from_utf8_lossy(&sig), &key()?)
}

fn key() -> Result<VerifyingKey> {
    let raw: [u8; 32] = STANDARD.decode(PUBLIC_KEY)?.try_into().map_err(|_| anyhow::anyhow!("bad release key"))?;
    Ok(VerifyingKey::from_bytes(&raw)?)
}

/// Checks the signature over the manifest's exact bytes, then what it says.
pub fn parse(data: &[u8], sig: &str, key: &VerifyingKey) -> Result<Manifest> {
    let raw = STANDARD.decode(sig.trim()).context("the manifest's signature is not base64")?;
    let sig = Signature::from_slice(&raw).context("the manifest's signature is malformed")?;
    key.verify_strict(data, &sig).context("the manifest's signature does not match the release key")?;
    let m: Manifest = serde_json::from_slice(data).context("the manifest is malformed")?;
    if semver(&m.version).is_none() {
        bail!("the manifest has a bad version {:?}", m.version);
    }
    let hex = m.digest.strip_prefix("sha256:").unwrap_or("");
    if hex.len() != 64 || !hex.bytes().all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b)) || !m.image.starts_with("ghcr.io/") {
        bail!("the manifest names a bad image {}@{}", m.image, m.digest);
    }
    Ok(m)
}

type Version<'a> = ([u64; 3], Option<&'a str>);

fn semver(v: &str) -> Option<Version<'_>> {
    let (core, pre) = match v.split_once('-') {
        Some((c, p)) if !p.is_empty() => (c, Some(p)),
        Some(_) => return None,
        None => (v, None),
    };
    let mut n = [0u64; 3];
    let mut parts = core.split('.');
    for x in &mut n {
        let p = parts.next()?;
        if p.is_empty() || !p.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        *x = p.parse().ok()?;
    }
    parts.next().is_none().then_some((n, pre))
}

/// Whether version a is later than b, as internal/release.Newer: a pre-release comes
/// before its release, and "dev" or anything unparsable is older than every release.
pub fn newer(a: &str, b: &str) -> bool {
    match (semver(a), semver(b)) {
        (None, _) => false,
        (Some(_), None) => true,
        (Some((x, xp)), Some((y, yp))) => match x.cmp(&y) {
            std::cmp::Ordering::Equal => match (xp, yp) {
                (None, Some(_)) => true,
                (Some(p), Some(q)) => p > q,
                _ => false,
            },
            o => o.is_gt(),
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use ed25519_dalek::{Signer, SigningKey};

    fn manifest(version: &str, image: &str) -> Vec<u8> {
        format!(
            r#"{{"version":"{version}","published":"2026-09-29T10:00:00Z","image":"{image}","digest":"sha256:{}","installer":{{"x86_64":{{"url":"https://github.com/Miroshka000/mikan/releases/download/v{version}/mikan-x86_64","sha256":"{}"}}}},"notes":{{"en":"- x"}}}}"#,
            "a".repeat(64),
            "b".repeat(64)
        )
        .into_bytes()
    }

    #[test]
    fn signed_manifests_only() {
        let signer = SigningKey::from_bytes(&[7; 32]);
        let key = signer.verifying_key();
        let data = manifest("0.3.9", "ghcr.io/miroshka000/mikan");
        let sig = STANDARD.encode(signer.sign(&data).to_bytes());
        let m = parse(&data, &format!("{sig}\n"), &key).unwrap();
        assert_eq!(m.reference(), format!("ghcr.io/miroshka000/mikan@sha256:{}", "a".repeat(64)));
        assert_eq!(m.installer["x86_64"].sha256, "b".repeat(64));

        let mut tampered = data.clone();
        tampered[14] = b'8';
        assert!(parse(&tampered, &sig, &key).is_err());
        let other = SigningKey::from_bytes(&[8; 32]).verifying_key();
        assert!(parse(&data, &sig, &other).is_err());
        assert!(parse(&data, "not base64!", &key).is_err());

        let docker_hub = manifest("0.3.9", "docker.io/someone/mikan");
        let sig = STANDARD.encode(signer.sign(&docker_hub).to_bytes());
        assert!(parse(&docker_hub, &sig, &key).is_err());
    }

    // The installer and the panel trust the same key.
    #[test]
    fn key_matches_the_panel() {
        let go = std::fs::read_to_string("../internal/release/release.go").unwrap();
        assert!(go.contains(&format!("const PublicKey = \"{PUBLIC_KEY}\"")), "internal/release.PublicKey differs");
        assert!(go.contains(&format!("const Repo = \"{REPO}\"")), "internal/release.Repo differs");
        assert!(
            go.contains("/releases/latest/download/install.sh | sudo bash\"") && go.contains("\" -s -- --join \""),
            "internal/release.JoinCommand differs"
        );
        key().unwrap();
    }

    #[test]
    fn versions() {
        assert!(newer("0.3.10", "0.3.9"));
        assert!(newer("0.4.0", "0.3.99"));
        assert!(newer("1.0.0", "0.9.9"));
        assert!(!newer("0.3.9", "0.3.9"));
        assert!(newer("0.3.9", "0.3.9-rc.1"));
        assert!(!newer("0.3.9-rc.1", "0.3.9"));
        assert!(newer("0.3.9-rc.2", "0.3.9-rc.1"));
        assert!(newer("0.3.9", "dev"));
        assert!(!newer("dev", "0.3.9"));
        assert!(!newer("0.3", "0.2.9"));
    }
}
