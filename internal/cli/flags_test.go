package cli

import (
	"strings"
	"testing"
)

func TestParse_ConfigExclusivity_OK(t *testing.T) {
	o, err := Parse([]string{"--config", "foo.toml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.ConfigFile != "foo.toml" {
		t.Fatalf("ConfigFile = %q", o.ConfigFile)
	}
}

func TestParse_ConfigExclusivity_Reject(t *testing.T) {
	for _, args := range [][]string{
		{"--config", "foo.toml", "JMR-dev/x"},
		{"JMR-dev/x", "--config", "foo.toml"},
		{"--config", "foo.toml", "--signed"},
	} {
		_, err := Parse(args)
		if err == nil || !strings.Contains(err.Error(), "No other flags allowed with config file.") {
			t.Errorf("args %v: expected exclusivity error, got %v", args, err)
		}
	}
}

func TestParse_EnvReviewer(t *testing.T) {
	o, err := Parse([]string{
		"JMR-dev/x",
		"--env", "production",
		"--env-reviewer", "production:user:octocat",
		"--env-reviewer", "production:team:JMR-dev/release-managers",
		"--env-reviewer", "staging:user:1234",
		"--env-branch-policy", "production:custom",
		"--env-branch-pattern", "production:release/*",
		"--env-wait-timer", "production:5",
		"--env-prevent-self-review", "production",
		"--env-no-admin-bypass", "production",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(o.Environments) != 2 {
		t.Fatalf("got %d envs, want 2", len(o.Environments))
	}
	prod := o.Environments[0]
	if prod.Name != "production" {
		t.Fatalf("prod.Name = %q", prod.Name)
	}
	if len(prod.ReviewerUsers) != 1 || prod.ReviewerUsers[0] != "octocat" {
		t.Errorf("ReviewerUsers = %v", prod.ReviewerUsers)
	}
	if len(prod.ReviewerTeams) != 1 || prod.ReviewerTeams[0] != "JMR-dev/release-managers" {
		t.Errorf("ReviewerTeams = %v", prod.ReviewerTeams)
	}
	if prod.BranchPolicy != "custom" || len(prod.BranchPatterns) != 1 {
		t.Errorf("branch policy/patterns = %v / %v", prod.BranchPolicy, prod.BranchPatterns)
	}
	if prod.WaitTimer == nil || *prod.WaitTimer != 5 {
		t.Errorf("WaitTimer = %v", prod.WaitTimer)
	}
	if prod.PreventSelfReview == nil || !*prod.PreventSelfReview {
		t.Errorf("PreventSelfReview = %v", prod.PreventSelfReview)
	}
	if prod.CanAdminsBypass == nil || *prod.CanAdminsBypass {
		t.Errorf("CanAdminsBypass = %v", prod.CanAdminsBypass)
	}
}

func TestParse_RepoSettingsRequireMode(t *testing.T) {
	_, err := Parse([]string{"JMR-dev/x", "--visibility", "private"})
	if err == nil || !strings.Contains(err.Error(), "--create or --manage-repo") {
		t.Fatalf("expected mode-required error, got %v", err)
	}
}

func TestParse_CreateAndManageMutuallyExclusive(t *testing.T) {
	_, err := Parse([]string{"JMR-dev/x", "--create", "--manage-repo"})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutex error, got %v", err)
	}
}

func TestParse_CreateFlags(t *testing.T) {
	o, err := Parse([]string{
		"JMR-dev/x", "--create",
		"--visibility", "private",
		"--description", "hello",
		"--topic", "go",
		"--topic", "api",
		"--default-repo-branch", "main",
		"--no-allow-merge-commit",
		"--allow-squash-merge",
		"--delete-branch-on-merge",
		"--auto-init",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.RepoMode != RepoModeCreate {
		t.Errorf("RepoMode = %v", o.RepoMode)
	}
	if o.RepoSettings.Visibility != "private" {
		t.Errorf("Visibility = %q", o.RepoSettings.Visibility)
	}
	if o.RepoSettings.Description == nil || *o.RepoSettings.Description != "hello" {
		t.Errorf("Description = %v", o.RepoSettings.Description)
	}
	if len(o.RepoSettings.Topics) != 2 {
		t.Errorf("Topics = %v", o.RepoSettings.Topics)
	}
	if o.RepoSettings.AllowMergeCommit == nil || *o.RepoSettings.AllowMergeCommit {
		t.Errorf("AllowMergeCommit = %v", o.RepoSettings.AllowMergeCommit)
	}
}
