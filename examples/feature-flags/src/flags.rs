//! Flag keys and their policies. A flag is one policy, `flags.<name>`, where
//! the flag key is the name with `_` written as `-`: `flags.new_checkout`
//! serves `new-checkout`. Keys hold lowercase letters, digits and hyphens,
//! so the mapping is one to one and every key names a valid policy.

use sha2::{Digest, Sha256};

/// The namespace every flag policy lives in.
pub const POLICY_PREFIX: &str = "flags.";

/// The longest flag key accepted.
pub const MAX_KEY_LEN: usize = 64;

/// Checks a key's shape, with advice on what to write instead.
pub fn validate_key(key: &str) -> Result<(), String> {
    let valid = !key.is_empty()
        && key.len() <= MAX_KEY_LEN
        && key.starts_with(|c: char| c.is_ascii_lowercase())
        && !key.ends_with('-')
        && !key.contains("--")
        && key.chars().all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-');
    if valid {
        Ok(())
    } else {
        Err(format!(
            "{key:?} is not a flag key; use lowercase letters, digits and single hyphens, starting with a letter, up to {MAX_KEY_LEN} characters, like new-checkout"
        ))
    }
}

/// The policy that serves `key`.
pub fn policy_name(key: &str) -> String {
    format!("{POLICY_PREFIX}{}", key.replace('-', "_"))
}

/// The flag key a policy name serves; `None` for a policy outside `flags.`
/// or one whose name no key maps to.
pub fn key_of_policy(policy: &str) -> Option<String> {
    let key = policy.strip_prefix(POLICY_PREFIX)?.replace('_', "-");
    validate_key(&key).ok().map(|()| key)
}

/// A user's place in a flag's rollout, 0 to 99: a stable hash of the flag and
/// the targeting key. The flag is part of the hash so that one user isn't in
/// the first 10% of every rollout at once. SHA-256 keeps the spread even
/// without a tuned function; the first eight bytes are plenty for a modulus.
pub fn bucket(flag: &str, targeting_key: &str) -> i64 {
    let mut h = Sha256::new();
    h.update(flag.as_bytes());
    // A NUL can't occur in either part of a JSON-decoded flag key, so
    // ("ab","c") and ("a","bc") hash differently.
    h.update([0u8]);
    h.update(targeting_key.as_bytes());
    let digest = h.finalize();
    let n = u64::from_be_bytes(digest[..8].try_into().expect("a SHA-256 digest has 32 bytes"));
    (n % 100) as i64
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case("new-checkout", true)]
    #[case("a", true)]
    #[case("search-v2", true)]
    #[case("dark-mode-2", true)]
    #[case("", false)]
    #[case("New-Checkout", false)]
    #[case("new_checkout", false)]
    #[case("2fa", false)]
    #[case("-x", false)]
    #[case("x-", false)]
    #[case("x--y", false)]
    #[case("x y", false)]
    #[case("a.b", false)]
    fn key_shapes(#[case] key: &str, #[case] ok: bool) {
        assert_eq!(validate_key(key).is_ok(), ok, "{key}");
    }

    #[test]
    fn overlong_keys_are_rejected() {
        assert!(validate_key(&"a".repeat(MAX_KEY_LEN)).is_ok());
        assert!(validate_key(&"a".repeat(MAX_KEY_LEN + 1)).is_err());
    }

    #[rstest]
    #[case("new-checkout", "flags.new_checkout")]
    #[case("dark-mode", "flags.dark_mode")]
    #[case("beta", "flags.beta")]
    fn policy_names_round_trip(#[case] key: &str, #[case] policy: &str) {
        assert_eq!(policy_name(key), policy);
        assert_eq!(key_of_policy(policy).as_deref(), Some(key));
    }

    #[rstest]
    #[case("platform.guardrails")]
    #[case("flags.")]
    #[case("flags.Bad")]
    #[case("flags.trailing_")]
    #[case("new_checkout")]
    fn policies_outside_the_namespace_serve_no_flag(#[case] policy: &str) {
        assert_eq!(key_of_policy(policy), None);
    }

    #[test]
    fn buckets_are_stable() {
        // Golden values: a change here moves every user in every rollout, and
        // requests/cases.json depends on these.
        assert_eq!(bucket("new-checkout", "user-5"), 25);
        assert_eq!(bucket("new-checkout", "user-10"), 24);
        assert_eq!(bucket("new-checkout", "user-6"), 19);
        assert_eq!(bucket("search-v2", "user-1"), 3);
        assert_eq!(bucket("search-v2", "user-3"), 27);
    }

    #[test]
    fn buckets_depend_on_the_flag() {
        let moved = (0..200).filter(|i| bucket("a", &format!("u{i}")) != bucket("b", &format!("u{i}"))).count();
        assert!(moved > 150, "only {moved} of 200 users moved between two flags");
    }

    #[test]
    fn the_separator_keeps_parts_apart() {
        assert_ne!(bucket("ab", "c"), bucket("a", "bc"), "unlikely collision, fix the separator if this ever fires");
    }

    #[test]
    fn buckets_spread_evenly() {
        let mut counts = [0u32; 100];
        for i in 0..100_000 {
            counts[bucket("spread", &format!("user-{i}")) as usize] += 1;
        }
        // 1000 expected per bucket; a fair hash stays well within 15%.
        assert!(counts.iter().all(|&c| (850..=1150).contains(&c)), "{counts:?}");
    }
}
