# `repo` module

Reusable OpenTofu module that applies a standard set of repository
guard-rails to an existing GitHub repository:

- A branch protection ruleset on the default branch
- A configurable set of deployment environments

The module does **not** create the repository — it only manages
protection + environments on a repo that already exists.

## Inputs

| Name | Type | Default | Description |
|------|------|---------|-------------|
| `repo_owner` | string | — | GitHub user/org that owns the repo |
| `repo_name` | string | — | Repository name (no owner prefix) |
| `default_branch` | string | `"main"` | Branch to protect |
| `required_reviews` | number | `1` | Required PR approving reviews |
| `require_signed_commits` | bool | `false` | Enforce signed commits |
| `environments` | list(string) | `[]` | Environments to ensure exist |
| `ruleset_name` | string | `"default-branch-protection"` | Ruleset name |

## Provider

The caller is responsible for configuring the `github` provider (owner +
auth). The module only declares `required_providers`.

## Example

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
