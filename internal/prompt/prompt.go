// Package prompt provides minimal TTY-aware prompts used by --create.
package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// IsInteractive reports whether stdin is a terminal.
func IsInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// Reader reads prompts from in and writes them to out. Use New() for the
// default stdin/stderr pairing.
type Reader struct {
	r *bufio.Reader
	w io.Writer
}

// New returns a Reader backed by stdin / stderr.
func New() *Reader {
	return &Reader{r: bufio.NewReader(os.Stdin), w: os.Stderr}
}

// NewFromReader is exposed for tests.
func NewFromReader(in io.Reader, out io.Writer) *Reader {
	return &Reader{r: bufio.NewReader(in), w: out}
}

// ErrNotInteractive is returned when a prompt is needed but stdin is not a TTY.
var ErrNotInteractive = errors.New("input is not a terminal and a required value was not provided")

// Line writes msg to the output stream and returns one trimmed line of input.
func (p *Reader) Line(msg string) (string, error) {
	fmt.Fprint(p.w, msg)
	s, err := p.r.ReadString('\n')
	if err != nil && (err != io.EOF || s == "") {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

// Choice reads a value from prompt and validates it against allowed. If the
// input is empty, def is returned.
func (p *Reader) Choice(msg, def string, allowed []string) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		v, err := p.Line(msg)
		if err != nil {
			return "", err
		}
		if v == "" {
			v = def
		}
		for _, a := range allowed {
			if v == a {
				return v, nil
			}
		}
		fmt.Fprintf(p.w, "  invalid value %q; expected one of: %s\n", v, strings.Join(allowed, ", "))
	}
	return "", fmt.Errorf("no valid answer after 3 attempts")
}
