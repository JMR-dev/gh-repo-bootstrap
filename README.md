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
