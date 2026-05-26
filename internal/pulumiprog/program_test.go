package pulumiprog

import (
	"strings"
	"testing"

	"github.com/JMR-dev/gh-repo-bootstrap/internal/cli"
	"github.com/JMR-dev/gh-repo-bootstrap/internal/secrets"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type mockResources struct {
	t *testing.T
}

func (m mockResources) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	outputs := args.Inputs.Mappable()
	return args.Name + "_id", resource.NewPropertyMapFromMap(outputs), nil
}

func (m mockResources) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}

func TestBuild_CreateMode(t *testing.T) {
	inputs := Inputs{
		Owner:       "test-owner",
		Repo:        "test-repo",
		Branch:      "main",
		Reviews:     2,
		Signed:      true,
		RulesetName: "test-ruleset",
		Environments: []ResolvedEnv{
			{
				Name:              "production",
				WaitTimer:         intPtr(10),
				PreventSelfReview: boolPtr(true),
				CanAdminsBypass:   boolPtr(false),
				ReviewerUserIDs:   []int{123},
				ReviewerTeamIDs:   []int{456},
				BranchPolicy:      "custom",
				BranchPatterns:    []string{"release/*"},
			},
			{
				Name:         "staging",
				BranchPolicy: "protected",
			},
			{
				Name:         "dev",
				BranchPolicy: "none",
			},
		},
		Bypass: []cli.BypassActor{
			{ActorID: 99, ActorType: "Team", BypassMode: "pull_request"},
			{ActorID: 100, ActorType: "RepositoryRole", BypassMode: "always"},
		},
		RepoSecrets: []secrets.Pair{
			{Name: "REPO_SECRET", Value: "secret-val"},
		},
		EnvSecrets: []secrets.EnvFile{
			{
				Env: "production",
				Secrets: []secrets.Pair{
					{Name: "ENV_SECRET", Value: "env-secret-val"},
				},
			},
		},
		RepoMode: cli.RepoModeCreate,
		RepoSettings: cli.RepoSettings{
			Visibility:          "private",
			Description:         strPtr("hello description"),
			DefaultBranch:       "main",
			Topics:              []string{"go", "api"},
			AllowMergeCommit:    boolPtr(false),
			AllowSquashMerge:    boolPtr(true),
			AllowRebaseMerge:    boolPtr(false),
			DeleteBranchOnMerge: boolPtr(true),
			AutoInit:            boolPtr(true),
			LicenseTemplate:     "mit",
			GitignoreTemplate:   "Go",
		},
	}

	err := pulumi.RunErr(Build(inputs), pulumi.WithMocks("project", "stack", mockResources{t: t}))
	if err != nil {
		t.Fatalf("Pulumi program failed in Create mode: %v", err)
	}
}

func TestBuild_ManageMode(t *testing.T) {
	inputs := Inputs{
		Owner:        "test-owner",
		Repo:         "test-repo",
		Branch:       "main",
		RepoMode:     cli.RepoModeManage,
		RepoSettings: cli.RepoSettings{},
	}

	err := pulumi.RunErr(Build(inputs), pulumi.WithMocks("project", "stack", mockResources{t: t}))
	if err != nil {
		t.Fatalf("Pulumi program failed in Manage mode: %v", err)
	}
}

func TestBuild_DataMode(t *testing.T) {
	inputs := Inputs{
		Owner:    "test-owner",
		Repo:     "test-repo",
		Branch:   "main",
		RepoMode: cli.RepoModeData,
	}

	err := pulumi.RunErr(Build(inputs), pulumi.WithMocks("project", "stack", mockResources{t: t}))
	if err != nil {
		t.Fatalf("Pulumi program failed in Data mode: %v", err)
	}
}

func TestBuild_EnvSecretsTargetMismatch(t *testing.T) {
	inputs := Inputs{
		Owner:  "test-owner",
		Repo:   "test-repo",
		Branch: "main",
		EnvSecrets: []secrets.EnvFile{
			{
				Env: "non-existent-env",
				Secrets: []secrets.Pair{
					{Name: "ENV_SECRET", Value: "val"},
				},
			},
		},
	}

	err := pulumi.RunErr(Build(inputs), pulumi.WithMocks("project", "stack", mockResources{t: t}))
	if err == nil || !strings.Contains(err.Error(), "has no matching environment") {
		t.Fatalf("expected target mismatch error, got: %v", err)
	}
}

func intPtr(i int) *int {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}

func strPtr(s string) *string {
	return &s
}
