output "repository_full_name" {
  value       = data.github_repository.this.full_name
  description = "Full name (owner/repo) of the repository being managed."
}

output "ruleset_id" {
  value       = github_repository_ruleset.default_branch.id
  description = "ID of the branch protection ruleset."
}

output "environments" {
  value       = sort([for e in github_repository_environment.envs : e.environment])
  description = "Environments managed by this configuration."
}
