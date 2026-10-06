/**
 * @file Tree-sitter grammar for the Sigil policy language
 * @license Apache-2.0
 *
 * One grammar covers the three kinds of document a `.sigil` file holds:
 * policies, modules and kinds, separated by `---` or by the next header.
 * It follows docs/reference/grammar.md and, where the two differ, the
 * hand-written parser in internal/parser, which is the language's
 * definition. It is more lenient than that parser in a few places the
 * type checker or the parser's own diagnostics cover anyway (see the
 * README), because an editor needs a tree for text that doesn't check.
 */

/// <reference types="tree-sitter-cli/dsl" />
// @ts-check

// Binding powers, lowest first, as in internal/parser/expr.go. A quantifier
// or a filter takes a full expression as its body, so it reduces below
// every infix operator: `any r in xs: a and b` is one quantifier.
const PREC = {
  binder: -1,
  or: 1,
  and: 2,
  not: 3,
  compare: 4,
  coalesce: 5,
  add: 6,
  unary: 7,
  postfix: 8,
};

// The reserved words of the language (docs/reference/lexical.md), but for
// `true`, `false` and `outcome`, which are rules of their own. `reason`,
// `accepts`, `min`, `max`, `list` and `map` aren't reserved: each means
// something in one place and is a name everywhere else.
const KEYWORDS = [
  'policy', 'module', 'use', 'as', 'param', 'let', 'pub', 'when', 'assert',
  'kind', 'version', 'enum', 'type', 'input', 'fn', 'decision',
  'precedence', 'collect', 'default', 'conflict',
  'and', 'or', 'xor', 'not', 'in', 'all', 'any', 'filter',
  'one', 'exclusive', 'has', 'like', 'matches', 'present',
];

// The relational operators, all at one level. `not in`, `all in`,
// `any in`, `one in` and `exclusive in` are two tokens each.
const COMPARE_OPS = [
  '==', '!=', '<', '<=', '>', '>=',
  'in', ['not', 'in'], ['all', 'in'], ['any', 'in'], ['one', 'in'], ['exclusive', 'in'],
  'has', 'like', 'matches',
];

module.exports = grammar({
  name: 'sigil',

  extras: $ => [/\s/, $.comment],

  word: $ => $.identifier,

  reserved: {
    global: $ => [...KEYWORDS, $.true, $.false, $.outcome],
  },

  supertypes: $ => [
    $._expression,
    $._type,
    $._statement,
    $._declaration,
  ],

  conflicts: $ => [
    // A dotted name is followed either by more segments or by the `.{` of
    // a selective import; which one shows only after the `.`.
    [$.policy_name],
  ],

  rules: {
    source_file: $ => repeat(choice($.separator, $._document)),

    separator: _ => '---',

    _document: $ => choice($.policy_document, $.module_document, $.kind_document),

    // ---------------------------------------------------------------
    // Policies and modules

    policy_document: $ => seq(
      field('header', $.policy_header),
      repeat($._statement),
    ),

    module_document: $ => seq(
      field('header', $.module_header),
      repeat($._statement),
    ),

    policy_header: $ => seq('policy', $._header_tail),

    module_header: $ => seq('module', $._header_tail),

    _header_tail: $ => seq(
      field('name', $.policy_name),
      ':',
      field('kind', $.identifier),
      optional(seq('@', field('version', $.integer))),
    ),

    // `deploy.common`. The parser also requires the dots to touch their
    // segments; the tree doesn't check that.
    policy_name: $ => seq($.identifier, repeat(seq('.', $.identifier))),

    // A module, unlike a policy, may only hold `use` and `let`. The
    // parser reports anything else and keeps going, so the tree accepts
    // the policy statements in both.
    _statement: $ => choice(
      $.use_statement,
      $.param_statement,
      $.let_statement,
      $.when_statement,
      $.assert_statement,
      $.call,
    ),

    use_statement: $ => seq(
      'use',
      field('path', $.policy_name),
      optional(choice(
        seq('as', field('alias', $.identifier)),
        field('imports', $.import_list),
      )),
    ),

    import_list: $ => seq('.', '{', commaSep1($.import_item), optional(','), '}'),

    import_item: $ => seq(
      field('name', $.identifier),
      optional(seq('as', field('alias', $.identifier))),
    ),

    param_statement: $ => seq(
      'param',
      field('name', $.identifier),
      ':',
      field('type', $._type),
      optional(seq('=', field('default', $._expression))),
      repeat(seq(',', field('bound', $.param_bound))),
    ),

    // `min:` and `max:` are names, not keywords.
    param_bound: $ => seq(
      field('name', alias(choice('min', 'max'), $.identifier)),
      ':',
      field('value', $._expression),
    ),

    let_statement: $ => seq(
      optional(field('pub', 'pub')),
      'let',
      field('name', $.identifier),
      '=',
      field('value', $._expression),
    ),

    when_statement: $ => seq(
      'when',
      field('condition', $._expression),
      field('body', $.block),
    ),

    // A rule body. A `use` or `param` inside one is an error the parser
    // reports; the tree takes any statement.
    block: $ => seq('{', repeat($._statement), '}'),

    assert_statement: $ => seq(
      'assert',
      '(',
      field('reason', $.string),
      ',',
      field('condition', $._expression),
      optional(','),
      ')',
    ),

    // A decision constructor or a policy invocation: `deny(reason: x)`.
    // The first argument may be positional, the old way to pass a reason,
    // which `sigil fmt` rewrites.
    call: $ => seq(
      field('name', $.identifier),
      field('arguments', $.call_arguments),
    ),

    call_arguments: $ => seq(
      '(',
      optional(seq(
        choice(
          $.named_argument,
          field('positional', $._expression),
        ),
        repeat(seq(',', $.named_argument)),
        optional(','),
      )),
      ')',
    ),

    named_argument: $ => seq(
      field('name', $._name),
      ':',
      field('value', $._expression),
    ),

    // ---------------------------------------------------------------
    // Kinds

    kind_document: $ => seq(
      field('header', $.kind_header),
      repeat($._declaration),
    ),

    kind_header: $ => seq(
      'kind',
      field('name', $.identifier),
      'version',
      field('version', $.integer),
      optional(seq(
        ',',
        'accepts',
        ':',
        field('accepts', $.integer),
      )),
    ),

    _declaration: $ => choice(
      $.enum_declaration,
      $.type_declaration,
      $.input_declaration,
      $.function_declaration,
      $.decision_declaration,
      $.precedence_declaration,
      $.exclusive_declaration,
      $.collect_declaration,
      $.default_declaration,
      $.conflict_declaration,
    ),

    enum_declaration: $ => seq(
      'enum',
      field('name', $.identifier),
      ':',
      $._alternatives,
    ),

    // `a | b | c`, an enum's values or a decision's reasons.
    _alternatives: $ => seq(
      field('value', $.identifier),
      repeat(seq('|', field('value', $.identifier))),
    ),

    type_declaration: $ => seq(
      'type',
      field('name', $.identifier),
      field('body', $.field_list),
    ),

    field_list: $ => seq('{', repeat($.field_declaration), '}'),

    field_declaration: $ => seq(
      field('name', $._name),
      ':',
      field('type', $._type),
    ),

    input_declaration: $ => seq(
      'input',
      field('name', $.identifier),
      ':',
      field('type', $._type),
    ),

    function_declaration: $ => seq(
      'fn',
      field('name', $.identifier),
      field('parameters', $.parameter_types),
      '->',
      field('result', $._type),
    ),

    parameter_types: $ => seq(
      '(',
      optional(seq(commaSep1($._type), optional(','))),
      ')',
    ),

    decision_declaration: $ => seq(
      'decision',
      field('name', $.identifier),
      // The old form, `decision name(fields) { reasons }`, which the kind
      // loader rejects and `sigil fmt` rewrites, may have either part.
      optional(field('parameters', $.legacy_payload_list)),
      field('body', choice($.decision_body, $.legacy_reason_list)),
    ),

    decision_body: $ => seq(
      '{',
      repeat(choice($.reason_field, $.payload_field)),
      '}',
    ),

    reason_field: $ => seq(
      field('name', alias('reason', $.identifier)),
      ':',
      $._alternatives,
    ),

    // A default ends before an operator at the level of a comparison or
    // looser, so that a field named like one, `in: string`, starts the next
    // field. The parser looks for the `:` instead, but a default is a
    // constant, which only `+`, `-` and literals make, so the checker rejects
    // every default the two read differently.
    payload_field: $ => prec.left(PREC.compare, seq(
      field('name', $._name),
      ':',
      field('type', $._type),
      optional(seq('=', field('default', $._expression))),
    )),

    legacy_payload_list: $ => seq(
      '(',
      optional(seq(commaSep1($.payload_field), optional(','))),
      ')',
    ),

    legacy_reason_list: $ => seq('{', repeat1(field('value', $.identifier)), '}'),

    // `precedence deny > review`, or `precedence approve: a > b` for one
    // decision's reasons.
    precedence_declaration: $ => seq(
      'precedence',
      optional(seq(field('scope', $.identifier), ':')),
      field('value', $.identifier),
      repeat(seq('>', field('value', $.identifier))),
    ),

    exclusive_declaration: $ => seq(
      'exclusive',
      $.outcome_reference,
      repeat1(seq(',', $.outcome_reference)),
    ),

    // A decision, or one of its reasons: `approve.release_manager`.
    outcome_reference: $ => seq(
      field('decision', $.identifier),
      optional(seq('.', field('reason', $.identifier))),
    ),

    collect_declaration: $ => seq('collect', field('mode', choice('one', 'all'))),

    default_declaration: $ => seq('default', field('call', $.call)),

    conflict_declaration: $ => seq('conflict', field('call', $.call)),

    // ---------------------------------------------------------------
    // Types

    _type: $ => choice(
      $.optional_type,
      $._base_type,
    ),

    optional_type: $ => seq('?', field('type', $._base_type)),

    _base_type: $ => choice(
      $.list_type,
      $.map_type,
      // `bool`, `string` and the other built-ins are names like a kind's
      // own types; the highlight query picks them out.
      alias($.identifier, $.type_identifier),
    ),

    list_type: $ => seq('list', '<', field('element', $._type), '>'),

    map_type: $ => seq(
      'map',
      '<',
      field('key', $._type),
      ',',
      field('value', $._type),
      '>',
    ),

    // ---------------------------------------------------------------
    // Expressions

    _expression: $ => choice(
      $.binary_expression,
      $.unary_expression,
      $.quantifier_expression,
      $.filter_expression,
      $.selector_expression,
      $.index_expression,
      $.call_expression,
      $.parenthesized_expression,
      $.list_literal,
      $.map_literal,
      $.identifier,
      $.integer,
      $.float,
      $.duration,
      $.string,
      $.raw_string,
      $.true,
      $.false,
      $.outcome,
    ),

    // Comparisons don't chain and `or` doesn't mix with `xor`; the parser
    // reports both, so the tree groups them like left-associative
    // operators instead of failing.
    binary_expression: $ => choice(
      ...[
        ['or', PREC.or],
        ['xor', PREC.or],
        ['and', PREC.and],
        ['+', PREC.add],
        ['-', PREC.add],
      ].map(([op, p]) => prec.left(p, seq(
        field('left', $._expression),
        field('operator', op),
        field('right', $._expression),
      ))),
      ...COMPARE_OPS.map(op => prec.left(PREC.compare, seq(
        field('left', $._expression),
        ...(Array.isArray(op) ? op.map(o => field('operator', o)) : [field('operator', op)]),
        field('right', $._expression),
      ))),
      prec.right(PREC.coalesce, seq(
        field('left', $._expression),
        field('operator', '??'),
        field('right', $._expression),
      )),
    ),

    unary_expression: $ => choice(
      prec(PREC.not, seq(field('operator', 'not'), field('operand', $._expression))),
      prec(PREC.unary, seq(field('operator', choice('-', 'present')), field('operand', $._expression))),
    ),

    // `any r in actor.roles: r like "sre-*"`. The body runs as far right
    // as the expression around it allows.
    quantifier_expression: $ => prec.right(PREC.binder, seq(
      field('quantifier', choice('any', 'all')),
      $._binder,
    )),

    filter_expression: $ => prec.right(PREC.binder, seq('filter', $._binder)),

    _binder: $ => seq(
      field('variable', $.identifier),
      'in',
      field('range', $._expression),
      ':',
      field('body', $._expression),
    ),

    selector_expression: $ => prec(PREC.postfix, seq(
      field('operand', $._expression),
      field('operator', choice('.', '?.')),
      field('field', $._name),
    )),

    index_expression: $ => prec(PREC.postfix, seq(
      field('operand', $._expression),
      '[',
      field('index', $._expression),
      ']',
    )),

    call_expression: $ => prec(PREC.postfix, seq(
      field('function', $._expression),
      field('arguments', $.arguments),
    )),

    arguments: $ => seq(
      '(',
      optional(seq(commaSep1($._expression), optional(','))),
      ')',
    ),

    parenthesized_expression: $ => seq('(', $._expression, ')'),

    list_literal: $ => seq(
      '[',
      optional(seq(commaSep1($._expression), optional(','))),
      ']',
    ),

    map_literal: $ => seq(
      '{',
      optional(seq(commaSep1($.map_entry), optional(','))),
      '}',
    ),

    // The parser reads a key at the level of `??`, so a comparison used
    // as a key needs parentheses there; the tree doesn't insist.
    map_entry: $ => seq(
      field('key', $._expression),
      ':',
      field('value', $._expression),
    ),

    // ---------------------------------------------------------------
    // Names

    // A field, payload or argument name, which may be spelled like a
    // keyword: `resource.kind`, `type Service { type: string }`.
    _name: $ => choice(
      $.identifier,
      alias(choice(...KEYWORDS, $.true, $.false, $.outcome), $.identifier),
    ),

    identifier: _ => /[A-Za-z_][A-Za-z0-9_]*/,

    // ---------------------------------------------------------------
    // Literals

    integer: _ => /[0-9]+/,

    float: _ => /[0-9]+\.[0-9]+/,

    // `1h30m`. The lexer also checks that each unit appears once and
    // largest first; the tree only reads the shape.
    duration: _ => /([0-9]+(ms|s|m|h|d))+/,

    string: $ => seq(
      '"',
      repeat(choice(
        $.string_content,
        $.escape_sequence,
      )),
      token.immediate('"'),
    ),

    string_content: _ => token.immediate(prec(1, /[^"\\\n]+/)),

    // Go's escapes for interpreted string literals.
    escape_sequence: _ => token.immediate(seq(
      '\\',
      choice(
        /[abfnrtv\\"]/,
        /x[0-9A-Fa-f]{2}/,
        /u[0-9A-Fa-f]{4}/,
        /U[0-9A-Fa-f]{8}/,
        /[0-7]{3}/,
      ),
    )),

    raw_string: _ => /`[^`]*`/,

    true: _ => 'true',

    false: _ => 'false',

    // The decision an `assert` checks.
    outcome: _ => 'outcome',

    comment: _ => token(seq('//', /[^\r\n]*/)),
  },
});

/**
 * One or more rules separated by commas.
 *
 * @param {RuleOrLiteral} rule
 * @returns {SeqRule}
 */
function commaSep1(rule) {
  return seq(rule, repeat(seq(',', rule)));
}
