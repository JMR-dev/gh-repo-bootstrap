// Package cli parses command-line arguments for gh-repo-bootstrap.
package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Action selects which Pulumi operation to perform.
type Action string

const (
	ActionApply   Action = "apply"
	ActionPlan    Action = "plan"
	ActionDestroy Action = "destroy"
)

// BypassActor is a parsed --bypass spec.
type BypassActor struct {
	ActorID    int
	ActorType  string
	BypassMode string
}

// Options holds the parsed CLI state.
type Options struct {
	Owner   string
	Repo    string
	Branch  string
	Reviews int
	Signed  bool
	Ruleset string

	// Environments preserves insertion order. Lookups go through findEnv.
	Environments []*EnvSpec

	Bypass          []BypassActor
	Action          Action
	StateDir        string
	RepoSecretsFile string
	EnvSecretsDir   string

	// Repo-level management.
	RepoMode     RepoMode     // "" defaults to RepoModeData
	RepoSettings RepoSettings

	// ConfigFile, if set, means all other flags came from a TOML file.
	ConfigFile string
}

const usage = `Usage:
  gh repo-bootstrap <owner/repo> [options]
  gh repo-bootstrap --config FILE

Apply a standard branch-protection ruleset, deployment environments,
optional environment protection rules, and optionally repo-level
settings to a GitHub repository via Pulumi.

Options:
  --config FILE        Load every setting from a TOML file. Exclusive
                       with every other flag (including <owner/repo>).
  --branch NAME        Default branch to protect (default: main)
  --reviews N          Required PR approving reviews (default: 1)
  --signed             Require signed commits on the protected branch
  --env NAME           Add a deployment environment (repeatable)
                       Default if none given: production
  --ruleset NAME       Ruleset name (default: default-branch-protection)
  --bypass SPEC        Add a bypass actor (repeatable). SPEC is
                       <actor_type>:<actor_id>[:<mode>]
  --solo               Shortcut for --bypass RepositoryRole:5:always

Repo-level (use with --create or --manage-repo):
  --create             Create the repo as a Pulumi resource. Runs minimal
                       interactive prompts for visibility + description
                       when those flags are not supplied.
  --manage-repo        Import an existing repo into Pulumi state and
                       manage its settings from now on. Run --plan first.
  --visibility V       Repo visibility: public | private
  --description TEXT   Repo description
  --topic NAME         Add a repository topic (repeatable)
  --default-repo-branch NAME   Default branch on the repo itself
                       (distinct from --branch which is the ruleset
                       target; on --create they default to the same).
  --allow-merge-commit / --no-allow-merge-commit
  --allow-squash-merge / --no-allow-squash-merge
  --allow-rebase-merge / --no-allow-rebase-merge
  --delete-branch-on-merge / --no-delete-branch-on-merge
  --auto-init / --no-auto-init   (--create only)
  --license-template SLUG        (--create only)
  --gitignore-template NAME      (--create only)

Environment protection:
  --env-reviewer ENV:ACTOR (repeatable)
                       ACTOR is user:<id-or-login> or team:<id-or-slug>
                       where slug is org/team-slug
  --env-wait-timer ENV:MINUTES
  --env-prevent-self-review ENV
  --env-no-admin-bypass ENV
  --env-branch-policy ENV:MODE   MODE = protected | custom | none
  --env-branch-pattern ENV:PATTERN (repeatable)

Secrets:
  --upload-repo-secrets FILE
  --upload-env-secrets DIR

Run control:
  --plan               pulumi preview
  --destroy            pulumi destroy
  --state-dir DIR      Override working/state directory
                       (default: $XDG_STATE_HOME/gh-repo-bootstrap or
                                 ~/.local/state/gh-repo-bootstrap)
  -h, --help           Show this help

Authentication:
  GITHUB_TOKEN is auto-populated from ` + "`gh auth token`" + ` if not already set.
`

// PrintUsage writes the usage text to stdout.
func PrintUsage() { fmt.Print(usage) }

// findEnv returns the EnvSpec with name n, creating it if necessary so order
// of first appearance is preserved.
func (o *Options) findEnv(n string) *EnvSpec {
	for _, e := range o.Environments {
		if e.Name == n {
			return e
		}
	}
	e := &EnvSpec{Name: n}
	o.Environments = append(o.Environments, e)
	return e
}

// Parse parses argv (excluding program name) into Options.
// Returns (nil, nil) when the user requested --help.
func Parse(argv []string) (*Options, error) {
	// --- --config exclusivity check ---------------------------------------
	if configFile, ok := findConfigFlag(argv); ok {
		if len(argv) != 2 {
			return nil, errors.New("No other flags allowed with config file.")
		}
		return &Options{ConfigFile: configFile}, nil
	}

	o := &Options{
		Branch:  "main",
		Reviews: 1,
		Ruleset: "default-branch-protection",
		Action:  ActionApply,
	}

	need := func(i int, flag string) (string, error) {
		if i+1 >= len(argv) {
			return "", fmt.Errorf("%s requires a value", flag)
		}
		return argv[i+1], nil
	}

	bptr := func(b bool) *bool { return &b }

	i := 0
	for i < len(argv) {
		a := argv[i]
		switch a {
		case "-h", "--help":
			PrintUsage()
			return nil, nil
		case "--branch":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			o.Branch = v
			i += 2
		case "--reviews":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("--reviews must be a non-negative integer (got: %s)", v)
			}
			o.Reviews = n
			i += 2
		case "--signed":
			o.Signed = true
			i++
		case "--ruleset":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			o.Ruleset = v
			i += 2
		case "--env":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			_ = o.findEnv(v)
			i += 2
		case "--bypass":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			b, err := parseBypass(v)
			if err != nil {
				return nil, err
			}
			o.Bypass = append(o.Bypass, b)
			i += 2
		case "--solo":
			o.Bypass = append(o.Bypass, BypassActor{ActorID: 5, ActorType: "RepositoryRole", BypassMode: "always"})
			i++
		case "--upload-repo-secrets":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			o.RepoSecretsFile = v
			i += 2
		case "--upload-env-secrets":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			o.EnvSecretsDir = v
			i += 2
		case "--plan":
			o.Action = ActionPlan
			i++
		case "--destroy":
			o.Action = ActionDestroy
			i++
		case "--state-dir":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			o.StateDir = v
			i += 2

		// --- Repo-level management ---------------------------------------
		case "--create":
			if o.RepoMode == RepoModeManage {
				return nil, errors.New("--create and --manage-repo are mutually exclusive")
			}
			o.RepoMode = RepoModeCreate
			i++
		case "--manage-repo":
			if o.RepoMode == RepoModeCreate {
				return nil, errors.New("--create and --manage-repo are mutually exclusive")
			}
			o.RepoMode = RepoModeManage
			i++
		case "--visibility":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			if v != "public" && v != "private" {
				return nil, fmt.Errorf("--visibility must be public or private (got: %s)", v)
			}
			o.RepoSettings.Visibility = v
			i += 2
		case "--description":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			s := v
			o.RepoSettings.Description = &s
			i += 2
		case "--topic":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			o.RepoSettings.Topics = append(o.RepoSettings.Topics, v)
			i += 2
		case "--default-repo-branch":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			o.RepoSettings.DefaultBranch = v
			i += 2
		case "--allow-merge-commit":
			o.RepoSettings.AllowMergeCommit = bptr(true)
			i++
		case "--no-allow-merge-commit":
			o.RepoSettings.AllowMergeCommit = bptr(false)
			i++
		case "--allow-squash-merge":
			o.RepoSettings.AllowSquashMerge = bptr(true)
			i++
		case "--no-allow-squash-merge":
			o.RepoSettings.AllowSquashMerge = bptr(false)
			i++
		case "--allow-rebase-merge":
			o.RepoSettings.AllowRebaseMerge = bptr(true)
			i++
		case "--no-allow-rebase-merge":
			o.RepoSettings.AllowRebaseMerge = bptr(false)
			i++
		case "--delete-branch-on-merge":
			o.RepoSettings.DeleteBranchOnMerge = bptr(true)
			i++
		case "--no-delete-branch-on-merge":
			o.RepoSettings.DeleteBranchOnMerge = bptr(false)
			i++
		case "--auto-init":
			o.RepoSettings.AutoInit = bptr(true)
			i++
		case "--no-auto-init":
			o.RepoSettings.AutoInit = bptr(false)
			i++
		case "--license-template":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			o.RepoSettings.LicenseTemplate = v
			i += 2
		case "--gitignore-template":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			o.RepoSettings.GitignoreTemplate = v
			i += 2

		// --- Environment protection --------------------------------------
		case "--env-reviewer":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			if err := parseEnvReviewer(o, v); err != nil {
				return nil, err
			}
			i += 2
		case "--env-wait-timer":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			envName, rest, ok := splitColon(v)
			if !ok {
				return nil, fmt.Errorf("--env-wait-timer expects ENV:MINUTES (got: %s)", v)
			}
			n, err := strconv.Atoi(rest)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("--env-wait-timer minutes must be a non-negative integer (got: %s)", rest)
			}
			e := o.findEnv(envName)
			e.WaitTimer = &n
			i += 2
		case "--env-prevent-self-review":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			e := o.findEnv(v)
			e.PreventSelfReview = bptr(true)
			i += 2
		case "--env-no-admin-bypass":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			e := o.findEnv(v)
			e.CanAdminsBypass = bptr(false)
			i += 2
		case "--env-branch-policy":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			envName, mode, ok := splitColon(v)
			if !ok {
				return nil, fmt.Errorf("--env-branch-policy expects ENV:MODE (got: %s)", v)
			}
			switch mode {
			case "none", "protected", "custom":
			default:
				return nil, fmt.Errorf("--env-branch-policy MODE must be none|protected|custom (got: %s)", mode)
			}
			e := o.findEnv(envName)
			e.BranchPolicy = mode
			i += 2
		case "--env-branch-pattern":
			v, err := need(i, a)
			if err != nil {
				return nil, err
			}
			envName, pat, ok := splitColon(v)
			if !ok || pat == "" {
				return nil, fmt.Errorf("--env-branch-pattern expects ENV:PATTERN (got: %s)", v)
			}
			e := o.findEnv(envName)
			e.BranchPatterns = append(e.BranchPatterns, pat)
			i += 2

		case "--":
			i++
			for ; i < len(argv); i++ {
				if err := o.setRepo(argv[i]); err != nil {
					return nil, err
				}
			}
		default:
			if strings.HasPrefix(a, "-") {
				return nil, fmt.Errorf("unknown option: %s", a)
			}
			if err := o.setRepo(a); err != nil {
				return nil, err
			}
			i++
		}
	}

	if o.Repo == "" {
		return nil, errors.New("first argument must be <owner>/<repo>")
	}
	if len(o.Environments) == 0 {
		o.Environments = []*EnvSpec{{Name: "production"}}
	}
	if o.RepoMode == "" {
		o.RepoMode = RepoModeData
	}
	if err := validateRepoModeFlags(o); err != nil {
		return nil, err
	}
	return o, nil
}

// findConfigFlag scans argv for `--config FILE` or `--config=FILE`.
func findConfigFlag(argv []string) (string, bool) {
	for i, a := range argv {
		if a == "--config" {
			if i+1 < len(argv) {
				return argv[i+1], true
			}
			return "", true // present but missing; caller will fail exclusivity check
		}
		if strings.HasPrefix(a, "--config=") {
			return strings.TrimPrefix(a, "--config="), true
		}
	}
	return "", false
}

// validateRepoModeFlags rejects repo-settings flags when mode is "data".
func validateRepoModeFlags(o *Options) error {
	if o.RepoMode != RepoModeData {
		return nil
	}
	rs := o.RepoSettings
	if rs.Visibility != "" || rs.Description != nil || rs.DefaultBranch != "" ||
		len(rs.Topics) > 0 ||
		rs.AllowMergeCommit != nil || rs.AllowSquashMerge != nil ||
		rs.AllowRebaseMerge != nil || rs.DeleteBranchOnMerge != nil ||
		rs.AutoInit != nil || rs.LicenseTemplate != "" || rs.GitignoreTemplate != "" {
		return errors.New("repo-level settings require --create or --manage-repo")
	}
	return nil
}

// parseEnvReviewer parses `ENV:user:NAME-OR-ID` or `ENV:team:SLUG-OR-ID`.
func parseEnvReviewer(o *Options, spec string) error {
	envName, rest, ok := splitColon(spec)
	if !ok {
		return fmt.Errorf("--env-reviewer expects ENV:user:... or ENV:team:... (got: %s)", spec)
	}
	kind, who, ok := splitColon(rest)
	if !ok || who == "" {
		return fmt.Errorf("--env-reviewer expects ENV:user:... or ENV:team:... (got: %s)", spec)
	}
	e := o.findEnv(envName)
	switch kind {
	case "user":
		e.ReviewerUsers = append(e.ReviewerUsers, who)
	case "team":
		e.ReviewerTeams = append(e.ReviewerTeams, who)
	default:
		return fmt.Errorf("--env-reviewer kind must be user or team (got: %s)", kind)
	}
	return nil
}

// splitColon splits "a:b" into ("a", "b", true). For "a:b:c" returns ("a", "b:c", true).
func splitColon(s string) (string, string, bool) {
	idx := strings.IndexByte(s, ':')
	if idx <= 0 || idx == len(s)-1 {
		return "", "", false
	}
	return s[:idx], s[idx+1:], true
}

func (o *Options) setRepo(a string) error {
	if o.Repo != "" {
		return fmt.Errorf("unexpected positional argument: %s", a)
	}
	slash := strings.IndexByte(a, '/')
	if slash <= 0 || slash == len(a)-1 {
		return errors.New("first argument must be <owner>/<repo>")
	}
	o.Owner = a[:slash]
	o.Repo = a[slash+1:]
	return nil
}

func parseBypass(spec string) (BypassActor, error) {
	parts := strings.Split(spec, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return BypassActor{}, fmt.Errorf("invalid --bypass SPEC %q (expected <actor_type>:<actor_id>[:<mode>])", spec)
	}
	actorType, actorIDStr := parts[0], parts[1]
	mode := "always"
	if len(parts) == 3 {
		mode = parts[2]
	}
	if actorType == "" || actorIDStr == "" {
		return BypassActor{}, fmt.Errorf("invalid --bypass SPEC %q (expected <actor_type>:<actor_id>[:<mode>])", spec)
	}
	id, err := strconv.Atoi(actorIDStr)
	if err != nil || id < 0 {
		return BypassActor{}, fmt.Errorf("invalid --bypass SPEC %q (actor_id must be numeric)", spec)
	}
	switch mode {
	case "always", "pull_request":
	default:
		return BypassActor{}, fmt.Errorf("invalid --bypass mode %q (must be 'always' or 'pull_request')", mode)
	}
	switch actorType {
	case "RepositoryRole", "Team", "Integration", "OrganizationAdmin", "DeployKey":
	default:
		return BypassActor{}, fmt.Errorf("invalid --bypass actor_type %q", actorType)
	}
	return BypassActor{ActorID: id, ActorType: actorType, BypassMode: mode}, nil
}

// DefaultStateDir returns the per-repo state directory under
// $XDG_STATE_HOME/gh-repo-bootstrap or ~/.local/state/gh-repo-bootstrap.
func DefaultStateDir(owner, repo string) string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = home + "/.local/state"
	}
	return fmt.Sprintf("%s/gh-repo-bootstrap/%s__%s", base, owner, repo)
}

// EnvNames returns the list of environment names in insertion order, for
// secret-file matching and other places that don't need the full EnvSpec.
func (o *Options) EnvNames() []string {
	out := make([]string, len(o.Environments))
	for i, e := range o.Environments {
		out[i] = e.Name
	}
	return out
}
