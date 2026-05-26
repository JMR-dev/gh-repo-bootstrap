# gh-repo-bootstrap

A [`gh` CLI extension](https://docs.github.com/en/github-cli/github-cli/using-github-cli-extensions)
for applying a standard set of guard-rails to a GitHub repository, powered by
[Pulumi](https://www.pulumi.com/):

- A branch protection ruleset on the default branch
  (no force-push, no deletion, required PRs with N approvals,
  resolved review threads, optional signed commits)
- Optional **repository creation** (`--create`) or **adoption of an
  existing repo** (`--manage-repo`) so the tool also manages
  repo-level settings: visibility, description, default branch,
  topics, merge buttons, delete-branch-on-merge, etc.
- A configurable set of deployment environments with optional
  **protection rules** (required reviewers, wait timer,
  prevent-self-review, admin bypass, deployment branch policies
  including custom branch/tag patterns)
- Optional repository- and environment-level GitHub Actions secrets,
  sourced from `KEY = "value"` files
- A single TOML file (`--config FILE`) can describe everything above

## Install

```sh
gh extension install JMR-dev/gh-repo-bootstrap
```

`gh` will fetch the precompiled binary for your OS/arch from the latest
release. You also need:

- [`pulumi`](https://www.pulumi.com/docs/iac/download-install/) on `PATH`
- `gh` already authenticated (`gh auth login`)

## Use

```sh
# Apply defaults (1 review, production env) to a repo:
gh repo-bootstrap JMR-dev/my-app

# Custom: 2 reviews, signed commits, multiple environments:
gh repo-bootstrap JMR-dev/api \
  --reviews 2 --signed \
  --env production --env staging --env preview

# Solo maintainer: allow the Admin role (you) to bypass the ruleset
# so you can merge your own PRs without a second approver:
gh repo-bootstrap JMR-dev/solo-project --solo

# Preview without applying:
gh repo-bootstrap JMR-dev/my-app --plan

# Tear down what this tool manages:
gh repo-bootstrap JMR-dev/my-app --destroy
```

### Creating a new repo

`--create` registers the repo as a Pulumi resource. It prompts for
visibility and description if those flags are not supplied; everything
else uses defaults or flag/config values:

```sh
gh repo-bootstrap JMR-dev/my-new-app --create \
  --visibility private \
  --description "Service for X" \
  --topic go --topic service \
  --no-allow-merge-commit --allow-squash-merge \
  --delete-branch-on-merge \
  --auto-init
```

### Managing an existing repo's settings

`--manage-repo` imports the existing GitHub repository into Pulumi
state on the first apply and manages it from then on. **Always run
`--plan` first** — the first apply imports the repo *and* reconciles
any drift between your flags/config and the live settings in a single
operation:

```sh
gh repo-bootstrap JMR-dev/api --manage-repo \
  --visibility private \
  --description "API service" \
  --default-repo-branch main \
  --no-allow-merge-commit --allow-squash-merge \
  --plan
```

### Environment protection rules

```sh
gh repo-bootstrap JMR-dev/api \
  --env production \
  --env-reviewer production:user:octocat \
  --env-reviewer production:team:JMR-dev/release-managers \
  --env-wait-timer production:5 \
  --env-prevent-self-review production \
  --env-no-admin-bypass production \
  --env-branch-policy production:custom \
  --env-branch-pattern production:'release/*' \
  --env-branch-pattern production:'hotfix/*'
```

Reviewer specs accept numeric IDs *or* string identifiers
(`user:octocat`, `team:JMR-dev/release-managers`). Strings are
resolved to numeric IDs via `gh api` before Pulumi runs. Team specs
must include the org (`org/team-slug`).

### TOML configuration

A single `--config FILE` can describe everything. When `--config`
is used, **no other flags are allowed**:

```toml
owner = "JMR-dev"
name  = "my-new-app"
mode  = "create"      # or "manage", or "data" (default)

[repo]
visibility             = "private"
description            = "Service for X"
default_branch         = "main"
topics                 = ["go", "service"]
allow_merge_commit     = false
allow_squash_merge     = true
allow_rebase_merge     = false
delete_branch_on_merge = true
auto_init              = true

[ruleset]
name                   = "default-branch-protection"
branch                 = "main"
required_reviews       = 1
require_signed_commits = false

[[ruleset.bypass]]
actor_type = "RepositoryRole"
actor_id   = 5
mode       = "always"

[[environments]]
name                = "production"
wait_timer          = 5
prevent_self_review = true
can_admins_bypass   = false
reviewers_users     = ["octocat", 12345]
reviewers_teams     = ["JMR-dev/release-managers"]
branch_policy       = "custom"
branch_patterns     = ["release/*", "hotfix/*"]

[[environments]]
name = "staging"

[secrets]
repo_file = "./repo.secrets.tfvars"
env_dir   = "./env-secrets"
```

When `mode = "create"` all `[repo]` keys listed above are required —
the loader errors with a single line naming the missing field. When
`mode = "manage"`, the same keys are required except `auto_init` /
`license_template` / `gitignore_template`, which apply only at
creation time.

### Uploading GitHub Actions secrets

The extension can also upload Actions secrets — both repository-level
and per-environment — sourced from `KEY = "value"` files:

```sh
# Repo-level:
cat > repo.secrets.tfvars <<'EOF'
API_TOKEN      = "ghp_..."
WEBHOOK_SECRET = "s3kr3t"
EOF
gh repo-bootstrap JMR-dev/my-app --upload-repo-secrets ./repo.secrets.tfvars

# Per-environment: one <env>.tfvars per env in a directory.
mkdir env-secrets
cat > env-secrets/production.tfvars <<'EOF'
DB_PASSWORD = "prodpw"
EOF
cat > env-secrets/staging.tfvars <<'EOF'
DB_PASSWORD = "stagepw"
EOF
gh repo-bootstrap JMR-dev/my-app \
  --env production --env staging \
  --upload-env-secrets ./env-secrets
```

Each line in a secrets file must be `NAME = "value"`. Names follow
GitHub's rules (alphanumerics + underscore, no leading digit, no
`GITHUB_` prefix). `#` and `//` comments are supported. Values may be
double-quoted (with `\\ \" \n \r \t` escapes) or single-quoted (raw).

Secret values are wrapped in Pulumi secret outputs, so they are
encrypted at rest in the state file and elided from `--plan` output.

### State and secret encryption

State is kept per-repo under
`$XDG_STATE_HOME/gh-repo-bootstrap/<owner>__<repo>/`
(default `~/.local/state/gh-repo-bootstrap/...`). Override with `--state-dir`.

Each per-repo directory contains:

- A Pulumi project (`Pulumi.yaml`, `Pulumi.bootstrap.yaml`)
- The local-backend stack state (encrypted JSON)
- `.passphrase` — a `chmod 600` file holding an auto-generated
  passphrase used to encrypt secrets in the state file

> **Back up the whole state directory, not just the state JSON.** If
> `.passphrase` is lost, the stack's encrypted secrets cannot be
> decrypted and the stack will be unusable. You can also override the
> passphrase by exporting `PULUMI_CONFIG_PASSPHRASE` before running the
> command.

## Migrating from the OpenTofu-based versions

Previous versions of this extension used OpenTofu. There is no
automatic migration: if you previously ran `gh repo-bootstrap` against
a repo, the GitHub ruleset / environments / secrets already exist on
GitHub and Pulumi will try to **create** them again on first run,
which can fail or conflict.

To migrate a repo:

1. Either tear down the previously-managed resources (e.g. delete the
   ruleset and environments via the GitHub UI or
   `gh api -X DELETE ...`) and let Pulumi re-create them, or
2. Use `pulumi import` against the local stack to adopt the existing
   resources without recreating them.

The old OpenTofu state directory
(`$XDG_STATE_HOME/gh-repo-bootstrap/<owner>__<repo>/terraform.tfstate`)
is safe to delete once the Pulumi stack is in place.

## Hacking

```sh
go build ./...
go test ./...
```

The CLI is a single Go binary that uses the Pulumi
[Automation API](https://www.pulumi.com/docs/iac/automation-api/) to
run an inline program against the
[`pulumi-github`](https://www.pulumi.com/registry/packages/github/) provider.

## License

MIT
