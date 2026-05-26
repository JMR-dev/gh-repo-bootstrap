package githubapi

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	if len(args) < 3 {
		fmt.Fprintf(os.Stderr, "invalid args: %v\n", args)
		os.Exit(2)
	}

	command := args[0]
	subCmd := args[1]
	path := args[2]

	if command != "gh" || subCmd != "api" {
		fmt.Fprintf(os.Stderr, "expected command 'gh api', got: %s %s\n", command, subCmd)
		os.Exit(2)
	}

	switch {
	case path == "users/octocat":
		fmt.Print(`{"id":583234}`)
	case path == "users/error-user":
		fmt.Fprint(os.Stderr, "http error 404")
		os.Exit(1)
	case path == "users/no-id-user":
		fmt.Print(`{"login":"no-id-user"}`)
	case path == "users/bad-json-user":
		fmt.Print(`{invalid}`)
	case path == "orgs/JMR-dev/teams/release-managers":
		fmt.Print(`{"id":98765}`)
	case path == "orgs/JMR-dev/teams/error-team":
		fmt.Fprint(os.Stderr, "http error 404")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "unknown path: %s\n", path)
		os.Exit(2)
	}
}

func mockExec(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	return cmd
}

func TestResolveUser(t *testing.T) {
	oldExec := ExecCommand
	ExecCommand = mockExec
	defer func() { ExecCommand = oldExec }()

	r := New()

	// 1. Numeric ID passes through
	id, err := r.ResolveUser("12345")
	if err != nil {
		t.Fatalf("unexpected error for numeric user ID: %v", err)
	}
	if id != 12345 {
		t.Errorf("expected 12345, got %d", id)
	}

	// 2. Resolve login (with leading @)
	id, err = r.ResolveUser("@octocat")
	if err != nil {
		t.Fatalf("unexpected error for user @octocat: %v", err)
	}
	if id != 583234 {
		t.Errorf("expected 583234, got %d", id)
	}

	// 3. Cached lookup
	id, err = r.ResolveUser("octocat")
	if err != nil {
		t.Fatalf("unexpected error for user octocat (cached): %v", err)
	}
	if id != 583234 {
		t.Errorf("expected 583234, got %d", id)
	}

	// 4. API Error
	_, err = r.ResolveUser("error-user")
	if err == nil || !strings.Contains(err.Error(), "http error 404") {
		t.Errorf("expected http error 404, got %v", err)
	}

	// 5. No ID field in response
	_, err = r.ResolveUser("no-id-user")
	if err == nil || !strings.Contains(err.Error(), "returned no id") {
		t.Errorf("expected 'returned no id' error, got %v", err)
	}

	// 6. Bad JSON response
	_, err = r.ResolveUser("bad-json-user")
	if err == nil || !strings.Contains(err.Error(), "decoding gh api") {
		t.Errorf("expected decoding error, got %v", err)
	}
}

func TestResolveTeam(t *testing.T) {
	oldExec := ExecCommand
	ExecCommand = mockExec
	defer func() { ExecCommand = oldExec }()

	r := New()

	// 1. Numeric ID passes through
	id, err := r.ResolveTeam("54321")
	if err != nil {
		t.Fatalf("unexpected error for numeric team ID: %v", err)
	}
	if id != 54321 {
		t.Errorf("expected 54321, got %d", id)
	}

	// 2. Bare slug rejected
	_, err = r.ResolveTeam("release-managers")
	if err == nil || !strings.Contains(err.Error(), "must be in the form org/team-slug") {
		t.Errorf("expected format error, got %v", err)
	}

	// 3. Invalid formats
	for _, invalid := range []string{"org/", "/team"} {
		_, err = r.ResolveTeam(invalid)
		if err == nil || !strings.Contains(err.Error(), "must be in the form org/team-slug") {
			t.Errorf("expected format error for %q, got %v", invalid, err)
		}
	}

	// 4. Resolve slug
	id, err = r.ResolveTeam("JMR-dev/release-managers")
	if err != nil {
		t.Fatalf("unexpected error for team: %v", err)
	}
	if id != 98765 {
		t.Errorf("expected 98765, got %d", id)
	}

	// 5. Cached slug
	id, err = r.ResolveTeam("JMR-dev/release-managers")
	if err != nil {
		t.Fatalf("unexpected error for team (cached): %v", err)
	}
	if id != 98765 {
		t.Errorf("expected 98765, got %d", id)
	}

	// 6. API Error
	_, err = r.ResolveTeam("JMR-dev/error-team")
	if err == nil || !strings.Contains(err.Error(), "http error 404") {
		t.Errorf("expected http error 404, got %v", err)
	}
}
