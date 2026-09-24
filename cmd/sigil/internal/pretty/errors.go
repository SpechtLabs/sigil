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
// usage errors cobra and pflag produce.
const usageAdvice = "run the command with --help to see its usage"

// ErrorHandler is a fang.ErrorHandler that renders errors with Printer.Err.
func ErrorHandler(w io.Writer, _ fang.Styles, err error) {
	_ = New(w).Err(err)
}

// Err renders err with its advice and chain of causes:
//
//	 ERROR  failed to load kind
//
//	What you can do
//	  • check that the file exists
//
//	Caused by
//	  • open deploy.sigil: no such file or directory
func (p *Printer) Err(err error) humane.Error {
	if err == nil {
		return nil
	}

	msg, advice, causes := unpack(err)

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
// (innermost first, as humane's own Display does), and the causes beneath it.
func unpack(err error) (msg string, advice, causes []string) {
	var he humane.Error
	if !errors.As(err, &he) {
		return strings.TrimSpace(err.Error()), []string{usageAdvice}, nil
	}

	advice = append(advice, he.Advice()...)
	for cause := he.Cause(); cause != nil; cause = errors.Unwrap(cause) {
		causes = append(causes, cause.Error())
		if h, ok := cause.(interface{ Advice() []string }); ok {
			advice = append(h.Advice(), advice...)
		}
	}

	return he.Error(), advice, causes
}
