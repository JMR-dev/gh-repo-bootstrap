// Package runner wires CLI options to the Pulumi Automation API: it
// configures a local-filesystem-backed workspace, manages the per-state-dir
// secrets passphrase, resolves GITHUB_TOKEN, runs interactive prompts and
// reviewer resolution, and dispatches up/preview/destroy.
package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JMR-dev/gh-repo-bootstrap/internal/cli"
	"github.com/JMR-dev/gh-repo-bootstrap/internal/githubapi"
	"github.com/JMR-dev/gh-repo-bootstrap/internal/prompt"
	"github.com/JMR-dev/gh-repo-bootstrap/internal/pulumiprog"
	"github.com/JMR-dev/gh-repo-bootstrap/internal/secrets"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/workspace"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	projectName = "gh-repo-bootstrap"
	stackName   = "bootstrap"
)

var execCommand = exec.Command

type stackInterface interface {
	SetConfig(ctx context.Context, key string, val auto.ConfigValue) error
	Up(ctx context.Context, opts ...optup.Option) (auto.UpResult, error)
	Preview(ctx context.Context, opts ...optpreview.Option) (auto.PreviewResult, error)
	Destroy(ctx context.Context, opts ...optdestroy.Option) (auto.DestroyResult, error)
}

var upsertStack = func(ctx context.Context, stackName, projectName string, program pulumi.RunFunc, opts ...auto.LocalWorkspaceOption) (stackInterface, error) {
	s, err := auto.UpsertStackInlineSource(ctx, stackName, projectName, program, opts...)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Run executes the requested action against the GitHub repo described by opts.
func Run(ctx context.Context, opts *cli.Options) error {
	// --- TOML config takes over the Options struct if --config was set ---
	if opts.ConfigFile != "" {
		loaded, err := cli.LoadConfig(opts.ConfigFile)
		if err != nil {
			return err
		}
		opts = loaded
	}

	// --- --create interactive prompts (CLI path only) -------------------
	if opts.RepoMode == cli.RepoModeCreate && opts.ConfigFile == "" {
		if err := runCreatePrompts(opts); err != nil {
			return err
		}
	}
	// Final create-mode sanity check (works for both CLI and TOML paths).
	if opts.RepoMode == cli.RepoModeCreate {
		if opts.RepoSettings.Visibility == "" {
			return errors.New("--create requires --visibility (or set [repo].visibility in config)")
		}
	}

	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = cli.DefaultStateDir(opts.Owner, opts.Repo)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	absStateDir, err := filepath.Abs(stateDir)
	if err != nil {
		return fmt.Errorf("resolving state dir: %w", err)
	}
	stateDir = absStateDir

	// --- Auth: prefer caller-supplied GITHUB_TOKEN, else borrow from gh ---
	if os.Getenv("GITHUB_TOKEN") == "" {
		out, err := execCommand("gh", "auth", "token").Output()
		if err != nil {
			return fmt.Errorf("no GITHUB_TOKEN set and `gh auth token` failed; run `gh auth login` first")
		}
		_ = os.Setenv("GITHUB_TOKEN", strings.TrimSpace(string(out)))
	}

	// --- Passphrase for the local backend's secret encryption ------------
	if err := ensurePassphrase(stateDir); err != nil {
		return err
	}

	// --- Parse secret files ----------------------------------------------
	var repoSecrets []secrets.Pair
	if opts.RepoSecretsFile != "" {
		ps, err := secrets.ParseFile(opts.RepoSecretsFile)
		if err != nil {
			return err
		}
		repoSecrets = ps
	}
	var envSecrets []secrets.EnvFile
	if opts.EnvSecretsDir != "" {
		envSet := make(map[string]struct{}, len(opts.Environments))
		for _, e := range opts.Environments {
			envSet[e.Name] = struct{}{}
		}
		es, err := secrets.LoadEnvDir(opts.EnvSecretsDir, envSet)
		if err != nil {
			return err
		}
		envSecrets = es
	}

	// --- Resolve reviewer identifiers ------------------------------------
	resolver := githubapi.New()
	resolvedEnvs := make([]pulumiprog.ResolvedEnv, 0, len(opts.Environments))
	for _, e := range opts.Environments {
		re := pulumiprog.ResolvedEnv{
			Name:              e.Name,
			WaitTimer:         e.WaitTimer,
			PreventSelfReview: e.PreventSelfReview,
			CanAdminsBypass:   e.CanAdminsBypass,
			BranchPolicy:      e.BranchPolicy,
			BranchPatterns:    e.BranchPatterns,
		}
		for _, u := range e.ReviewerUsers {
			id, err := resolver.ResolveUser(u)
			if err != nil {
				return err
			}
			re.ReviewerUserIDs = append(re.ReviewerUserIDs, id)
		}
		for _, t := range e.ReviewerTeams {
			id, err := resolver.ResolveTeam(t)
			if err != nil {
				return err
			}
			re.ReviewerTeamIDs = append(re.ReviewerTeamIDs, id)
		}
		resolvedEnvs = append(resolvedEnvs, re)
	}

	// --- Pulumi workspace pointed at file://<stateDir> -------------------
	backendURL := "file://" + stateDir
	projectSettings := workspace.Project{
		Name:    tokens.PackageName(projectName),
		Runtime: workspace.NewProjectRuntimeInfo("go", nil),
		Backend: &workspace.ProjectBackend{URL: backendURL},
	}
	program := pulumiprog.Build(pulumiprog.Inputs{
		Owner:        opts.Owner,
		Repo:         opts.Repo,
		Branch:       opts.Branch,
		Reviews:      opts.Reviews,
		Signed:       opts.Signed,
		RulesetName:  opts.Ruleset,
		Environments: resolvedEnvs,
		Bypass:       opts.Bypass,
		RepoSecrets:  repoSecrets,
		EnvSecrets:   envSecrets,
		RepoMode:     opts.RepoMode,
		RepoSettings: opts.RepoSettings,
	})

	fmt.Printf(">>> Working directory: %s\n", stateDir)
	if opts.RepoMode == cli.RepoModeManage {
		fmt.Println(">>> --manage-repo: the first apply imports the existing repo into state.")
		fmt.Println(">>> Run with --plan first to review the import + any drift reconciliation.")
	}

	stack, err := upsertStack(ctx, stackName, projectName, program,
		auto.WorkDir(stateDir),
		auto.EnvVars(map[string]string{
			"PULUMI_BACKEND_URL":       backendURL,
			"PULUMI_CONFIG_PASSPHRASE": os.Getenv("PULUMI_CONFIG_PASSPHRASE"),
			"PULUMI_SKIP_UPDATE_CHECK": "true",
		}),
		auto.Project(projectSettings),
	)
	if err != nil {
		return fmt.Errorf("creating Pulumi stack: %w", err)
	}

	if err := stack.SetConfig(ctx, "github:owner", auto.ConfigValue{Value: opts.Owner}); err != nil {
		return fmt.Errorf("setting github:owner config: %w", err)
	}

	switch opts.Action {
	case cli.ActionApply:
		_, err = stack.Up(ctx, optup.ProgressStreams(os.Stdout), optup.ErrorProgressStreams(os.Stderr))
	case cli.ActionPlan:
		_, err = stack.Preview(ctx, optpreview.ProgressStreams(os.Stdout), optpreview.ErrorProgressStreams(os.Stderr))
	case cli.ActionDestroy:
		_, err = stack.Destroy(ctx, optdestroy.ProgressStreams(os.Stdout), optdestroy.ErrorProgressStreams(os.Stderr))
	default:
		return fmt.Errorf("unknown action: %s", opts.Action)
	}
	if err != nil {
		return err
	}
	if opts.RepoMode == cli.RepoModeCreate && opts.Action == cli.ActionApply {
		if err := setGitRemoteAndPush(opts.Owner, opts.Repo); err != nil {
			return err
		}
	}
	return nil
}

// setGitRemoteAndPush wires the newly created GitHub repo as the "origin"
// remote of the current git working tree and pushes all local commits.
// If the working directory is not inside a git repo the step is skipped.
func setGitRemoteAndPush(owner, repo string) error {
	if err := execCommand("git", "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		fmt.Println(">>> Not inside a git repository; skipping remote setup.")
		return nil
	}

	remoteURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	fmt.Printf(">>> Setting git remote origin → %s\n", remoteURL)

	if execCommand("git", "remote", "get-url", "origin").Run() == nil {
		if out, err := execCommand("git", "remote", "set-url", "origin", remoteURL).CombinedOutput(); err != nil {
			return fmt.Errorf("updating git remote: %s: %w", strings.TrimSpace(string(out)), err)
		}
	} else {
		if out, err := execCommand("git", "remote", "add", "origin", remoteURL).CombinedOutput(); err != nil {
			return fmt.Errorf("adding git remote: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}

	fmt.Println(">>> Pushing local commits to origin...")
	if out, err := execCommand("git", "push", "-u", "origin", "HEAD").CombinedOutput(); err != nil {
		return fmt.Errorf("pushing to remote: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// runCreatePrompts asks the user for any missing required --create values
// (visibility, description) when those weren't supplied on the command line.
func runCreatePrompts(opts *cli.Options) error {
	needsVisibility := opts.RepoSettings.Visibility == ""
	needsDescription := opts.RepoSettings.Description == nil
	if !needsVisibility && !needsDescription {
		return nil
	}
	if !prompt.IsInteractive() {
		return prompt.ErrNotInteractive
	}
	p := prompt.New()
	fmt.Fprintf(os.Stderr, ">>> Creating %s/%s\n", opts.Owner, opts.Repo)
	if needsVisibility {
		v, err := p.Choice("Visibility? [public/private] (default: private): ",
			"private", []string{"public", "private"})
		if err != nil {
			return err
		}
		opts.RepoSettings.Visibility = v
	}
	if needsDescription {
		d, err := p.Line("Description (optional): ")
		if err != nil {
			return err
		}
		opts.RepoSettings.Description = &d
	}
	return nil
}

// ensurePassphrase makes sure PULUMI_CONFIG_PASSPHRASE is set for this process,
// auto-generating and persisting one at <stateDir>/.passphrase if needed.
func ensurePassphrase(stateDir string) error {
	if os.Getenv("PULUMI_CONFIG_PASSPHRASE") != "" || os.Getenv("PULUMI_CONFIG_PASSPHRASE_FILE") != "" {
		return nil
	}
	p := filepath.Join(stateDir, ".passphrase")
	b, err := os.ReadFile(p)
	if err == nil {
		_ = os.Setenv("PULUMI_CONFIG_PASSPHRASE", strings.TrimRight(string(b), "\r\n"))
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("reading passphrase file: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("generating passphrase: %w", err)
	}
	pass := hex.EncodeToString(raw)
	if err := os.WriteFile(p, []byte(pass+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing passphrase file: %w", err)
	}
	_ = os.Setenv("PULUMI_CONFIG_PASSPHRASE", pass)
	return nil
}
