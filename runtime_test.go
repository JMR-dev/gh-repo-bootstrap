package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// captureStderr redirects os.Stderr for the duration of fn and returns what
// was written. Used to verify the side-effecting print helpers without
// asserting against the real terminal.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	w.Close()
	return <-done
}

func TestUsageAndErrf(t *testing.T) {
	out := captureStderr(t, func() { usage() })
	if !strings.Contains(out, "gh repo-bootstrap <owner/repo>") {
		t.Errorf("usage output missing header:\n%s", out)
	}

	out = captureStderr(t, func() { errf("hello %s %d", "world", 42) })
	want := "gh-repo-bootstrap: hello world 42\n"
	if out != want {
		t.Errorf("errf = %q want %q", out, want)
	}
}

func TestParseArgsHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		_, err := parseArgs([]string{flag})
		if !errors.Is(err, errHelp) {
			t.Errorf("expected errHelp for %s, got %v", flag, err)
		}
	}
}

func TestParseArgsAllFlags(t *testing.T) {
	args := []string{
		"o/r",
		"--branch", "trunk",
		"--reviews", "0",
		"--signed",
		"--ruleset", "rs",
		"--env", "production",
		"--env", "staging",
		"--bypass", "Team:7:pull_request",
		"--upload-repo-secrets", "/tmp/repo.tfvars",
		"--upload-env-secrets", "/tmp/envs",
		"--state-dir", "/tmp/state",
		"--destroy",
	}
	opts, err := parseArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	want := &options{
		repo: "o/r", owner: "o", name: "r",
		branch: "trunk", reviews: 0, signed: true, ruleset: "rs",
		envs:            []string{"production", "staging"},
		bypass:          []bypassActor{{"Team", 7, "pull_request"}},
		repoSecretsFile: "/tmp/repo.tfvars",
		envSecretsDir:   "/tmp/envs",
		stateDir:        "/tmp/state",
		action:          "destroy",
	}
	if !reflect.DeepEqual(opts, want) {
		t.Errorf("got  %+v\nwant %+v", opts, want)
	}
}

func TestParseArgsDoubleDash(t *testing.T) {
	// Positional repo after `--`.
	opts, err := parseArgs([]string{"--", "o/r"})
	if err != nil || opts.repo != "o/r" {
		t.Errorf("got %+v err %v", opts, err)
	}
	// Two positionals after `--` is an error.
	if _, err := parseArgs([]string{"--", "o/r", "extra"}); err == nil {
		t.Errorf("expected error for two positionals after --")
	}
}

func TestParseArgsMissingValue(t *testing.T) {
	for _, args := range [][]string{
		{"o/r", "--branch"},
		{"o/r", "--reviews"},
		{"o/r", "--ruleset"},
		{"o/r", "--env"},
		{"o/r", "--bypass"},
		{"o/r", "--upload-repo-secrets"},
		{"o/r", "--upload-env-secrets"},
		{"o/r", "--state-dir"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("expected missing-value error for %v", args)
		}
	}
}

func TestParseArgsEmptyOwnerName(t *testing.T) {
	if _, err := parseArgs([]string{"/repo"}); err == nil {
		t.Errorf("expected error for empty owner")
	}
	if _, err := parseArgs([]string{"owner/"}); err == nil {
		t.Errorf("expected error for empty repo")
	}
}

func TestDefaultStateDir(t *testing.T) {
	t.Setenv("HOME", "/home/test")

	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", `C:\Users\test\AppData\Local`)
		got, err := defaultStateDir()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(`C:\Users\test\AppData\Local`, "gh-repo-bootstrap")
		if got != want {
			t.Errorf("got %s want %s", got, want)
		}
		t.Setenv("LOCALAPPDATA", "")
		got, err = defaultStateDir()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(got, filepath.Join("AppData", "Local", "gh-repo-bootstrap")) {
			t.Errorf("fallback path wrong: %s", got)
		}
		return
	}

	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	got, err := defaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/xdg/state/gh-repo-bootstrap" {
		t.Errorf("XDG path wrong: %s", got)
	}

	t.Setenv("XDG_STATE_HOME", "")
	got, err = defaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/test/.local/state/gh-repo-bootstrap" {
		t.Errorf("home fallback wrong: %s", got)
	}
}

func TestExtractModuleErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics differ on Windows")
	}
	// Target a path under a regular file: MkdirAll will fail.
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "block")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "child")
	if err := extractModule(target); err == nil {
		t.Errorf("expected mkdir error under regular file")
	}
}

func TestMakeSecretsTmpDir(t *testing.T) {
	dir, err := makeSecretsTmpDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsDir() {
		t.Errorf("expected directory: %s", dir)
	}
	// The dir must be writable.
	probe := filepath.Join(dir, "probe")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		t.Errorf("tmpdir not writable: %v", err)
	}
}

func TestEnsureGitHubTokenFromEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "from-env")
	tok, err := ensureGitHubToken()
	if err != nil || tok != "from-env" {
		t.Errorf("env token: %q %v", tok, err)
	}
}

func TestEnsureGitHubTokenFromFakeGh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell required for fake gh")
	}
	bin := t.TempDir()
	// Print token *with trailing whitespace* to verify TrimSpace.
	gh := "#!/bin/sh\nprintf 'tok-from-gh   \\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("GITHUB_TOKEN", "")
	tok, err := ensureGitHubToken()
	if err != nil || tok != "tok-from-gh" {
		t.Errorf("fake gh token: %q %v", tok, err)
	}
}

func TestEnsureGitHubTokenEmptyFromGh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell required for fake gh")
	}
	bin := t.TempDir()
	gh := "#!/bin/sh\necho ''\n"
	os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o755)
	t.Setenv("PATH", bin)
	t.Setenv("GITHUB_TOKEN", "")
	if _, err := ensureGitHubToken(); err == nil || !strings.Contains(err.Error(), "empty token") {
		t.Errorf("expected empty-token error, got %v", err)
	}
}

func TestEnsureGitHubTokenGhMissing(t *testing.T) {
	bin := t.TempDir() // no gh inside
	t.Setenv("PATH", bin)
	t.Setenv("GITHUB_TOKEN", "")
	if _, err := ensureGitHubToken(); err == nil {
		t.Errorf("expected error when gh is absent")
	}
}

// fakeBin builds a tempdir containing fake gh and tofu shell scripts and
// returns the directory. The fake tofu logs each invocation's args to
// $tmpdir/tofu.log and exits with status from $tmpdir/tofu.exit (default 0).
func fakeBin(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("posix shell required for fake binaries")
	}
	bin := t.TempDir()
	gh := "#!/bin/sh\necho fake-token-from-gh\n"
	tofu := `#!/bin/sh
echo "$@" >> "$BIN/tofu.log"
exit "$(cat "$BIN/tofu.exit" 2>/dev/null || echo 0)"
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "tofu"), []byte(tofu), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BIN", bin)
	// Tests that use this helper need /bin and /usr/bin on PATH so the fake
	// tofu shell script can resolve `cat`. Tests that specifically want
	// missing tools should set their own PATH.
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	return bin
}

func TestRunTofuSuccessAndFailure(t *testing.T) {
	bin := fakeBin(t)

	wd := t.TempDir()
	if err := runTofu(t.Context(), wd, "tok", "init", "-input=false"); err != nil {
		t.Errorf("expected success: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(bin, "tofu.log"))
	if !strings.Contains(string(got), "init -input=false") {
		t.Errorf("tofu not invoked correctly: %s", got)
	}

	// Force a non-zero exit and verify error propagation.
	os.WriteFile(filepath.Join(bin, "tofu.exit"), []byte("3\n"), 0o600)
	if err := runTofu(t.Context(), wd, "tok", "plan"); err == nil {
		t.Errorf("expected error from non-zero exit")
	}
}

func TestRunTofuStartFailure(t *testing.T) {
	// Empty PATH so exec.LookPath/Start fails inside runTofu.
	t.Setenv("PATH", t.TempDir())
	if err := runTofu(t.Context(), t.TempDir(), "tok"); err == nil {
		t.Errorf("expected start error with no tofu on PATH")
	}
}

func TestSignalContext(t *testing.T) {
	ctx, cancel := signalContext()
	defer cancel()
	select {
	case <-ctx.Done():
		t.Errorf("ctx should not be done immediately")
	default:
	}
	cancel()
	<-ctx.Done() // should return promptly
}

func TestRunEndToEndPlan(t *testing.T) {
	bin := fakeBin(t)
	state := t.TempDir()

	// Create a repo-secrets file so generateSecrets actually runs and the
	// resulting -var-file gets passed to fake tofu.
	repoSecrets := filepath.Join(t.TempDir(), "repo.tfvars")
	os.WriteFile(repoSecrets, []byte("API = \"abc\"\n"), 0o600)

	t.Setenv("GITHUB_TOKEN", "") // force gh fallback

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"gh-repo-bootstrap",
		"owner/repo",
		"--plan",
		"--state-dir", state,
		"--upload-repo-secrets", repoSecrets,
	}

	if err := run(); err != nil {
		t.Fatalf("run() failed: %v", err)
	}

	// Expect main.tf and secrets.tf in state dir.
	for _, want := range []string{"main.tf", "secrets.tf", filepath.Join(".module", "main.tf")} {
		if _, err := os.Stat(filepath.Join(state, want)); err != nil {
			t.Errorf("missing %s: %v", want, err)
		}
	}
	// Verify tofu was called for init then plan, with -var-file on plan.
	log, _ := os.ReadFile(filepath.Join(bin, "tofu.log"))
	ls := string(log)
	if !strings.Contains(ls, "init -input=false -upgrade") {
		t.Errorf("init not logged: %s", ls)
	}
	if !strings.Contains(ls, "plan -input=false -var-file=") {
		t.Errorf("plan with var-file not logged: %s", ls)
	}
}

func TestRunEndToEndApplyAndDestroy(t *testing.T) {
	bin := fakeBin(t)
	t.Setenv("GITHUB_TOKEN", "preset")

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	for _, action := range []string{"--destroy"} { // apply is the default
		state := t.TempDir()
		os.Args = []string{"gh-repo-bootstrap", "o/r", action, "--state-dir", state}
		if err := run(); err != nil {
			t.Fatalf("%s: run failed: %v", action, err)
		}
	}

	// Default apply path.
	state := t.TempDir()
	os.Args = []string{"gh-repo-bootstrap", "o/r", "--state-dir", state}
	if err := run(); err != nil {
		t.Fatalf("apply: run failed: %v", err)
	}

	log, _ := os.ReadFile(filepath.Join(bin, "tofu.log"))
	ls := string(log)
	for _, want := range []string{
		"destroy -input=false -auto-approve",
		"apply -input=false -auto-approve",
	} {
		if !strings.Contains(ls, want) {
			t.Errorf("missing %q in log:\n%s", want, ls)
		}
	}
}

func TestRunBadArgs(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"gh-repo-bootstrap", "no-slash"}
	// Capture stderr so test output stays clean.
	captureStderr(t, func() {
		if err := run(); err == nil {
			t.Errorf("expected error for bad args")
		}
	})
}

func TestRunHelp(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"gh-repo-bootstrap", "--help"}
	err := run()
	if !errors.Is(err, errHelp) {
		t.Errorf("expected errHelp, got %v", err)
	}
}

func TestRunMissingTools(t *testing.T) {
	// PATH with neither gh nor tofu.
	t.Setenv("PATH", t.TempDir())
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"gh-repo-bootstrap", "o/r"}
	captureStderr(t, func() {
		if err := run(); err == nil {
			t.Errorf("expected error when tofu/gh missing")
		}
	})
}

func TestRunDefaultStateDir(t *testing.T) {
	// Without --state-dir, run() resolves XDG_STATE_HOME and creates the dir.
	bin := fakeBin(t)
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	t.Setenv("GITHUB_TOKEN", "preset")

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"gh-repo-bootstrap", "o/r", "--plan"}
	if err := run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := filepath.Join(xdg, "gh-repo-bootstrap", "o__r", "main.tf")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected default state dir to be used: %v", err)
	}
	_ = bin
}

func TestRunStateDirCreateFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics differ on Windows")
	}
	_ = fakeBin(t) // PATH gets gh+tofu
	t.Setenv("GITHUB_TOKEN", "preset")

	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "block")
	os.WriteFile(blocker, []byte("x"), 0o600)
	bad := filepath.Join(blocker, "child")

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"gh-repo-bootstrap", "o/r", "--plan", "--state-dir", bad}
	if err := run(); err == nil {
		t.Errorf("expected mkdir error")
	}
}

func TestGenerateSecretsErrorPaths(t *testing.T) {
	state := t.TempDir()
	tmpd := t.TempDir()

	// Env-secrets dir does not exist.
	opts := &options{
		envSecretsDir: filepath.Join(state, "nope"),
		envs:          []string{"production"},
	}
	if _, err := generateSecrets(state, tmpd, opts); err == nil {
		t.Errorf("expected missing-dir error")
	}

	// Env-secrets dir is empty (no .tfvars files).
	emptyDir := filepath.Join(state, "empty")
	os.MkdirAll(emptyDir, 0o700)
	opts.envSecretsDir = emptyDir
	if _, err := generateSecrets(state, tmpd, opts); err == nil ||
		!strings.Contains(err.Error(), "no *.tfvars") {
		t.Errorf("expected empty-dir error, got %v", err)
	}

	// Env-secrets file has an invalid env-name in the basename.
	weirdDir := filepath.Join(state, "weird")
	os.MkdirAll(weirdDir, 0o700)
	os.WriteFile(filepath.Join(weirdDir, "1bad.tfvars"), []byte("X = \"y\"\n"), 0o600)
	opts.envSecretsDir = weirdDir
	if _, err := generateSecrets(state, tmpd, opts); err == nil ||
		!strings.Contains(err.Error(), "invalid env name") {
		t.Errorf("expected invalid-env-name error, got %v", err)
	}

	// Env-secrets parse error propagates.
	parseDir := filepath.Join(state, "parse")
	os.MkdirAll(parseDir, 0o700)
	os.WriteFile(filepath.Join(parseDir, "production.tfvars"),
		[]byte("not a valid line\n"), 0o600)
	opts.envSecretsDir = parseDir
	if _, err := generateSecrets(state, tmpd, opts); err == nil {
		t.Errorf("expected parse-error propagation")
	}

	// Repo-secrets file does not exist.
	opts2 := &options{repoSecretsFile: filepath.Join(state, "nope.tfvars")}
	if _, err := generateSecrets(state, tmpd, opts2); err == nil {
		t.Errorf("expected missing repo-secrets error")
	}
}

func TestParseSecretsFileUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based unreadable check not portable / root bypasses perms")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "locked.tfvars")
	os.WriteFile(p, []byte("X = \"y\"\n"), 0o600)
	if err := os.Chmod(p, 0o000); err != nil {
		t.Skip("cannot chmod 000")
	}
	defer os.Chmod(p, 0o600)
	if _, err := parseSecretsFile(p); err == nil {
		t.Errorf("expected unreadable error")
	}
}

func TestParseBypassSpecTooManyParts(t *testing.T) {
	if _, err := parseBypassSpec("Team:1:always:extra"); err == nil {
		t.Errorf("expected error for 4-part spec")
	}
	if _, err := parseBypassSpec(":1:always"); err == nil {
		t.Errorf("expected error for empty actor_type")
	}
}

// TestMainEntryPoint exercises main() in a subprocess so we can observe its
// exit code / stderr handling without disturbing the test runner.
func TestMainEntryPoint(t *testing.T) {
	if os.Getenv("GO_TEST_RUN_MAIN") == "1" {
		// Strip the testing flags; user-supplied args follow "--".
		for i, a := range os.Args {
			if a == "--" {
				os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
				break
			}
		}
		main()
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("subprocess args plumbing differs on Windows; main is a 7-line wrapper")
	}

	cases := []struct {
		name     string
		args     []string
		wantExit int
		wantErr  string
	}{
		{"help", []string{"--help"}, 0, "gh repo-bootstrap"},
		{"badArgs", []string{"no-slash"}, 1, "first argument must be"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestMainEntryPoint", "--")
			cmd.Args = append(cmd.Args, c.args...)
			cmd.Env = append(os.Environ(), "GO_TEST_RUN_MAIN=1")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			cmd.Stdout = &stderr
			err := cmd.Run()
			rc := 0
			if ee, ok := err.(*exec.ExitError); ok {
				rc = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("subprocess: %v", err)
			}
			if rc != c.wantExit {
				t.Errorf("exit=%d want %d (stderr=%s)", rc, c.wantExit, stderr.String())
			}
			if !strings.Contains(stderr.String(), c.wantErr) {
				t.Errorf("stderr missing %q", c.wantErr)
			}
		})
	}
}
