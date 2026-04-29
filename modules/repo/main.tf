data "github_repository" "this" {
  name = var.repo_name
}

resource "github_repository_ruleset" "default_branch" {
  repository  = data.github_repository.this.name
  name        = var.ruleset_name
  target      = "branch"
  enforcement = "active"

  conditions {
    ref_name {
      include = ["refs/heads/${var.default_branch}"]
      exclude = []
    }
  }

  rules {
    deletion            = true
    non_fast_forward    = true
    required_signatures = var.require_signed_commits

    pull_request {
      required_approving_review_count   = var.required_reviews
      dismiss_stale_reviews_on_push     = true
      require_code_owner_review         = false
      require_last_push_approval        = false
      required_review_thread_resolution = true
    }
  }
}

resource "github_repository_environment" "envs" {
  for_each    = toset(var.environments)
  repository  = data.github_repository.this.name
  environment = each.value
}
