; Text objects for nvim-treesitter-textobjects.

; A rule is what a policy is made of, so it's the function text object,
; and so is a host function's declaration.
(when_statement) @function.outer

(when_statement
  body: (block
    .
    "{"
    _+ @function.inner
    "}"))

(function_declaration) @function.outer

(function_declaration
  parameters: (parameter_types
    .
    "("
    _+ @function.inner
    ")"))

; Documents, struct types and decisions are the class text objects.
[
  (policy_document)
  (module_document)
  (kind_document)
  (type_declaration)
  (decision_declaration)
] @class.outer

(policy_document
  header: (_)
  _+ @class.inner)

(module_document
  header: (_)
  _+ @class.inner)

(kind_document
  header: (_)
  _+ @class.inner)

(type_declaration
  body: (field_list
    .
    "{"
    _+ @class.inner
    "}"))

(decision_declaration
  body: (_
    .
    "{"
    _+ @class.inner
    "}"))

; Rules are conditionals too: the condition, and the body.
(when_statement) @conditional.outer

(when_statement
  condition: (_) @conditional.inner)

(when_statement
  body: (block
    .
    "{"
    _+ @conditional.inner
    "}"))

; Blocks
[
  (block)
  (field_list)
  (decision_body)
  (legacy_reason_list)
] @block.outer

[
  (block
    .
    "{"
    _+ @block.inner
    "}")
  (field_list
    .
    "{"
    _+ @block.inner
    "}")
  (decision_body
    .
    "{"
    _+ @block.inner
    "}")
  (legacy_reason_list
    .
    "{"
    _+ @block.inner
    "}")
]

; Quantifiers and filters loop over a collection.
[
  (quantifier_expression)
  (filter_expression)
] @loop.outer

(quantifier_expression
  body: (_) @loop.inner)

(filter_expression
  body: (_) @loop.inner)

; Calls: decision constructors, policy invocations and host functions.
[
  (call)
  (call_expression)
] @call.outer

(call
  arguments: (call_arguments
    .
    "("
    _+ @call.inner
    ")"))

(call_expression
  arguments: (arguments
    .
    "("
    _+ @call.inner
    ")"))

; Parameters: arguments, a function's parameter types and imported names,
; each with the comma on one side.
(call_arguments
  "," @parameter.outer
  .
  (_) @parameter.inner @parameter.outer)

(call_arguments
  .
  (_) @parameter.inner @parameter.outer
  .
  ","? @parameter.outer)

(arguments
  "," @parameter.outer
  .
  (_) @parameter.inner @parameter.outer)

(arguments
  .
  (_) @parameter.inner @parameter.outer
  .
  ","? @parameter.outer)

(parameter_types
  "," @parameter.outer
  .
  (_) @parameter.inner @parameter.outer)

(parameter_types
  .
  (_) @parameter.inner @parameter.outer
  .
  ","? @parameter.outer)

(legacy_payload_list
  "," @parameter.outer
  .
  (_) @parameter.inner @parameter.outer)

(legacy_payload_list
  .
  (_) @parameter.inner @parameter.outer
  .
  ","? @parameter.outer)

(import_list
  "," @parameter.outer
  .
  (_) @parameter.inner @parameter.outer)

(import_list
  .
  (_) @parameter.inner @parameter.outer
  .
  ","? @parameter.outer)

; Statements and declarations
(policy_document
  (_statement) @statement.outer)

(module_document
  (_statement) @statement.outer)

(block
  (_statement) @statement.outer)

(kind_document
  (_declaration) @statement.outer)

; Assignments: lets, and params and payload fields with a default.
(let_statement
  name: (_) @assignment.lhs
  value: (_) @assignment.rhs @assignment.inner) @assignment.outer

(param_statement
  name: (_) @assignment.lhs
  default: (_) @assignment.rhs @assignment.inner) @assignment.outer

(payload_field
  name: (_) @assignment.lhs
  default: (_) @assignment.rhs @assignment.inner) @assignment.outer

; The pattern after `matches`, with and without its quotes.
(binary_expression
  operator: "matches"
  right: (_) @regex.outer)

(binary_expression
  operator: "matches"
  right: (_) @regex.inner
  (#offset! @regex.inner 0 1 0 -1))

[
  (integer)
  (float)
  (duration)
] @number.inner

(comment) @comment.outer
