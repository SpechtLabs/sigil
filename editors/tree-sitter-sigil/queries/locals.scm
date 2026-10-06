; Scopes. Every document is a namespace of its own; a rule body scopes the
; lets in it, and a quantifier or a filter its variable.

[
  (policy_document)
  (module_document)
  (kind_document)
  (block)
  (quantifier_expression)
  (filter_expression)
] @local.scope

; Definitions

(param_statement
  name: (identifier) @local.definition.parameter)

(let_statement
  name: (identifier) @local.definition.var)

(use_statement
  alias: (identifier) @local.definition.namespace)

(import_item
  !alias
  name: (identifier) @local.definition.import)

(import_item
  alias: (identifier) @local.definition.import)

(input_declaration
  name: (identifier) @local.definition.var)

(function_declaration
  name: (identifier) @local.definition.function)

(type_declaration
  name: (identifier) @local.definition.type)

(enum_declaration
  name: (identifier) @local.definition.enum
  value: (identifier) @local.definition.constant)

(decision_declaration
  name: (identifier) @local.definition.type)

(field_declaration
  name: (identifier) @local.definition.field)

(payload_field
  name: (identifier) @local.definition.field)

(quantifier_expression
  variable: (identifier) @local.definition.parameter)

(filter_expression
  variable: (identifier) @local.definition.parameter)

; References

(identifier) @local.reference
