variable "repo_owner" {
  description = "GitHub user or organization that owns the repository."
  type        = string
}

variable "repo_name" {
  description = "Repository name (without owner prefix)."
  type        = string
}

variable "default_branch" {
  description = "Branch protected by the ruleset."
  type        = string
  default     = "main"
}

variable "required_reviews" {
  description = "Number of required approving reviews on PRs targeting the default branch."
  type        = number
  default     = 1
}

variable "require_signed_commits" {
  description = "Require signed commits on the protected branch."
  type        = bool
  default     = false
}

variable "environments" {
  description = "List of GitHub deployment environments to ensure exist."
  type        = list(string)
  default     = []
}

variable "ruleset_name" {
  description = "Name to give the branch protection ruleset."
  type        = string
  default     = "default-branch-protection"
}

variable "bypass_actors" {
  description = <<-EOT
    Actors permitted to bypass the ruleset. Each entry needs:
      - actor_id: numeric ID (for built-in repo roles: 1=read, 2=triage,
        3=write, 4=maintain, 5=admin)
      - actor_type: one of RepositoryRole, Team, Integration,
        OrganizationAdmin, DeployKey
      - bypass_mode: "always" or "pull_request"
  EOT
  type = list(object({
    actor_id    = number
    actor_type  = string
    bypass_mode = string
  }))
  default = []
}
