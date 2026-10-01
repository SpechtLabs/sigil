//! The types a kind is built from, the twin of the Go types `policy.NewKind`
//! reflects over: strings, lists, enums and structs. Build them with the
//! constructors on [`Type`].

use std::sync::Arc;

/// A Sigil type.
#[derive(Debug, Clone, PartialEq)]
pub enum Type {
    String,
    Bool,
    /// A 64-bit integer.
    Int,
    Float,
    /// A duration in Sigil's syntax, like `1h30m`; see [`crate::parse_duration`].
    Duration,
    /// An RFC 3339 string, going in and coming out.
    Timestamp,
    List(Box<Type>),
    /// Keys cross as JSON object keys: `"3"` for an int, `"45m"` for a duration.
    Map(Box<Type>, Box<Type>),
    /// `?T`: a value, or none. As a struct field, the key may be left out.
    Optional(Box<Type>),
    Enum(Arc<EnumDef>),
    Struct(Arc<StructDef>),
}

/// An enum: a closed set of names, written bare in policies.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct EnumDef {
    pub name: String,
    pub values: Vec<String>,
}

/// A struct type: named fields, each with its type, in declaration order.
#[derive(Debug, Clone, PartialEq)]
pub struct StructDef {
    pub name: String,
    pub fields: Vec<(String, Type)>,
}

impl Type {
    pub fn string() -> Self {
        Type::String
    }

    pub fn bool() -> Self {
        Type::Bool
    }

    pub fn int() -> Self {
        Type::Int
    }

    pub fn float() -> Self {
        Type::Float
    }

    pub fn duration() -> Self {
        Type::Duration
    }

    pub fn timestamp() -> Self {
        Type::Timestamp
    }

    /// `list<T>`.
    pub fn list(elem: Type) -> Self {
        Type::List(Box::new(elem))
    }

    /// `map<K, V>`.
    pub fn map(key: Type, value: Type) -> Self {
        Type::Map(Box::new(key), Box::new(value))
    }

    /// `?T`.
    pub fn optional(elem: Type) -> Self {
        Type::Optional(Box::new(elem))
    }

    /// An enum: `Type::enumeration("Severity", ["critical", "warning", "info"])`.
    /// Policies write its values bare, and the kind prints it before the types
    /// that use it.
    pub fn enumeration<V: Into<String>>(name: impl Into<String>, values: impl IntoIterator<Item = V>) -> Self {
        Type::Enum(Arc::new(EnumDef { name: name.into(), values: values.into_iter().map(Into::into).collect() }))
    }

    /// A struct type: `Type::structure("Team", [("name", Type::string())])`.
    /// Field order is declaration order in the kind file.
    pub fn structure<N: Into<String>>(name: impl Into<String>, fields: impl IntoIterator<Item = (N, Type)>) -> Self {
        Type::Struct(Arc::new(StructDef { name: name.into(), fields: fields.into_iter().map(|(n, t)| (n.into(), t)).collect() }))
    }

    /// The type as a kind file writes it, like `list<string>` or `?Team`.
    pub fn sigil(&self) -> String {
        match self {
            Type::String => "string".into(),
            Type::Bool => "bool".into(),
            Type::Int => "int".into(),
            Type::Float => "float".into(),
            Type::Duration => "duration".into(),
            Type::Timestamp => "timestamp".into(),
            Type::List(elem) => format!("list<{}>", elem.sigil()),
            Type::Map(key, value) => format!("map<{}, {}>", key.sigil(), value.sigil()),
            Type::Optional(elem) => format!("?{}", elem.sigil()),
            Type::Enum(e) => e.name.clone(),
            Type::Struct(s) => s.name.clone(),
        }
    }

    /// The types a list, map or optional holds, in the order Go converts them.
    pub(crate) fn inner(&self) -> Vec<&Type> {
        match self {
            Type::List(elem) | Type::Optional(elem) => vec![elem],
            Type::Map(key, value) => vec![key, value],
            _ => Vec::new(),
        }
    }
}

impl std::fmt::Display for Type {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.sigil())
    }
}
