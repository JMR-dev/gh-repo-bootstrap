package prompt

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestIsInteractive(t *testing.T) {
	oldIsTerminal := IsTerminal
	defer func() { IsTerminal = oldIsTerminal }()

	// Test interactive mode
	IsTerminal = func(fd int) bool {
		return true
	}
	if !IsInteractive() {
		t.Error("expected IsInteractive to be true")
	}

	// Test non-interactive mode
	IsTerminal = func(fd int) bool {
		return false
	}
	if IsInteractive() {
		t.Error("expected IsInteractive to be false")
	}
}

func TestNew(t *testing.T) {
	reader := New()
	if reader == nil {
		t.Fatal("expected reader to not be nil")
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("read error")
}

func TestReader_Line(t *testing.T) {
	// 1. Success path
	in := bytes.NewBufferString("hello\n")
	out := &bytes.Buffer{}
	p := NewFromReader(in, out)
	val, err := p.Line("Enter something: ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "hello" {
		t.Errorf("expected 'hello', got %q", val)
	}
	if out.String() != "Enter something: " {
		t.Errorf("expected prompt output, got %q", out.String())
	}

	// 2. EOF with input
	in = bytes.NewBufferString("hello")
	out = &bytes.Buffer{}
	p = NewFromReader(in, out)
	val, err = p.Line("Enter: ")
	if err != nil {
		t.Fatalf("unexpected error on EOF with content: %v", err)
	}
	if val != "hello" {
		t.Errorf("expected 'hello', got %q", val)
	}

	// 3. Reader error
	out = &bytes.Buffer{}
	p = NewFromReader(errReader{}, out)
	_, err = p.Line("Enter: ")
	if err == nil || !strings.Contains(err.Error(), "read error") {
		t.Errorf("expected read error, got %v", err)
	}
}

func TestReader_Choice(t *testing.T) {
	// 1. Valid choice first attempt
	in := bytes.NewBufferString("public\n")
	out := &bytes.Buffer{}
	p := NewFromReader(in, out)
	val, err := p.Choice("Visibility [public/private]: ", "private", []string{"public", "private"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "public" {
		t.Errorf("expected 'public', got %q", val)
	}

	// 2. Use default choice on empty line
	in = bytes.NewBufferString("\n")
	out = &bytes.Buffer{}
	p = NewFromReader(in, out)
	val, err = p.Choice("Visibility [public/private]: ", "private", []string{"public", "private"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "private" {
		t.Errorf("expected 'private', got %q", val)
	}

	// 3. Invalid choice followed by valid choice
	in = bytes.NewBufferString("invalid\nprivate\n")
	out = &bytes.Buffer{}
	p = NewFromReader(in, out)
	val, err = p.Choice("Visibility: ", "private", []string{"public", "private"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "private" {
		t.Errorf("expected 'private', got %q", val)
	}
	if !strings.Contains(out.String(), "invalid value \"invalid\"") {
		t.Errorf("expected warning in output, got %q", out.String())
	}

	// 4. Exceed maximum attempts
	in = bytes.NewBufferString("invalid1\ninvalid2\ninvalid3\n")
	out = &bytes.Buffer{}
	p = NewFromReader(in, out)
	_, err = p.Choice("Visibility: ", "private", []string{"public", "private"})
	if err == nil || !strings.Contains(err.Error(), "no valid answer after 3 attempts") {
		t.Errorf("expected error, got %v", err)
	}

	// 5. Line read error inside Choice
	out = &bytes.Buffer{}
	p = NewFromReader(errReader{}, out)
	_, err = p.Choice("Visibility: ", "private", []string{"public", "private"})
	if err == nil || !strings.Contains(err.Error(), "read error") {
		t.Errorf("expected read error, got %v", err)
	}
}
