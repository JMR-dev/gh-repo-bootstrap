// Package pulumiprog builds the inline Pulumi program that manages a single
// GitHub repository's settings, ruleset, environments, and Actions secrets.
package pulumiprog

import (
	"fmt"

	"github.com/JMR-dev/gh-repo-bootstrap/internal/cli"
	"github.com/JMR-dev/gh-repo-bootstrap/internal/secrets"
	"github.com/pulumi/pulumi-github/sdk/v6/go/github"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// ResolvedEnv mirrors cli.EnvSpec but with reviewer IDs resolved to ints.
type ResolvedEnv struct {
	Name              string
	WaitTimer         *int
	PreventSelfReview *bool
	CanAdminsBypass   *bool
	ReviewerUserIDs   []int
	ReviewerTeamIDs   []int
	BranchPolicy      string
	BranchPatterns    []string
}

// Inputs is the set of values needed by the inline program. It is collected
// from CLI flags / TOML + parsed secret files / resolved reviewer IDs before
// pulumi runs.
type Inputs struct {
	Owner       string
	Repo        string
	Branch      string
	Reviews     int
	Signed      bool
	RulesetName string

	Environments []ResolvedEnv

	Bypass      []cli.BypassActor
	RepoSecrets []secrets.Pair
	EnvSecrets  []secrets.EnvFile

	RepoMode     cli.RepoMode
	RepoSettings cli.RepoSettings
}

// Build returns a pulumi.RunFunc that materializes the resources described
// by in.
func Build(in Inputs) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
		// --- Repository ----------------------------------------------------
		// repoName is the StringInput that downstream resources reference as
		// `Repository`. For data mode it's just the literal string; for
		// create/manage it's the managed resource's Name output so child
		// resources implicitly depend on the repo.
		var repoName pulumi.StringInput = pulumi.String(in.Repo)
		var repoDep pulumi.Resource
		if in.RepoMode == cli.RepoModeCreate || in.RepoMode == cli.RepoModeManage {
			args := buildRepoArgs(in.Repo, in.RepoSettings, in.RepoMode)
			opts := []pulumi.ResourceOption{}
			if in.RepoMode == cli.RepoModeManage {
				opts = append(opts, pulumi.Import(pulumi.ID(in.Repo)))
			}
			repo, err := github.NewRepository(ctx, "repo_"+sanitize(in.Repo), args, opts...)
			if err != nil {
				return fmt.Errorf("creating repository resource: %w", err)
			}
			repoName = repo.Name
			repoDep = repo
		}

		// --- Ruleset on the default branch --------------------------------
		var bypass github.RepositoryRulesetBypassActorArray
		for _, b := range in.Bypass {
			bypass = append(bypass, &github.RepositoryRulesetBypassActorArgs{
				ActorId:    pulumi.Int(b.ActorID),
				ActorType:  pulumi.String(b.ActorType),
				BypassMode: pulumi.String(toCamelBypassMode(b.BypassMode)),
			})
		}

		rulesetOpts := []pulumi.ResourceOption{}
		if repoDep != nil {
			rulesetOpts = append(rulesetOpts, pulumi.DependsOn([]pulumi.Resource{repoDep}))
		}
		_, err := github.NewRepositoryRuleset(ctx, "default_branch", &github.RepositoryRulesetArgs{
			Repository:  repoName,
			Name:        pulumi.String(in.RulesetName),
			Target:      pulumi.String("branch"),
			Enforcement: pulumi.String("active"),
			Conditions: &github.RepositoryRulesetConditionsArgs{
				RefName: &github.RepositoryRulesetConditionsRefNameArgs{
					Includes: pulumi.StringArray{pulumi.String("refs/heads/" + in.Branch)},
					Excludes: pulumi.StringArray{},
				},
			},
			BypassActors: bypass,
			Rules: &github.RepositoryRulesetRulesArgs{
				Deletion:           pulumi.Bool(true),
				NonFastForward:     pulumi.Bool(true),
				RequiredSignatures: pulumi.Bool(in.Signed),
				PullRequest: &github.RepositoryRulesetRulesPullRequestArgs{
					RequiredApprovingReviewCount:   pulumi.Int(in.Reviews),
					DismissStaleReviewsOnPush:      pulumi.Bool(true),
					RequireCodeOwnerReview:         pulumi.Bool(false),
					RequireLastPushApproval:        pulumi.Bool(false),
					RequiredReviewThreadResolution: pulumi.Bool(true),
				},
			},
		}, rulesetOpts...)
		if err != nil {
			return fmt.Errorf("creating ruleset: %w", err)
		}

		// --- Environments + protection rules ------------------------------
		envByName := map[string]*github.RepositoryEnvironment{}
		for _, e := range in.Environments {
			envArgs := &github.RepositoryEnvironmentArgs{
				Repository:  repoName,
				Environment: pulumi.String(e.Name),
			}
			if e.WaitTimer != nil {
				envArgs.WaitTimer = pulumi.Int(*e.WaitTimer)
			}
			if e.PreventSelfReview != nil {
				envArgs.PreventSelfReview = pulumi.Bool(*e.PreventSelfReview)
			}
			if e.CanAdminsBypass != nil {
				envArgs.CanAdminsBypass = pulumi.Bool(*e.CanAdminsBypass)
			}
			if len(e.ReviewerUserIDs) > 0 || len(e.ReviewerTeamIDs) > 0 {
				users := make(pulumi.IntArray, 0, len(e.ReviewerUserIDs))
				for _, id := range e.ReviewerUserIDs {
					users = append(users, pulumi.Int(id))
				}
				teams := make(pulumi.IntArray, 0, len(e.ReviewerTeamIDs))
				for _, id := range e.ReviewerTeamIDs {
					teams = append(teams, pulumi.Int(id))
				}
				envArgs.Reviewers = github.RepositoryEnvironmentReviewerArray{
					&github.RepositoryEnvironmentReviewerArgs{
						Users: users,
						Teams: teams,
					},
				}
			}
			switch e.BranchPolicy {
			case "protected":
				envArgs.DeploymentBranchPolicy = &github.RepositoryEnvironmentDeploymentBranchPolicyArgs{
					ProtectedBranches:    pulumi.Bool(true),
					CustomBranchPolicies: pulumi.Bool(false),
				}
			case "custom":
				envArgs.DeploymentBranchPolicy = &github.RepositoryEnvironmentDeploymentBranchPolicyArgs{
					ProtectedBranches:    pulumi.Bool(false),
					CustomBranchPolicies: pulumi.Bool(true),
				}
			case "none":
				envArgs.DeploymentBranchPolicy = &github.RepositoryEnvironmentDeploymentBranchPolicyArgs{
					ProtectedBranches:    pulumi.Bool(false),
					CustomBranchPolicies: pulumi.Bool(false),
				}
			}
			envOpts := []pulumi.ResourceOption{}
			if repoDep != nil {
				envOpts = append(envOpts, pulumi.DependsOn([]pulumi.Resource{repoDep}))
			}
			env, err := github.NewRepositoryEnvironment(ctx, "env_"+sanitize(e.Name), envArgs, envOpts...)
			if err != nil {
				return fmt.Errorf("creating environment %q: %w", e.Name, err)
			}
			envByName[e.Name] = env

			// Custom branch policies (one resource per pattern).
			if e.BranchPolicy == "custom" {
				for i, pat := range e.BranchPatterns {
					_, err := github.NewRepositoryDeploymentBranchPolicy(ctx,
						fmt.Sprintf("bp_%s_%d", sanitize(e.Name), i),
						&github.RepositoryDeploymentBranchPolicyArgs{
							Repository:      repoName,
							EnvironmentName: env.Environment,
							Name:            pulumi.String(pat),
						},
						pulumi.DependsOn([]pulumi.Resource{env}),
					)
					if err != nil {
						return fmt.Errorf("creating branch policy %q for env %q: %w", pat, e.Name, err)
					}
				}
			}
		}

		// --- Repo-level Actions secrets -----------------------------------
		for i, s := range in.RepoSecrets {
			_, err := github.NewActionsSecret(ctx, fmt.Sprintf("rs_%d", i), &github.ActionsSecretArgs{
				Repository:     repoName,
				SecretName:     pulumi.String(s.Name),
				PlaintextValue: pulumi.String(s.Value),
			})
			if err != nil {
				return fmt.Errorf("creating repo secret %q: %w", s.Name, err)
			}
		}

		// --- Env-level Actions secrets ------------------------------------
		for ei, ef := range in.EnvSecrets {
			env, ok := envByName[ef.Env]
			if !ok {
				return fmt.Errorf("env-secrets target %q has no matching environment", ef.Env)
			}
			for si, s := range ef.Secrets {
				_, err := github.NewActionsEnvironmentSecret(ctx,
					fmt.Sprintf("es_%d_%d", ei, si),
					&github.ActionsEnvironmentSecretArgs{
						Repository:     repoName,
						Environment:    env.Environment,
						SecretName:     pulumi.String(s.Name),
						PlaintextValue: pulumi.String(s.Value),
					},
					pulumi.DependsOn([]pulumi.Resource{env}),
				)
				if err != nil {
					return fmt.Errorf("creating env secret %q in %q: %w", s.Name, ef.Env, err)
				}
			}
		}

		// --- Outputs -------------------------------------------------------
		ctx.Export("repository_full_name", pulumi.String(in.Owner+"/"+in.Repo))
		envNames := make(pulumi.StringArray, 0, len(in.Environments))
		for _, e := range in.Environments {
			envNames = append(envNames, pulumi.String(e.Name))
		}
		ctx.Export("environments", envNames)
		return nil
	}
}

// buildRepoArgs constructs github.RepositoryArgs from RepoSettings, honoring
// create-only fields only when mode == RepoModeCreate.
func buildRepoArgs(name string, s cli.RepoSettings, mode cli.RepoMode) *github.RepositoryArgs {
	args := &github.RepositoryArgs{
		Name: pulumi.String(name),
	}
	if s.Visibility != "" {
		args.Visibility = pulumi.String(s.Visibility)
	}
	if s.Description != nil {
		args.Description = pulumi.String(*s.Description)
	}
	if s.DefaultBranch != "" {
		args.DefaultBranch = pulumi.String(s.DefaultBranch)
	}
	if len(s.Topics) > 0 {
		topics := make(pulumi.StringArray, 0, len(s.Topics))
		for _, t := range s.Topics {
			topics = append(topics, pulumi.String(t))
		}
		args.Topics = topics
	}
	if s.AllowMergeCommit != nil {
		args.AllowMergeCommit = pulumi.Bool(*s.AllowMergeCommit)
	}
	if s.AllowSquashMerge != nil {
		args.AllowSquashMerge = pulumi.Bool(*s.AllowSquashMerge)
	}
	if s.AllowRebaseMerge != nil {
		args.AllowRebaseMerge = pulumi.Bool(*s.AllowRebaseMerge)
	}
	if s.DeleteBranchOnMerge != nil {
		args.DeleteBranchOnMerge = pulumi.Bool(*s.DeleteBranchOnMerge)
	}
	if mode == cli.RepoModeCreate {
		if s.AutoInit != nil {
			args.AutoInit = pulumi.Bool(*s.AutoInit)
		}
		if s.LicenseTemplate != "" {
			args.LicenseTemplate = pulumi.String(s.LicenseTemplate)
		}
		if s.GitignoreTemplate != "" {
			args.GitignoreTemplate = pulumi.String(s.GitignoreTemplate)
		}
	}
	return args
}

// toCamelBypassMode converts the snake_case CLI value to the camelCase the
// Pulumi GitHub provider expects (always | pullRequest).
func toCamelBypassMode(m string) string {
	if m == "pull_request" {
		return "pullRequest"
	}
	return m
}

// sanitize turns a name into a Pulumi-resource-name-safe slug.
func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
