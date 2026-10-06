; TODO, FIXME and the like in comments.
((comment) @injection.content
  (#set! injection.language "comment"))

; The pattern after `matches` is an RE2 regular expression.
(binary_expression
  operator: "matches"
  right: (raw_string) @injection.content
  (#offset! @injection.content 0 1 0 -1)
  (#set! injection.language "regex"))
