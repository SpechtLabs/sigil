package pretty

import (
	"errors"
	"io"
	"strings"

	"charm.land/fang/v2"
	"github.com/sierrasoftworks/humane-errors-go"
)

// usageAdvice is shown for errors that don't carry their own advice. Every
// error sigil itself returns is a humane.Error, so in practice these are the
// usage errors cobra and pflag produce that Execute didn't translate.
const usageAdvice = "run the command with --help to see its usage"

// ErrorHandler is a [fang.ErrorHandler] that renders errors with
// [Printer.Err] on a new [Printer] for w. It ignores fang's styles, since
// the printer has its own.
func ErrorHandler(w io.Writer, _ fang.Styles, err error) {
	_ = New(w).Err(err)
}

// Err renders err with its advice and chain of causes:
//
//	 ERROR  the kind file couldn't be read
//
//	What you can do
//	  • pass the exported kind file with --kind
//
//	Caused by
//	  • open deploy.sigil: no such file or directory
//
// A Failed error was reported by the command already, so nothing is
// printed. A Diagnostics error prints its diagnostics first.
func (p *Printer) Err(err error) humane.Error {
	if err == nil || Reported(err) {
		return nil
	}
	if d, ok := errors.AsType[*Diagnostics](err); ok {
		if werr := p.Diagnostics(d.Errs, d.Src); werr != nil {
			return werr
		}
		return p.errBlock(d.Message(), d.Advice(), nil)
	}
	msg, advice, causes := unpack(err)
	return p.errBlock(msg, advice, causes)
}

func (p *Printer) errBlock(msg string, advice, causes []string) humane.Error {
	var b strings.Builder
	if p.tty {
		b.WriteString(p.styles.badge.Render("ERROR") + " " + p.styles.fail.Render(msg) + "\n")
	} else {
		b.WriteString("Error: " + msg + "\n")
	}
	bullet := p.styles.info.Render("•")
	if len(advice) > 0 {
		b.WriteString("\n" + p.styles.section.Render("What you can do") + "\n")
		for _, a := range advice {
			b.WriteString("  " + bullet + " " + p.styles.text.Render(a) + "\n")
		}
	}
	if len(causes) > 0 {
		b.WriteString("\n" + p.styles.section.Render("Caused by") + "\n")
		for _, c := range causes {
			b.WriteString("  " + bullet + " " + p.styles.muted.Render(c) + "\n")
		}
	}
	return p.write(b.String())
}

// unpack splits err into its message, the advice collected along its chain
// (innermost first, as humane's own Display does), and the causes beneath
// it. A cause that only repeats the end of the one above it, the way a
// bare errno repeats the *os.PathError that wraps it, is left out.
func unpack(err error) (msg string, advice, causes []string) {
	var he humane.Error
	if !errors.As(err, &he) {
		return strings.TrimSpace(err.Error()), []string{usageAdvice}, nil
	}
	advice = append(advice, he.Advice()...)
	for cause := he.Cause(); cause != nil; cause = errors.Unwrap(cause) {
		text := cause.Error()
		if n := len(causes); n == 0 || !strings.HasSuffix(causes[n-1], text) {
			causes = append(causes, text)
		}
		if h, ok := cause.(interface{ Advice() []string }); ok {
			advice = append(h.Advice(), advice...)
		}
	}
	return he.Error(), advice, causes
}
