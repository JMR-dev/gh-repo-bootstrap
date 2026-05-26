package cli

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// configFile is the on-disk schema for `--config FILE`. The loader translates
// it into an *Options that downstream code consumes identically to flag-based
// invocations.
type configFile struct {
	Owner    string `toml:"owner"`
	Name     string `toml:"name"`
	Mode     string `toml:"mode"`
	StateDir string `toml:"state_dir"`
	Action   string `toml:"action"`

	Repo *configRepo `toml:"repo"`

	Ruleset *configRuleset `toml:"ruleset"`

	Environments []configEnv `toml:"environments"`

	Secrets *configSecrets `toml:"secrets"`
}

type configRepo struct {
	Visibility           string   `toml:"visibility"`
	Description          *string  `toml:"description"`
	DefaultBranch        string   `toml:"default_branch"`
	Topics               []string `toml:"topics"`
	AllowMergeCommit     *bool    `toml:"allow_merge_commit"`
	AllowSquashMerge     *bool    `toml:"allow_squash_merge"`
	AllowRebaseMerge     *bool    `toml:"allow_rebase_merge"`
	DeleteBranchOnMerge  *bool    `toml:"delete_branch_on_merge"`
	AutoInit             *bool    `toml:"auto_init"`
	LicenseTemplate      string   `toml:"license_template"`
	GitignoreTemplate    string   `toml:"gitignore_template"`
}

type configRuleset struct {
	Name                 string         `toml:"name"`
	Branch               string         `toml:"branch"`
	RequiredReviews      *int           `toml:"required_reviews"`
	RequireSignedCommits *bool          `toml:"require_signed_commits"`
	Bypass               []configBypass `toml:"bypass"`
}

type configBypass struct {
	ActorType string `toml:"actor_type"`
	ActorID   int    `toml:"actor_id"`
	Mode      string `toml:"mode"`
}

type configEnv struct {
	Name              string   `toml:"name"`
	WaitTimer         *int     `toml:"wait_timer"`
	PreventSelfReview *bool    `toml:"prevent_self_review"`
	CanAdminsBypass   *bool    `toml:"can_admins_bypass"`
	ReviewersUsers    []any    `toml:"reviewers_users"`
	ReviewersTeams    []any    `toml:"reviewers_teams"`
	BranchPolicy      string   `toml:"branch_policy"`
	BranchPatterns    []string `toml:"branch_patterns"`
}

type configSecrets struct {
	RepoFile string `toml:"repo_file"`
	EnvDir   string `toml:"env_dir"`
}

// LoadConfig reads the TOML file at path and returns a fully-populated
// *Options. It validates create-mode required fields and rejects unknown
// values up front.
func LoadConfig(path string) (*Options, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cf configFile
	md, err := toml.Decode(string(raw), &cf)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if undec := md.Undecoded(); len(undec) > 0 {
		return nil, fmt.Errorf("unknown key in config: %s", undec[0])
	}

	if cf.Owner == "" {
		return nil, fmt.Errorf("config: owner is required")
	}
	if cf.Name == "" {
		return nil, fmt.Errorf("config: name is required")
	}

	mode := RepoMode(cf.Mode)
	if mode == "" {
		mode = RepoModeData
	}
	switch mode {
	case RepoModeData, RepoModeCreate, RepoModeManage:
	default:
		return nil, fmt.Errorf("config: mode must be data|create|manage (got: %s)", cf.Mode)
	}

	action := ActionApply
	switch cf.Action {
	case "", "apply":
		action = ActionApply
	case "plan":
		action = ActionPlan
	case "destroy":
		action = ActionDestroy
	default:
		return nil, fmt.Errorf("config: action must be apply|plan|destroy (got: %s)", cf.Action)
	}

	o := &Options{
		Owner:    cf.Owner,
		Repo:     cf.Name,
		Action:   action,
		StateDir: cf.StateDir,
		RepoMode: mode,
		Branch:   "main",
		Reviews:  1,
		Ruleset:  "default-branch-protection",
	}

	// --- [repo] -----------------------------------------------------------
	if cf.Repo != nil {
		r := cf.Repo
		if r.Visibility != "" && r.Visibility != "public" && r.Visibility != "private" {
			return nil, fmt.Errorf("config: [repo].visibility must be public or private (got: %s)", r.Visibility)
		}
		o.RepoSettings = RepoSettings{
			Visibility:          r.Visibility,
			Description:         r.Description,
			DefaultBranch:       r.DefaultBranch,
			Topics:              r.Topics,
			AllowMergeCommit:    r.AllowMergeCommit,
			AllowSquashMerge:    r.AllowSquashMerge,
			AllowRebaseMerge:    r.AllowRebaseMerge,
			DeleteBranchOnMerge: r.DeleteBranchOnMerge,
			AutoInit:            r.AutoInit,
			LicenseTemplate:     r.LicenseTemplate,
			GitignoreTemplate:   r.GitignoreTemplate,
		}
	}
	if mode == RepoModeCreate {
		if err := validateCreateRequired(cf.Repo); err != nil {
			return nil, err
		}
	}
	if mode == RepoModeManage {
		if err := validateManageRequired(cf.Repo); err != nil {
			return nil, err
		}
	}

	// --- [ruleset] --------------------------------------------------------
	if cf.Ruleset != nil {
		if cf.Ruleset.Name != "" {
			o.Ruleset = cf.Ruleset.Name
		}
		if cf.Ruleset.Branch != "" {
			o.Branch = cf.Ruleset.Branch
		}
		if cf.Ruleset.RequiredReviews != nil {
			if *cf.Ruleset.RequiredReviews < 0 {
				return nil, fmt.Errorf("config: [ruleset].required_reviews must be >= 0")
			}
			o.Reviews = *cf.Ruleset.RequiredReviews
		}
		if cf.Ruleset.RequireSignedCommits != nil {
			o.Signed = *cf.Ruleset.RequireSignedCommits
		}
		for _, b := range cf.Ruleset.Bypass {
			mode := b.Mode
			if mode == "" {
				mode = "always"
			}
			if mode != "always" && mode != "pull_request" {
				return nil, fmt.Errorf("config: ruleset bypass mode must be always|pull_request (got: %s)", b.Mode)
			}
			if b.ActorType == "" || b.ActorID == 0 {
				return nil, fmt.Errorf("config: ruleset bypass entry requires actor_type and actor_id")
			}
			o.Bypass = append(o.Bypass, BypassActor{
				ActorID:    b.ActorID,
				ActorType:  b.ActorType,
				BypassMode: mode,
			})
		}
	}

	// --- [[environments]] ------------------------------------------------
	for idx, e := range cf.Environments {
		if e.Name == "" {
			return nil, fmt.Errorf("config: [[environments]][%d].name is required", idx)
		}
		spec := &EnvSpec{
			Name:              e.Name,
			WaitTimer:         e.WaitTimer,
			PreventSelfReview: e.PreventSelfReview,
			CanAdminsBypass:   e.CanAdminsBypass,
		}
		for _, v := range e.ReviewersUsers {
			s, err := anyToReviewerStr(v)
			if err != nil {
				return nil, fmt.Errorf("config: env %q reviewers_users: %w", e.Name, err)
			}
			spec.ReviewerUsers = append(spec.ReviewerUsers, s)
		}
		for _, v := range e.ReviewersTeams {
			s, err := anyToReviewerStr(v)
			if err != nil {
				return nil, fmt.Errorf("config: env %q reviewers_teams: %w", e.Name, err)
			}
			spec.ReviewerTeams = append(spec.ReviewerTeams, s)
		}
		switch e.BranchPolicy {
		case "", "none", "protected", "custom":
		default:
			return nil, fmt.Errorf("config: env %q branch_policy must be none|protected|custom (got: %s)", e.Name, e.BranchPolicy)
		}
		spec.BranchPolicy = e.BranchPolicy
		spec.BranchPatterns = e.BranchPatterns
		o.Environments = append(o.Environments, spec)
	}
	if len(o.Environments) == 0 {
		o.Environments = []*EnvSpec{{Name: "production"}}
	}

	// --- [secrets] -------------------------------------------------------
	if cf.Secrets != nil {
		o.RepoSecretsFile = cf.Secrets.RepoFile
		o.EnvSecretsDir = cf.Secrets.EnvDir
	}

	return o, nil
}

// anyToReviewerStr accepts either a TOML integer or string and renders it as
// the raw reviewer identifier that the resolver will later turn into an int.
func anyToReviewerStr(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case int64:
		return fmt.Sprintf("%d", t), nil
	case int:
		return fmt.Sprintf("%d", t), nil
	default:
		return "", fmt.Errorf("reviewer entries must be strings or integers (got: %T)", v)
	}
}

// validateCreateRequired checks every required key for mode = "create".
func validateCreateRequired(r *configRepo) error {
	if r == nil {
		return fmt.Errorf("config: [repo] table is required when mode = \"create\"")
	}
	missing := func(name string) error {
		return fmt.Errorf("config: [repo].%s is required when mode = \"create\"", name)
	}
	if r.Visibility == "" {
		return missing("visibility")
	}
	if r.Description == nil {
		return missing("description")
	}
	if r.DefaultBranch == "" {
		return missing("default_branch")
	}
	if r.AllowMergeCommit == nil {
		return missing("allow_merge_commit")
	}
	if r.AllowSquashMerge == nil {
		return missing("allow_squash_merge")
	}
	if r.AllowRebaseMerge == nil {
		return missing("allow_rebase_merge")
	}
	if r.DeleteBranchOnMerge == nil {
		return missing("delete_branch_on_merge")
	}
	if r.AutoInit == nil {
		return missing("auto_init")
	}
	return nil
}

// validateManageRequired checks every required key for mode = "manage".
// auto_init / license_template / gitignore_template apply only at creation
// time and are ignored here.
func validateManageRequired(r *configRepo) error {
	if r == nil {
		return fmt.Errorf("config: [repo] table is required when mode = \"manage\"")
	}
	missing := func(name string) error {
		return fmt.Errorf("config: [repo].%s is required when mode = \"manage\"", name)
	}
	if r.Visibility == "" {
		return missing("visibility")
	}
	if r.Description == nil {
		return missing("description")
	}
	if r.DefaultBranch == "" {
		return missing("default_branch")
	}
	if r.AllowMergeCommit == nil {
		return missing("allow_merge_commit")
	}
	if r.AllowSquashMerge == nil {
		return missing("allow_squash_merge")
	}
	if r.AllowRebaseMerge == nil {
		return missing("allow_rebase_merge")
	}
	if r.DeleteBranchOnMerge == nil {
		return missing("delete_branch_on_merge")
	}
	return nil
}
