; Sigil highlights, with nvim-treesitter's capture names. Later patterns
; win over earlier ones, so the general cases come first.

; Names

(identifier) @variable

(type_identifier) @type

((type_identifier) @type.builtin
  (#any-of? @type.builtin "bool" "int" "float" "string" "duration" "timestamp"))

[
  "list"
  "map"
] @type.builtin

; Documents

(policy_name
  (identifier) @module)

(policy_header
  kind: (identifier) @type)

(module_header
  kind: (identifier) @type)

(kind_header
  name: (identifier) @type.definition)

(use_statement
  alias: (identifier) @module)

; Policy statements

(param_statement
  name: (identifier) @variable.parameter)

(param_bound
  name: (identifier) @property)

(let_statement
  name: (identifier) @variable)

; A decision constructor or a policy invocation.
(call
  name: (identifier) @constructor)

(named_argument
  name: (identifier) @variable.parameter)

; A constructor's reason names one of the decision's reasons.
((named_argument
  name: (identifier) @variable.parameter
  value: (identifier) @constant)
  (#eq? @variable.parameter "reason"))

; Kind declarations

(enum_declaration
  name: (identifier) @type.definition
  value: (identifier) @constant)

(type_declaration
  name: (identifier) @type.definition)

(field_declaration
  name: (identifier) @variable.member)

(input_declaration
  name: (identifier) @variable)

(function_declaration
  name: (identifier) @function)

(decision_declaration
  name: (identifier) @constructor)

(payload_field
  name: (identifier) @variable.member)

(reason_field
  name: (identifier) @variable.member
  value: (identifier) @constant)

(legacy_reason_list
  value: (identifier) @constant)

(precedence_declaration
  value: (identifier) @constructor)

; With a scope, precedence orders that decision's reasons.
(precedence_declaration
  scope: (identifier) @constructor
  value: (identifier) @constant)

(outcome_reference
  decision: (identifier) @constructor
  reason: (identifier)? @constant)

; Expressions

(selector_expression
  field: (identifier) @variable.member)

(call_expression
  function: (identifier) @function.call)

(quantifier_expression
  variable: (identifier) @variable.parameter)

(filter_expression
  variable: (identifier) @variable.parameter)

; Literals

[
  (string)
  (raw_string)
] @string

(escape_sequence) @string.escape

; The pattern after `matches` is a regular expression, the one after
; `like` a glob.
(binary_expression
  operator: "matches"
  right: [
    (string)
    (raw_string)
  ] @string.regexp)

(binary_expression
  operator: "like"
  right: [
    (string)
    (raw_string)
  ] @string.special)

[
  (integer)
  (duration)
] @number

(float) @number.float

[
  (true)
  (false)
] @boolean

(outcome) @variable.builtin

; Spell-check comments. The capture comes first, so a highlighter that
; reports one capture per node reports @comment.
(comment) @spell

(comment) @comment

; Keywords

[
  "policy"
  "module"
  "use"
  "as"
] @keyword.import

[
  "kind"
  "type"
  "enum"
  "decision"
] @keyword.type

[
  "param"
  "let"
  "assert"
  "version"
  "accepts"
  "input"
  "precedence"
  "exclusive"
  "collect"
  "default"
  "conflict"
] @keyword

(collect_declaration
  mode: _ @keyword)

"pub" @keyword.modifier

"fn" @keyword.function

"when" @keyword.conditional

[
  "and"
  "or"
  "xor"
  "not"
  "in"
  "has"
  "like"
  "matches"
  "present"
] @keyword.operator

(binary_expression
  operator: [
    "all"
    "any"
    "one"
    "exclusive"
  ] @keyword.operator)

(quantifier_expression
  quantifier: _ @keyword.repeat)

"filter" @keyword.repeat

; Operators and punctuation

[
  "=="
  "!="
  "<"
  "<="
  ">"
  ">="
  "+"
  "-"
  "??"
  "?."
  "?"
  "="
  "->"
  "|"
  "@"
] @operator

[
  "."
  ","
  ":"
] @punctuation.delimiter

[
  "("
  ")"
  "["
  "]"
  "{"
  "}"
] @punctuation.bracket

; The angle brackets of a type argument list.
(list_type
  [
    "<"
    ">"
  ] @punctuation.bracket)

(map_type
  [
    "<"
    ">"
  ] @punctuation.bracket)

(import_list
  "." @punctuation.delimiter)

(separator) @punctuation.special
