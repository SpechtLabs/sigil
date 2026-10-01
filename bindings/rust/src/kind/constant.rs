//! Constants of a kind file: payload defaults, written as Go's
//! `constant.Format` writes them so the kind file is byte for byte Go's.

use serde_json::Value;
use unicode_general_category::{GeneralCategory, get_general_category};

use super::ty::Type;
use crate::duration;

/// Writes `value` as a constant of type `ty`, or says why it isn't one.
pub(crate) fn format(ty: &Type, value: &Value) -> Result<String, String> {
    let bad = || Err(format!("{value} isn't a constant of type {}", ty.sigil()));
    match ty {
        Type::String => match value {
            Value::String(s) => Ok(go_quote(s)),
            _ => bad(),
        },
        Type::Bool => match value {
            Value::Bool(b) => Ok(b.to_string()),
            _ => bad(),
        },
        Type::Int => match value.as_i64() {
            Some(n) => Ok(n.to_string()),
            None => bad(),
        },
        Type::Float => match value.as_f64() {
            Some(f) if f.is_finite() => Ok(format_float(f)),
            _ => bad(),
        },
        Type::Duration => match value {
            Value::String(s) => duration::to_ms(s).map(duration::render).map_err(|e| e.to_string()),
            _ => bad(),
        },
        Type::Timestamp => Err("a timestamp field can't have a default: timestamps come from input; there's no literal for one".into()),
        Type::List(elem) => match value {
            Value::Array(items) => Ok(format!("[{}]", items.iter().map(|v| format(elem, v)).collect::<Result<Vec<_>, _>>()?.join(", "))),
            _ => bad(),
        },
        Type::Map(key, val) => match value {
            Value::Object(entries) => {
                let numeric = matches!(**key, Type::Int | Type::Float);
                let mut parts = Vec::with_capacity(entries.len());
                for (k, v) in entries {
                    let key_value = match (numeric, k.parse::<f64>()) {
                        (true, Ok(_)) => serde_json::from_str::<Value>(k).unwrap_or_else(|_| Value::String(k.clone())),
                        _ => Value::String(k.clone()),
                    };
                    parts.push(format!("{}: {}", format(key, &key_value)?, format(val, v)?));
                }
                // Go sorts the formatted entries by their bytes.
                parts.sort();
                Ok(format!("{{{}}}", parts.join(", ")))
            }
            _ => bad(),
        },
        Type::Optional(elem) => match value {
            Value::Null => Ok("none".into()),
            v => format(elem, v),
        },
        Type::Enum(e) => match value {
            Value::String(s) if e.values.contains(s) => Ok(s.clone()),
            _ => bad(),
        },
        Type::Struct(s) => {
            Err(format!("a default of struct type {} can't be written in a kind file: give struct-typed payload fields no default", s.name))
        }
    }
}

/// Quotes a string as Go's `%q` verb does, which is how a kind file writes a
/// string constant: printable characters as they are, the rest escaped.
pub(crate) fn go_quote(s: &str) -> String {
    let mut out = String::from('"');
    for ch in s.chars() {
        match ch {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\x07' => out.push_str("\\a"),
            '\x08' => out.push_str("\\b"),
            '\x0c' => out.push_str("\\f"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\x0b' => out.push_str("\\v"),
            c if is_print(c) => out.push(c),
            c if (c as u32) < 0x80 => out.push_str(&format!("\\x{:02x}", c as u32)),
            c if (c as u32) <= 0xffff => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push_str(&format!("\\U{:08x}", c as u32)),
        }
    }
    out.push('"');
    out
}

/// Go's `unicode.IsPrint`: letters, marks, numbers, punctuation, symbols and
/// the ASCII space. The Unicode tables are this crate's dependency's, which may
/// be a version away from Go's for characters assigned in between.
fn is_print(c: char) -> bool {
    use GeneralCategory::*;
    c == ' '
        || matches!(
            get_general_category(c),
            UppercaseLetter
                | LowercaseLetter
                | TitlecaseLetter
                | ModifierLetter
                | OtherLetter
                | NonspacingMark
                | SpacingMark
                | EnclosingMark
                | DecimalNumber
                | LetterNumber
                | OtherNumber
                | ConnectorPunctuation
                | DashPunctuation
                | OpenPunctuation
                | ClosePunctuation
                | InitialPunctuation
                | FinalPunctuation
                | OtherPunctuation
                | MathSymbol
                | CurrencySymbol
                | ModifierSymbol
                | OtherSymbol
        )
}

/// Formats a float as Go's `strconv.FormatFloat(v, 'f', -1, 64)` does, plus a
/// `.0` for a whole number: the shortest digits that round-trip, never in
/// exponent notation. Rust's `Display` for `f64` is exactly that.
fn format_float(v: f64) -> String {
    let s = v.to_string();
    if s.contains('.') { s } else { format!("{s}.0") }
}

#[cfg(test)]
mod tests {
    use super::*;
    use rstest::rstest;
    use serde_json::json;

    #[rstest]
    #[case("plain", "\"plain\"")]
    #[case("a\"b\\c", "\"a\\\"b\\\\c\"")]
    #[case("tab\tnl\ncr\r", "\"tab\\tnl\\ncr\\r\"")]
    #[case("\u{7}\u{8}\u{c}\u{b}", "\"\\a\\b\\f\\v\"")]
    #[case("\u{1}\u{7f}", "\"\\x01\\x7f\"")]
    #[case("\u{80}\u{2028}\u{feff}", "\"\\u0080\\u2028\\ufeff\"")]
    #[case("✓ é 😀 ä", "\"✓ é 😀 ä\"")]
    #[case("\u{e0001}", "\"\\U000e0001\"")]
    #[case("", "\"\"")]
    fn quotes_as_gos_q_verb_does(#[case] input: &str, #[case] quoted: &str) {
        assert_eq!(go_quote(input), quoted);
    }

    #[rstest]
    #[case(Type::string(), json!("x"), "\"x\"")]
    #[case(Type::bool(), json!(false), "false")]
    #[case(Type::int(), json!(-3), "-3")]
    #[case(Type::int(), json!(i64::MIN), "-9223372036854775808")]
    #[case(Type::float(), json!(0.5), "0.5")]
    #[case(Type::float(), json!(2), "2.0")]
    #[case(Type::float(), json!(1e21), "1000000000000000000000.0")]
    #[case(Type::float(), json!(1e-7), "0.0000001")]
    #[case(Type::float(), json!(-0.0), "-0.0")]
    #[case(Type::duration(), json!("90m"), "1h30m")]
    #[case(Type::duration(), json!(""), "0s")]
    #[case(Type::list(Type::int()), json!([3, 1]), "[3, 1]")]
    #[case(Type::list(Type::string()), json!([]), "[]")]
    #[case(Type::map(Type::string(), Type::int()), json!({"b": 2, "a": 1, "ä": 3, "Z": 4}), "{\"Z\": 4, \"a\": 1, \"b\": 2, \"ä\": 3}")]
    #[case(Type::map(Type::int(), Type::bool()), json!({"10": true, "2": false}), "{10: true, 2: false}")]
    #[case(Type::optional(Type::int()), json!(4), "4")]
    #[case(Type::enumeration("L", ["low", "high"]), json!("high"), "high")]
    fn formats_constants_as_go_does(#[case] ty: Type, #[case] value: Value, #[case] want: &str) {
        assert_eq!(format(&ty, &value).unwrap(), want);
    }

    #[rstest]
    #[case(Type::string(), json!(1), "isn't a constant of type string")]
    #[case(Type::int(), json!(1.5), "isn't a constant of type int")]
    #[case(Type::int(), json!("1"), "isn't a constant of type int")]
    #[case(Type::bool(), json!("true"), "isn't a constant of type bool")]
    #[case(Type::float(), json!("x"), "isn't a constant of type float")]
    #[case(Type::duration(), json!(5), "isn't a constant of type duration")]
    #[case(Type::duration(), json!("soon"), "invalid duration")]
    #[case(Type::list(Type::int()), json!({}), "isn't a constant of type list<int>")]
    #[case(Type::list(Type::int()), json!(["a"]), "isn't a constant of type int")]
    #[case(Type::map(Type::string(), Type::int()), json!([]), "isn't a constant of type map<string, int>")]
    #[case(Type::enumeration("L", ["low"]), json!("high"), "isn't a constant of type L")]
    #[case(Type::timestamp(), json!("2026-01-01T00:00:00Z"), "timestamp field can't have a default")]
    #[case(Type::structure("S", [("a", Type::int())]), json!({"a": 1}), "can't be written in a kind file")]
    fn refuses_what_isnt_a_constant_of_the_type(#[case] ty: Type, #[case] value: Value, #[case] why: &str) {
        let err = format(&ty, &value).unwrap_err();
        assert!(err.contains(why), "{err}");
    }
}
