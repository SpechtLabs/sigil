//! Durations as Sigil writes them: integer components with the units `d`,
//! `h`, `m`, `s` and `ms`, largest first, each at most once, like `1h30m` or
//! `2d`. That's the form inputs, payloads and host function arguments carry
//! across the boundary, and it isn't Go's `time.ParseDuration` syntax: there is
//! a `d`, and no fractions or `us`.

use std::time::Duration;

use crate::error::Error;

const UNITS: [(&str, u128); 5] = [("d", 86_400_000), ("h", 3_600_000), ("m", 60_000), ("s", 1_000), ("ms", 1)];

/// The largest duration Go's `time.Duration` holds, in whole milliseconds.
const MAX_MS: u128 = 9_223_372_036_854;

/// Parses a duration in Sigil's syntax: `parse_duration("1h30m")` is 90 minutes.
pub fn parse_duration(text: &str) -> Result<Duration, Error> {
    let ms = to_ms(text)?;
    Ok(Duration::from_millis(u64::try_from(ms).expect("checked against MAX_MS")))
}

/// Writes a duration in Sigil's canonical form, to whole milliseconds:
/// `format_duration(Duration::from_secs(5400))` is `1h30m`, zero is `0s`.
/// Fails for one Sigil can't hold (about 292 years).
pub fn format_duration(d: Duration) -> Result<String, Error> {
    let ms = d.as_millis();
    if ms > MAX_MS {
        return Err(Error::sigil(format!("{d:?} isn't a duration Sigil can write"), "a duration is at most about 292 years"));
    }
    Ok(render(ms))
}

/// The milliseconds of a duration in Sigil's syntax.
pub(crate) fn to_ms(text: &str) -> Result<u128, Error> {
    let invalid = |help: &str| Error::sigil(format!("invalid duration {text:?}"), help);
    let mut total: u128 = 0;
    let mut last: Option<usize> = None;
    let mut rest = text;
    while !rest.is_empty() {
        let digits = rest.bytes().take_while(u8::is_ascii_digit).count();
        let (number, after) = rest.split_at(digits);
        // `ms` before `m`, so "5ms" isn't "5m" and a stray "s".
        let Some((rank, (unit, size))) = UNITS.iter().enumerate().rev().find(|(_, (u, _))| after.starts_with(u)).filter(|_| digits > 0)
        else {
            return Err(invalid("units are d, h, m, s and ms, largest first, each at most once: \"1h30m\""));
        };
        if Some(rank) == last {
            return Err(invalid("each unit may appear once in a duration; add the components together"));
        }
        if last.is_some_and(|l| rank < l) {
            return Err(invalid("write the largest unit first, like \"1h30m\""));
        }
        last = Some(rank);
        let n: u128 = number.parse().map_err(|_| invalid("a duration is at most about 292 years"))?;
        total = n.checked_mul(*size).and_then(|v| total.checked_add(v)).ok_or_else(|| invalid("a duration is at most about 292 years"))?;
        rest = &after[unit.len()..];
    }
    if total > MAX_MS {
        return Err(invalid("a duration is at most about 292 years"));
    }
    Ok(total)
}

/// Renders milliseconds as Sigil's constant formatter does (the largest units
/// first, each at most once, `0s` for zero), so a payload default prints byte
/// for byte as Go's `Kind.Schema` writes it.
pub(crate) fn render(mut ms: u128) -> String {
    if ms == 0 {
        return "0s".into();
    }
    let mut out = String::new();
    for (unit, size) in UNITS {
        let q = ms / size;
        if q > 0 {
            out.push_str(&format!("{q}{unit}"));
            ms -= q * size;
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use rstest::rstest;

    #[rstest]
    #[case("", 0)]
    #[case("0s", 0)]
    #[case("1h30m", 5_400_000)]
    #[case("2d3h", 183_600_000)]
    #[case("5ms", 5)]
    #[case("1m30s250ms", 90_250)]
    #[case("90s", 90_000)]
    fn parses(#[case] text: &str, #[case] ms: u128) {
        assert_eq!(to_ms(text).unwrap(), ms);
    }

    #[rstest]
    #[case("90 minutes", "units are d, h, m, s and ms")]
    #[case("1h1h", "each unit may appear once")]
    #[case("30m1h", "write the largest unit first")]
    #[case("h", "units are d, h, m, s and ms")]
    #[case("5", "units are d, h, m, s and ms")]
    #[case("1.5h", "units are d, h, m, s and ms")]
    #[case("-1h", "units are d, h, m, s and ms")]
    #[case("9999999999999d", "at most about 292 years")]
    #[case("99999999999999999999999999999999999999999d", "at most about 292 years")]
    fn rejects_with_help(#[case] text: &str, #[case] help: &str) {
        let err = to_ms(text).unwrap_err().to_string();
        assert!(err.contains(&format!("invalid duration {text:?}")), "{err}");
        assert!(err.contains(help), "{err}");
    }

    #[rstest]
    #[case(0, "0s")]
    #[case(1, "1ms")]
    #[case(720_000, "12m")]
    #[case(5_400_000, "1h30m")]
    #[case(90 * 60_000, "1h30m")]
    #[case(183_600_000, "2d3h")]
    #[case(86_400_000 + 1, "1d1ms")]
    fn renders_canonically(#[case] ms: u64, #[case] text: &str) {
        assert_eq!(format_duration(Duration::from_millis(ms)).unwrap(), text);
    }

    #[test]
    fn round_trips_and_rejects_what_sigil_cant_hold() {
        assert_eq!(parse_duration("1h30m").unwrap(), Duration::from_secs(5400));
        assert_eq!(format_duration(parse_duration("90m").unwrap()).unwrap(), "1h30m");
        assert!(format_duration(Duration::from_secs(u64::MAX / 2)).is_err());
    }

    #[test]
    fn sub_millisecond_parts_are_dropped() {
        assert_eq!(format_duration(Duration::from_micros(1500)).unwrap(), "1ms");
    }
}
