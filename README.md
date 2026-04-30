# gh-repo-bootstrap

A reusable [OpenTofu](https://opentofu.org/) module **and** a
[`gh` CLI extension](https://docs.github.com/en/github-cli/github-cli/using-github-cli-extensions)
for applying a standard set of guard-rails to a GitHub repository:

- A branch protection ruleset on the default branch
  (no force-push, no deletion, required PRs with N approvals,
  resolved review threads, optional signed commits)
- A configurable set of deployment environments

## Install (gh extension)

```sh
gh extension install JMR-dev/gh-repo-bootstrap
```

You also need:
- [`tofu`](https://opentofu.org/docs/intro/install/) on PATH
- `gh` already authenticated (`gh auth login`)

## Use (gh extension)

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

# Plan only:
gh repo-bootstrap JMR-dev/my-app --plan

# Tear down what this tool manages:
gh repo-bootstrap JMR-dev/my-app --destroy
```

### Uploading GitHub Actions secrets

The extension can also upload Actions secrets — both repository-level
and per-environment — sourced from tfvars-style files:

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

Each line in a tfvars file must be `NAME = "value"`. Names follow
GitHub's rules (alphanumerics + underscore, no leading digit, no
`GITHUB_` prefix). `#` and `//` comments are supported.

Each parsed secret is materialized as its own
`variable "..." { sensitive = true }` plus matching
`github_actions_secret` / `github_actions_environment_secret`
resource in the generated wrapper, so values stay sensitive
throughout the plan and never appear in plan output. The
intermediate tfvars file the script feeds to OpenTofu is written
to a `chmod 700` directory under `/dev/shm` (when available) and
deleted on exit via a trap.

> **State warning.** GitHub stores secrets encrypted, but the
> OpenTofu state file written to `--state-dir` contains the
> plaintext values. Protect that directory and consider a remote
> backend with state encryption for anything beyond local use.

State is kept per-repo under `$XDG_STATE_HOME/gh-repo-bootstrap/<owner>__<repo>/`
(default `~/.local/state/gh-repo-bootstrap/...`). Override with `--state-dir`.

## Use (OpenTofu module directly)

```hcl
provider "github" {
  owner = "JMR-dev"
}

module "repo" {
  source = "git::https://github.com/JMR-dev/gh-repo-bootstrap.git//modules/repo?ref=main"

  repo_owner       = "JMR-dev"
  repo_name        = "my-new-project"
  required_reviews = 1
  environments     = ["production", "staging"]
}
```

See [`modules/repo/README.md`](modules/repo/README.md) for full input docs
and [`examples/basic/`](examples/basic/) for a runnable example.

## What it does **not** do

- Create the repository (point it at an existing one).
- Manage repo-level settings (merge buttons, default branch, topics, etc.).
  Use `gh api` for those, or import `github_repository` into your own root
  config if you want them under OpenTofu control.
- Manage environment protection rules (required reviewers, wait timer,
  deployment branch policies). The environments are created empty; add
  protection separately if needed.

## License

MIT
