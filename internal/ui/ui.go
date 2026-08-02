// Package ui renders command output.
//
// Every check reports one of ok / warn / fail with a short reason, so `doctor`
// output stays scannable and a failure names the thing to fix instead of dumping
// a stack trace at you.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Status is the outcome of a single check.
type Status int

const (
	OK Status = iota
	Warn
	Fail
	Info
)

func (s Status) marker() string {
	switch s {
	case OK:
		return "ok  "
	case Warn:
		return "warn"
	case Fail:
		return "FAIL"
	default:
		return "    "
	}
}

// Printer writes human-readable output.
type Printer struct {
	Out     io.Writer
	Err     io.Writer
	Verbose bool
}

// New returns a Printer writing to stdout and stderr.
func New() *Printer { return &Printer{Out: os.Stdout, Err: os.Stderr} }

// Section prints a heading.
func (p *Printer) Section(title string) {
	fmt.Fprintf(p.Out, "\n%s\n", title)
	fmt.Fprintf(p.Out, "%s\n", strings.Repeat("-", len(title)))
}

// Check prints one check result. detail may be empty.
func (p *Printer) Check(s Status, label, detail string) {
	if detail == "" {
		fmt.Fprintf(p.Out, "  [%s] %s\n", s.marker(), label)
		return
	}
	fmt.Fprintf(p.Out, "  [%s] %-34s %s\n", s.marker(), label, detail)
}

// Line prints an unadorned line.
func (p *Printer) Line(format string, args ...any) {
	fmt.Fprintf(p.Out, format+"\n", args...)
}

// Detail prints an indented continuation line.
func (p *Printer) Detail(format string, args ...any) {
	fmt.Fprintf(p.Out, "         "+format+"\n", args...)
}

// Debug prints only with --verbose.
func (p *Printer) Debug(format string, args ...any) {
	if p.Verbose {
		fmt.Fprintf(p.Out, "  . "+format+"\n", args...)
	}
}

// Warn prints a warning to stderr.
func (p *Printer) Warn(format string, args ...any) {
	fmt.Fprintf(p.Err, "warning: "+format+"\n", args...)
}
