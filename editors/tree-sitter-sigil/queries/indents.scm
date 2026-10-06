; Indentation in sigil fmt's style: two spaces inside every bracket.

[
  (block)
  (import_list)
  (field_list)
  (decision_body)
  (legacy_payload_list)
  (legacy_reason_list)
  (call_arguments)
  (arguments)
  (parameter_types)
  (list_literal)
  (map_literal)
  (parenthesized_expression)
  (index_expression)
] @indent.begin

[
  "}"
  ")"
  "]"
] @indent.branch @indent.end

; A raw string keeps its lines as written.
(raw_string) @indent.ignore

(comment) @indent.auto
