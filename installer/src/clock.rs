//! The time as text, from the system clock: no `date` to run, no "now" to fall back on
//! that two backups of one minute would share.

use std::time::{SystemTime, UNIX_EPOCH};

/// A moment in UTC.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Utc {
    pub year: i64,
    pub month: u32,
    pub day: u32,
    pub hour: u32,
    pub minute: u32,
    pub second: u32,
}

impl Utc {
    pub fn now() -> Self {
        let secs = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs()).unwrap_or(0);
        Self::from_unix(secs as i64)
    }

    pub fn from_unix(secs: i64) -> Self {
        let days = secs.div_euclid(86_400);
        let rest = secs.rem_euclid(86_400) as u32;
        // Days since 1970-01-01 to a civil date (Howard Hinnant's algorithm).
        let z = days + 719_468;
        let era = z.div_euclid(146_097);
        let doe = z.rem_euclid(146_097);
        let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
        let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
        let mp = (5 * doy + 2) / 153;
        let day = (doy - (153 * mp + 2) / 5 + 1) as u32;
        let month = if mp < 10 { mp + 3 } else { mp - 9 } as u32;
        let year = yoe + era * 400 + i64::from(month <= 2);
        Self { year, month, day, hour: rest / 3600, minute: rest % 3600 / 60, second: rest % 60 }
    }

    /// 20261001-181929, for file names.
    pub fn stamp(&self) -> String {
        format!("{:04}{:02}{:02}-{:02}{:02}{:02}", self.year, self.month, self.day, self.hour, self.minute, self.second)
    }

    /// 2026-10-01T18:19:29Z, as the panel reads it.
    pub fn rfc3339(&self) -> String {
        format!("{:04}-{:02}-{:02}T{:02}:{:02}:{:02}Z", self.year, self.month, self.day, self.hour, self.minute, self.second)
    }
}

pub fn rfc3339() -> String {
    Utc::now().rfc3339()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn civil_dates() {
        assert_eq!(Utc::from_unix(0).rfc3339(), "1970-01-01T00:00:00Z");
        assert_eq!(Utc::from_unix(951_782_400).rfc3339(), "2000-02-29T00:00:00Z");
        assert_eq!(Utc::from_unix(1_790_876_369).rfc3339(), "2026-10-01T17:39:29Z");
        assert_eq!(Utc::from_unix(1_790_876_369).stamp(), "20261001-173929");
        assert_eq!(Utc::from_unix(4_107_542_399).rfc3339(), "2100-02-28T23:59:59Z");
        assert_eq!(Utc::from_unix(-1).rfc3339(), "1969-12-31T23:59:59Z");
        assert!(Utc::now().year >= 2026);
    }
}
