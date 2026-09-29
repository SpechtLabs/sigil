// Package lexer turns Sigil source text into tokens.
//
// The lexer is the first stage of the compiler. The parser in
// internal/parser pulls tokens from it one at a time and builds the syntax
// tree, and the formatter in internal/format runs a second lexer over the
// same source to collect the comments the tree doesn't keep. [New] creates a
// [Lexer] over a byte slice, [Lexer.Next] returns one [token.Token] per call,
// and [Lexer.Errors] returns the lexical errors found so far.
//
// # Scanning
//
// The lexer is context-free: it never looks at what came before to decide
// what a token is, and it takes the longest match at every position, as
// https://sigil.specht-labs.de/reference/lexical/ specifies. Everything that needs context, such as
// reading `deploy.common` as one policy name or splitting `>=` after a type
// argument, is the parser's job. Whitespace is skipped; comments are
// returned as [token.Comment] tokens, which the parser skips.
//
// # Errors
//
// Lexical errors don't stop the lexer. It records a [diag.Error], emits a
// [token.Illegal] token covering the bad text, and carries on, so one typo
// produces one diagnostic instead of hiding everything after it. The
// recorded errors have no File; the caller that knows the file name sets it.
//
// # Literal decoders
//
// Each kind of token has its own file, holding the scanner method and, for
// literals, the decoder. The literal decoders ([ParseInt], [ParseFloat],
// [ParseDuration] and [Unquote]) serve two callers. The lexer runs them to
// validate a literal's text as soon as it's scanned, so a lexical error is
// reported where the literal is. The parser runs them again to get the value
// for the AST, and can rely on them succeeding for any token the lexer
// accepted. Keeping one implementation means the two can't disagree about
// what a literal is worth.
//
// The decoders return a [*diag.Error] rather than an error so the lexer can
// read the message and hint. Any position in an error they return is
// relative to the literal: Offset counts bytes and Column counts characters
// from the literal's first character, and Line is unset.
package lexer
