package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JMR-dev/gh-repo-bootstrap/internal/cli"
	"github.com/JMR-dev/gh-repo-bootstrap/internal/githubapi"
	"github.com/JMR-dev/gh-repo-bootstrap/internal/prompt"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Helper process for mocking gh CLI in runner tests.
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
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "invalid args: %v\n", args)
		os.Exit(2)
	}

	command := args[0]
	switch command {
	case "gh":
		subCmd := args[1]
		switch subCmd {
		case "auth":
			if len(args) >= 3 && args[2] == "token" {
				fmt.Print("gh_mock_token\n")
			} else {
				fmt.Fprintf(os.Stderr, "unknown auth subcommand\n")
				os.Exit(2)
			}
		case "api":
			if len(args) >= 3 {
				path := args[2]
				switch {
				case path == "users/octocat":
					fmt.Print(`{"id":583234}`)
				case path == "orgs/JMR-dev/teams/release":
					fmt.Print(`{"id":98765}`)
				default:
					fmt.Fprintf(os.Stderr, "unknown path: %s\n", path)
					os.Exit(2)
				}
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown gh command: %s\n", subCmd)
			os.Exit(2)
		}
	case "git":
		if os.Getenv("MOCK_GIT_ENABLED") != "1" {
			fmt.Fprintf(os.Stderr, "git not mocked in this test\n")
			os.Exit(2)
		}
		subCmd := args[1]
		switch subCmd {
		case "rev-parse":
			// success — we're in a mock git repo
		case "remote":
			if len(args) >= 3 && args[2] == "get-url" {
				// Simulate "no origin" by default; set MOCK_GIT_ORIGIN_EXISTS=1 to override.
				if os.Getenv("MOCK_GIT_ORIGIN_EXISTS") != "1" {
					os.Exit(1)
				}
			}
			// add / set-url: exit 0 (success)
		case "push":
			// success
		default:
			fmt.Fprintf(os.Stderr, "unknown git subcommand: %s\n", subCmd)
			os.Exit(2)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", command)
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

// mockExecWithGit is like mockExec but also enables the git mock.
func mockExecWithGit(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "MOCK_GIT_ENABLED=1")
	return cmd
}

// makeRecordingExec wraps a delegate exec function and records every call.
func makeRecordingExec(delegate func(string, ...string) *exec.Cmd) (func(string, ...string) *exec.Cmd, *[][]string) {
	calls := &[][]string{}
	return func(name string, args ...string) *exec.Cmd {
		*calls = append(*calls, append([]string{name}, args...))
		return delegate(name, args...)
	}, calls
}

// mockStack implements stackInterface.
type mockStack struct {
	setConfigCalls map[string]string
	actionCalled   string
	failAction     bool
}

func (m *mockStack) SetConfig(ctx context.Context, key string, val auto.ConfigValue) error {
	m.setConfigCalls[key] = val.Value
	return nil
}

func (m *mockStack) Up(ctx context.Context, opts ...optup.Option) (auto.UpResult, error) {
	m.actionCalled = "up"
	if m.failAction {
		return auto.UpResult{}, errors.New("up error")
	}
	return auto.UpResult{}, nil
}

func (m *mockStack) Preview(ctx context.Context, opts ...optpreview.Option) (auto.PreviewResult, error) {
	m.actionCalled = "preview"
	if m.failAction {
		return auto.PreviewResult{}, errors.New("preview error")
	}
	return auto.PreviewResult{}, nil
}

func (m *mockStack) Destroy(ctx context.Context, opts ...optdestroy.Option) (auto.DestroyResult, error) {
	m.actionCalled = "destroy"
	if m.failAction {
		return auto.DestroyResult{}, errors.New("destroy error")
	}
	return auto.DestroyResult{}, nil
}

// TestRun_RelativeStateDir verifies that a relative state_dir (e.g. "./state"
// from a TOML config) does not cause a "state/state" double-path in the
// file:// backend URL. Before the fix, upsertStack was called with a relative
// file:// URL that Pulumi resolved against its WorkDir, producing the wrong path.
func TestRun_RelativeStateDir(t *testing.T) {
	oldExec := execCommand
	oldGithubExec := githubapi.ExecCommand
	oldUpsert := upsertStack
	execCommand = mockExec
	githubapi.ExecCommand = mockExec
	defer func() {
		execCommand = oldExec
		githubapi.ExecCommand = oldGithubExec
		upsertStack = oldUpsert
	}()

	// Capture the PULUMI_BACKEND_URL env var passed to upsertStack.
	var capturedBackendURL string
	mStack := &mockStack{setConfigCalls: make(map[string]string)}
	upsertStack = func(ctx context.Context, stackName, projectName string, program pulumi.RunFunc, opts ...auto.LocalWorkspaceOption) (stackInterface, error) {
		// Apply options to a scratch workspace to extract env vars.
		ws, err := auto.NewLocalWorkspace(ctx, opts...)
		if err == nil {
			env := ws.GetEnvVars()
			capturedBackendURL = env["PULUMI_BACKEND_URL"]
		}
		return mStack, nil
	}

	parent := t.TempDir()
	oldWd, _ := os.Getwd()
	_ = os.Chdir(parent)
	defer os.Chdir(oldWd)

	opts := &cli.Options{
		Owner:        "JMR-dev",
		Repo:         "test-repo",
		Branch:       "main",
		Action:       cli.ActionApply,
		StateDir:     "./state",
		Environments: []*cli.EnvSpec{{Name: "production"}},
	}

	oldToken := os.Getenv("GITHUB_TOKEN")
	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Setenv("GITHUB_TOKEN", oldToken)

	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "file://" + filepath.Join(parent, "state")
	if capturedBackendURL != want {
		t.Errorf("PULUMI_BACKEND_URL = %q, want %q", capturedBackendURL, want)
	}
}

func TestRun_Apply(t *testing.T) {
	oldExec := execCommand
	oldGithubExec := githubapi.ExecCommand
	oldUpsert := upsertStack
	execCommand = mockExec
	githubapi.ExecCommand = mockExec
	defer func() {
		execCommand = oldExec
		githubapi.ExecCommand = oldGithubExec
		upsertStack = oldUpsert
	}()

	// Mock upsertStack
	mStack := &mockStack{setConfigCalls: make(map[string]string)}
	upsertStack = func(ctx context.Context, stackName, projectName string, program pulumi.RunFunc, opts ...auto.LocalWorkspaceOption) (stackInterface, error) {
		return mStack, nil
	}

	stateDir := t.TempDir()

	opts := &cli.Options{
		Owner:    "JMR-dev",
		Repo:     "test-repo",
		Branch:   "main",
		Action:   cli.ActionApply,
		StateDir: stateDir,
		Environments: []*cli.EnvSpec{
			{
				Name:          "production",
				ReviewerUsers: []string{"octocat"},
				ReviewerTeams: []string{"JMR-dev/release"},
			},
		},
	}

	// Make sure GITHUB_TOKEN is cleared to exercise fallback.
	oldToken := os.Getenv("GITHUB_TOKEN")
	os.Unsetenv("GITHUB_TOKEN")
	defer os.Setenv("GITHUB_TOKEN", oldToken)

	err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mStack.actionCalled != "up" {
		t.Errorf("expected stack action 'up', got: %s", mStack.actionCalled)
	}

	if val, ok := mStack.setConfigCalls["github:owner"]; !ok || val != "JMR-dev" {
		t.Errorf("expected github:owner config to be JMR-dev, got %s", val)
	}

	// Verify GITHUB_TOKEN is set after Run via fallback command.
	if os.Getenv("GITHUB_TOKEN") != "gh_mock_token" {
		t.Errorf("expected GITHUB_TOKEN to be set to 'gh_mock_token', got: %s", os.Getenv("GITHUB_TOKEN"))
	}
}

func TestRun_PlanAndDestroy(t *testing.T) {
	oldExec := execCommand
	oldUpsert := upsertStack
	execCommand = mockExec
	defer func() {
		execCommand = oldExec
		upsertStack = oldUpsert
	}()

	mStack := &mockStack{setConfigCalls: make(map[string]string)}
	upsertStack = func(ctx context.Context, stackName, projectName string, program pulumi.RunFunc, opts ...auto.LocalWorkspaceOption) (stackInterface, error) {
		return mStack, nil
	}

	stateDir := t.TempDir()

	// Test Plan
	optsPlan := &cli.Options{
		Owner:    "JMR-dev",
		Repo:     "test-repo",
		Branch:   "main",
		Action:   cli.ActionPlan,
		StateDir: stateDir,
	}
	err := Run(context.Background(), optsPlan)
	if err != nil {
		t.Fatalf("unexpected plan error: %v", err)
	}
	if mStack.actionCalled != "preview" {
		t.Errorf("expected preview, got %s", mStack.actionCalled)
	}

	// Test Destroy
	optsDestroy := &cli.Options{
		Owner:    "JMR-dev",
		Repo:     "test-repo",
		Branch:   "main",
		Action:   cli.ActionDestroy,
		StateDir: stateDir,
	}
	err = Run(context.Background(), optsDestroy)
	if err != nil {
		t.Fatalf("unexpected destroy error: %v", err)
	}
	if mStack.actionCalled != "destroy" {
		t.Errorf("expected destroy, got %s", mStack.actionCalled)
	}
}

func TestRun_SecretsAndPassphrase(t *testing.T) {
	oldExec := execCommand
	oldUpsert := upsertStack
	execCommand = mockExec
	defer func() {
		execCommand = oldExec
		upsertStack = oldUpsert
	}()

	mStack := &mockStack{setConfigCalls: make(map[string]string)}
	upsertStack = func(ctx context.Context, stackName, projectName string, program pulumi.RunFunc, opts ...auto.LocalWorkspaceOption) (stackInterface, error) {
		return mStack, nil
	}

	stateDir := t.TempDir()
	secretsDir := t.TempDir()
	
	// Create repo secrets file.
	repoSecretsFile := filepath.Join(secretsDir, "repo.tfvars")
	_ = os.WriteFile(repoSecretsFile, []byte("TOKEN = \"1234\"\n"), 0o600)

	// Create env secrets dir and file.
	envSecretsDir := filepath.Join(secretsDir, "envs")
	_ = os.MkdirAll(envSecretsDir, 0o700)
	_ = os.WriteFile(filepath.Join(envSecretsDir, "production.tfvars"), []byte("DB_PW = \"prodpwd\"\n"), 0o600)

	opts := &cli.Options{
		Owner:           "JMR-dev",
		Repo:            "test-repo",
		Branch:          "main",
		Action:          cli.ActionPlan,
		StateDir:        stateDir,
		RepoSecretsFile: repoSecretsFile,
		EnvSecretsDir:   envSecretsDir,
		Environments: []*cli.EnvSpec{
			{Name: "production"},
		},
	}

	// Clear passphrase env var
	oldPassphrase := os.Getenv("PULUMI_CONFIG_PASSPHRASE")
	os.Unsetenv("PULUMI_CONFIG_PASSPHRASE")
	defer os.Setenv("PULUMI_CONFIG_PASSPHRASE", oldPassphrase)

	err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify passphrase file is generated.
	passphraseFile := filepath.Join(stateDir, ".passphrase")
	if _, err := os.Stat(passphraseFile); os.IsNotExist(err) {
		t.Error("expected passphrase file to be created")
	}

	// Run again to verify it reuses the existing passphrase.
	err = Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error on second run: %v", err)
	}
}

func TestRun_TOMLConfigFile(t *testing.T) {
	oldExec := execCommand
	oldUpsert := upsertStack
	execCommand = mockExec
	defer func() {
		execCommand = oldExec
		upsertStack = oldUpsert
	}()

	mStack := &mockStack{setConfigCalls: make(map[string]string)}
	upsertStack = func(ctx context.Context, stackName, projectName string, program pulumi.RunFunc, opts ...auto.LocalWorkspaceOption) (stackInterface, error) {
		return mStack, nil
	}

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.toml")
	tomlData := `
owner = "JMR-dev"
name = "config-repo"
mode = "data"
`
	_ = os.WriteFile(configFile, []byte(tomlData), 0o600)

	opts := &cli.Options{
		ConfigFile: configFile,
	}

	err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error with TOML config: %v", err)
	}
}

func TestRun_CreateModeValidation(t *testing.T) {
	stateDir := t.TempDir()

	// --create requires --visibility (or config file)
	opts := &cli.Options{
		Owner:    "JMR-dev",
		Repo:     "test-repo",
		RepoMode: cli.RepoModeCreate,
		StateDir: stateDir,
	}

	err := Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "input is not a terminal") {
		t.Fatalf("expected visibility validation error (non-interactive), got: %v", err)
	}
}

func TestRun_CreateModeInteractive(t *testing.T) {
	oldExec := execCommand
	oldGithubExec := githubapi.ExecCommand
	oldUpsert := upsertStack
	oldIsTerminal := prompt.IsTerminal
	execCommand = mockExec
	githubapi.ExecCommand = mockExec
	prompt.IsTerminal = func(fd int) bool { return true }
	defer func() {
		execCommand = oldExec
		githubapi.ExecCommand = oldGithubExec
		upsertStack = oldUpsert
		prompt.IsTerminal = oldIsTerminal
	}()

	mStack := &mockStack{setConfigCalls: make(map[string]string)}
	upsertStack = func(ctx context.Context, stackName, projectName string, program pulumi.RunFunc, opts ...auto.LocalWorkspaceOption) (stackInterface, error) {
		return mStack, nil
	}

	stateDir := t.TempDir()

	opts := &cli.Options{
		Owner:    "JMR-dev",
		Repo:     "test-repo",
		RepoMode: cli.RepoModeCreate,
		StateDir: stateDir,
		Action:   cli.ActionApply,
	}

	// Create pipe to feed stdin
	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() {
		os.Stdin = oldStdin
		r.Close()
		w.Close()
	}()

	// Feed mock input: "public" for visibility, and "my repo description" for description.
	_, _ = w.Write([]byte("public\nmy repo description\n"))

	err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if opts.RepoSettings.Visibility != "public" {
		t.Errorf("expected Visibility to be public, got: %s", opts.RepoSettings.Visibility)
	}
	if opts.RepoSettings.Description == nil || *opts.RepoSettings.Description != "my repo description" {
		t.Errorf("expected Description to be 'my repo description', got: %v", opts.RepoSettings.Description)
	}
}

func TestRun_CreateMode_SetsRemoteAndPushes(t *testing.T) {
	oldExec := execCommand
	oldGithubExec := githubapi.ExecCommand
	oldUpsert := upsertStack
	defer func() {
		execCommand = oldExec
		githubapi.ExecCommand = oldGithubExec
		upsertStack = oldUpsert
	}()

	githubapi.ExecCommand = mockExec

	recorder, calls := makeRecordingExec(mockExecWithGit)
	execCommand = recorder

	mStack := &mockStack{setConfigCalls: make(map[string]string)}
	upsertStack = func(ctx context.Context, stackName, projectName string, program pulumi.RunFunc, opts ...auto.LocalWorkspaceOption) (stackInterface, error) {
		return mStack, nil
	}

	stateDir := t.TempDir()
	oldToken := os.Getenv("GITHUB_TOKEN")
	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Setenv("GITHUB_TOKEN", oldToken)

	desc := "test repo"
	opts := &cli.Options{
		Owner:    "JMR-dev",
		Repo:     "new-repo",
		Branch:   "main",
		Action:   cli.ActionApply,
		RepoMode: cli.RepoModeCreate,
		StateDir: stateDir,
		RepoSettings: cli.RepoSettings{
			Visibility:  "private",
			Description: &desc,
		},
	}

	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the expected git command sequence was issued.
	wantRemoteURL := "https://github.com/JMR-dev/new-repo.git"
	checkCmd := func(want []string) {
		t.Helper()
		for _, c := range *calls {
			if len(c) == len(want) {
				match := true
				for i := range want {
					if c[i] != want[i] {
						match = false
						break
					}
				}
				if match {
					return
				}
			}
		}
		t.Errorf("expected command %v not found in recorded calls: %v", want, *calls)
	}

	checkCmd([]string{"git", "rev-parse", "--is-inside-work-tree"})
	checkCmd([]string{"git", "remote", "get-url", "origin"})
	checkCmd([]string{"git", "remote", "add", "origin", wantRemoteURL})
	checkCmd([]string{"git", "push", "-u", "origin", "HEAD"})
}

func TestRun_NonCreateMode_NoRemoteSetup(t *testing.T) {
	oldExec := execCommand
	oldUpsert := upsertStack
	defer func() {
		execCommand = oldExec
		upsertStack = oldUpsert
	}()

	recorder, calls := makeRecordingExec(mockExec)
	execCommand = recorder

	mStack := &mockStack{setConfigCalls: make(map[string]string)}
	upsertStack = func(ctx context.Context, stackName, projectName string, program pulumi.RunFunc, opts ...auto.LocalWorkspaceOption) (stackInterface, error) {
		return mStack, nil
	}

	stateDir := t.TempDir()
	oldToken := os.Getenv("GITHUB_TOKEN")
	os.Setenv("GITHUB_TOKEN", "test-token")
	defer os.Setenv("GITHUB_TOKEN", oldToken)

	opts := &cli.Options{
		Owner:    "JMR-dev",
		Repo:     "existing-repo",
		Branch:   "main",
		Action:   cli.ActionApply,
		RepoMode: cli.RepoModeData,
		StateDir: stateDir,
	}

	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range *calls {
		if len(c) > 0 && c[0] == "git" {
			t.Errorf("expected no git commands for non-create mode, got: %v", c)
		}
	}
}
