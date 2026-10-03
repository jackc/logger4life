//! Validation shared by the catalog's HTTP and application operations.
use crate::{AppError, Result};
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use std::collections::HashSet;

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct Field {
    #[serde(default, deserialize_with = "null_default")]
    pub name: String,
    #[serde(default, rename = "type", deserialize_with = "null_default")]
    pub kind: String,
    #[serde(default, deserialize_with = "null_default")]
    pub required: bool,
}

fn null_default<'de, D, T>(deserializer: D) -> std::result::Result<T, D::Error>
where
    D: serde::Deserializer<'de>,
    T: Deserialize<'de> + Default,
{
    Ok(Option::<T>::deserialize(deserializer)?.unwrap_or_default())
}

pub fn id(field: &str, value: &str) -> Result<()> {
    uuid::Uuid::parse_str(value)
        .map(|_| ())
        .map_err(|_| AppError::bad_request(format!("{field} is invalid")))
}

pub fn optional_id(field: &str, value: Option<&str>) -> Result<()> {
    value.map(|value| id(field, value)).transpose().map(|_| ())
}

pub fn name(value: &str) -> Result<String> {
    let value = value.trim();
    if value.is_empty() || value.len() > 100 {
        return Err(AppError::bad_request("name must be 1-100 characters"));
    }
    Ok(value.to_owned())
}

pub fn definitions(fields: &mut [Field]) -> Result<()> {
    if fields.len() > 20 {
        return Err(AppError::bad_request("too many fields (max 20)"));
    }
    let mut seen = HashSet::new();
    for field in fields {
        field.name = field.name.trim().to_owned();
        if field.name.is_empty() || field.name.len() > 100 {
            return Err(AppError::bad_request("field name must be 1-100 characters"));
        }
        if !seen.insert(
            field
                .name
                .chars()
                .flat_map(|c| c.to_lowercase().next())
                .collect::<String>(),
        ) {
            return Err(AppError::bad_request(format!(
                "duplicate field name: {}",
                field.name
            )));
        }
        if !matches!(field.kind.as_str(), "text" | "number" | "boolean") {
            return Err(AppError::bad_request(
                "field type must be 'text', 'number', or 'boolean'",
            ));
        }
    }
    Ok(())
}

pub fn values(fields: &[Field], values: &Map<String, Value>) -> Result<()> {
    for key in values.keys() {
        if !fields.iter().any(|field| field.name == *key) {
            return Err(AppError::bad_request(format!("unknown field: {key}")));
        }
    }
    for field in fields {
        let value = values.get(&field.name).unwrap_or(&Value::Null);
        let quoted = serde_json::to_string(&field.name).unwrap();
        if value.is_null() {
            if field.required {
                return Err(AppError::bad_request(format!("field {quoted} is required")));
            }
            continue;
        }
        match field.kind.as_str() {
            "text" | "number" => {
                let value = value.as_str().ok_or_else(|| {
                    AppError::bad_request(format!("field {quoted} must be a string"))
                })?;
                if field.required && value.trim().is_empty() {
                    return Err(AppError::bad_request(format!("field {quoted} is required")));
                }
                if field.kind == "number" && !value.trim().is_empty() && !valid_number(value) {
                    return Err(AppError::bad_request(format!(
                        "field {quoted} must be a valid number"
                    )));
                }
            }
            "boolean" if !value.is_boolean() => {
                return Err(AppError::bad_request(format!(
                    "field {quoted} must be true or false"
                )));
            }
            _ => {}
        }
    }
    Ok(())
}

// Go's ParseFloat accepts the decimal exponent notation used by the client,
// infinities/NaN, and hexadecimal floating-point literals. Overflow is an error.
fn valid_number(value: &str) -> bool {
    if matches!(
        value.to_ascii_lowercase().as_str(),
        "nan" | "inf" | "+inf" | "-inf" | "infinity" | "+infinity" | "-infinity"
    ) {
        return true;
    }
    if value.trim() != value {
        return false;
    }
    let unsigned = value.trim_start_matches(['+', '-']);
    if unsigned.starts_with("0x") || unsigned.starts_with("0X") {
        let raw = &unsigned[2..];
        let Some((mantissa, exponent)) = raw.split_once(['p', 'P']) else {
            return false;
        };
        if !valid_underscores(mantissa.strip_prefix('_').unwrap_or(mantissa), true)
            || !valid_underscores(exponent, false)
        {
            return false;
        }
        let exponent: i32 = match exponent.replace('_', "").parse() {
            Ok(n) => n,
            Err(_) => return false,
        };
        let mut digits = 0;
        let mut dot = false;
        let mut n = 0.0_f64;
        let mut fraction = 1.0_f64;
        for ch in mantissa.chars().filter(|ch| *ch != '_') {
            if ch == '.' && !dot {
                dot = true;
                continue;
            }
            let Some(d) = ch.to_digit(16) else {
                return false;
            };
            digits += 1;
            if dot {
                fraction /= 16.0;
                n += f64::from(d) * fraction;
            } else {
                n = n * 16.0 + f64::from(d);
            }
        }
        return digits > 0 && (n * 2_f64.powi(exponent)).is_finite();
    }
    if !valid_underscores(value, false) {
        return false;
    }
    value
        .replace('_', "")
        .parse::<f64>()
        .is_ok_and(f64::is_finite)
}

fn valid_underscores(value: &str, hex: bool) -> bool {
    let bytes = value.as_bytes();
    bytes.iter().enumerate().all(|(i, b)| {
        *b != b'_'
            || (i > 0
                && i + 1 < bytes.len()
                && (if hex {
                    bytes[i - 1].is_ascii_hexdigit() && bytes[i + 1].is_ascii_hexdigit()
                } else {
                    bytes[i - 1].is_ascii_digit() && bytes[i + 1].is_ascii_digit()
                }))
    })
}

pub fn note(value: Option<&str>) -> Result<()> {
    if value.is_some_and(|value| value.chars().count() > 20_000) {
        return Err(AppError::bad_request(
            "note must be at most 20000 characters",
        ));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn field_values_preserve_number_strings_and_boolean_types() {
        let fields = vec![
            Field {
                name: "dose".into(),
                kind: "number".into(),
                required: true,
            },
            Field {
                name: "taken".into(),
                kind: "boolean".into(),
                required: false,
            },
        ];
        for number in ["500", "1e-5", "0x1.fp2", "1_000", "NaN"] {
            assert!(
                values(
                    &fields,
                    json!({"dose":number,"taken":true}).as_object().unwrap()
                )
                .is_ok()
            );
        }
        for invalid in [
            json!({"dose":500}),
            json!({"dose":" "}),
            json!({"dose":"1e999"}),
            json!({"dose":"500","taken":"true"}),
            json!({"dose":"500","extra":1}),
        ] {
            assert!(values(&fields, invalid.as_object().unwrap()).is_err());
        }
    }

    #[test]
    fn names_use_bytes_while_notes_use_unicode_characters() {
        assert!(name(&"é".repeat(50)).is_ok());
        assert!(name(&"é".repeat(51)).is_err());
        assert!(note(Some(&"🙂".repeat(20_000))).is_ok());
        assert!(note(Some(&"🙂".repeat(20_001))).is_err());
    }
}
