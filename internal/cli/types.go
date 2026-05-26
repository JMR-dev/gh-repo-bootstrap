package cli

// RepoMode selects how the repository itself is handled by Pulumi.
//
//   - RepoModeData   — repository is a data source only; ruleset/envs/secrets
//     are managed against it but the repo settings themselves are untouched.
//     This is the default and matches the original bash behavior.
//   - RepoModeCreate — Pulumi creates the repository from scratch.
//   - RepoModeManage — Pulumi imports the existing repository into state on
//     first apply, then manages its settings going forward.
type RepoMode string

const (
	RepoModeData   RepoMode = "data"
	RepoModeCreate RepoMode = "create"
	RepoModeManage RepoMode = "manage"
)

// RepoSettings holds the repo-level configuration applied when RepoMode is
// "create" or "manage". Pointers distinguish "unset" from a deliberate false.
type RepoSettings struct {
	Visibility          string
	Description         *string
	DefaultBranch       string
	Topics              []string
	AllowMergeCommit    *bool
	AllowSquashMerge    *bool
	AllowRebaseMerge    *bool
	DeleteBranchOnMerge *bool

	// Create-only fields (ignored on manage).
	AutoInit          *bool
	LicenseTemplate   string
	GitignoreTemplate string
}

// EnvSpec describes one deployment environment and its protection rules.
// ReviewerUsers / ReviewerTeams entries are raw strings (numeric IDs as
// strings, GitHub logins, or org/team-slug pairs) before they are resolved
// by the runner.
type EnvSpec struct {
	Name              string
	WaitTimer         *int
	PreventSelfReview *bool
	CanAdminsBypass   *bool
	ReviewerUsers     []string
	ReviewerTeams     []string

	// BranchPolicy: "" / "none" / "protected" / "custom"
	BranchPolicy   string
	BranchPatterns []string
}
