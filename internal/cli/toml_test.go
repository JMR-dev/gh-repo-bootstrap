package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTmp(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	return p
}

func TestLoadConfig_DataMode_Defaults(t *testing.T) {
	p := writeTmp(t, `
owner = "JMR-dev"
name  = "x"
`)
	o, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.Owner != "JMR-dev" || o.Repo != "x" {
		t.Errorf("owner/name = %q/%q", o.Owner, o.Repo)
	}
	if o.RepoMode != RepoModeData {
		t.Errorf("RepoMode = %v", o.RepoMode)
	}
	if len(o.Environments) != 1 || o.Environments[0].Name != "production" {
		t.Errorf("Environments = %+v", o.Environments)
	}
}

func TestLoadConfig_CreateRequiresFields(t *testing.T) {
	p := writeTmp(t, `
owner = "JMR-dev"
name  = "x"
mode  = "create"
`)
	_, err := LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "[repo] table is required") {
		t.Fatalf("expected required-table error, got %v", err)
	}
}

func TestLoadConfig_CreateMissingVisibility(t *testing.T) {
	p := writeTmp(t, `
owner = "JMR-dev"
name  = "x"
mode  = "create"

[repo]
description            = ""
default_branch         = "main"
allow_merge_commit     = false
allow_squash_merge     = true
allow_rebase_merge     = false
delete_branch_on_merge = true
auto_init              = true
`)
	_, err := LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "visibility is required") {
		t.Fatalf("expected visibility-required error, got %v", err)
	}
}

func TestLoadConfig_CreateFull(t *testing.T) {
	p := writeTmp(t, `
owner = "JMR-dev"
name  = "x"
mode  = "create"

[repo]
visibility             = "private"
description            = "hi"
default_branch         = "main"
topics                 = ["go", "api"]
allow_merge_commit     = false
allow_squash_merge     = true
allow_rebase_merge     = false
delete_branch_on_merge = true
auto_init              = true

[[environments]]
name = "production"
wait_timer = 5
prevent_self_review = true
can_admins_bypass = false
reviewers_users = ["octocat", 12345]
reviewers_teams = ["JMR-dev/release"]
branch_policy = "custom"
branch_patterns = ["release/*"]
`)
	o, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.RepoMode != RepoModeCreate {
		t.Fatalf("RepoMode = %v", o.RepoMode)
	}
	if o.RepoSettings.Visibility != "private" {
		t.Errorf("visibility = %q", o.RepoSettings.Visibility)
	}
	if len(o.RepoSettings.Topics) != 2 {
		t.Errorf("topics = %v", o.RepoSettings.Topics)
	}
	if len(o.Environments) != 1 {
		t.Fatalf("len envs = %d", len(o.Environments))
	}
	prod := o.Environments[0]
	if got := prod.ReviewerUsers; len(got) != 2 || got[0] != "octocat" || got[1] != "12345" {
		t.Errorf("reviewer users = %v", got)
	}
	if prod.BranchPolicy != "custom" || len(prod.BranchPatterns) != 1 {
		t.Errorf("branch policy = %v / patterns = %v", prod.BranchPolicy, prod.BranchPatterns)
	}
}

func TestLoadConfig_UnknownKey(t *testing.T) {
	p := writeTmp(t, `
owner = "JMR-dev"
name  = "x"
nope  = true
`)
	_, err := LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("expected unknown-key error, got %v", err)
	}
}
