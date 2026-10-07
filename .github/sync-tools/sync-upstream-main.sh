#!/usr/bin/env bash
# Sync only main, preserving upstream commits and rejecting non-fast-forward updates.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "Usage: $0 <fork-repository-url> <upstream-repository-url>" >&2
  exit 2
fi

fork_url=$1
upstream_url=$2
sync_dir=$(mktemp -d)
trap 'rm -rf "$sync_dir"' EXIT
sync_repo="$sync_dir/repository.git"

report() {
  printf '%s\n' "$*"
  if [[ -n ${GITHUB_STEP_SUMMARY:-} ]]; then
    printf '%s\n' "$*" >> "$GITHUB_STEP_SUMMARY"
  fi
}

# Use an isolated bare repository: never check out or execute upstream code.
git init --bare --quiet "$sync_repo"
git -C "$sync_repo" fetch --quiet --no-tags --filter=blob:none "$fork_url" \
  refs/heads/main:refs/heads/fork-main
git -C "$sync_repo" fetch --quiet --no-tags --filter=blob:none "$upstream_url" \
  refs/heads/main:refs/heads/upstream-main
fork_sha=$(git -C "$sync_repo" rev-parse refs/heads/fork-main)
upstream_sha=$(git -C "$sync_repo" rev-parse refs/heads/upstream-main)

report '## Upstream main synchronization'
report "Fork main before: \`$fork_sha\`"
report "Upstream main: \`$upstream_sha\`"

if [[ "$fork_sha" == "$upstream_sha" ]]; then
  report 'Already up to date. No commits to fetch; no branches were changed.'
  exit 0
fi

if ! git -C "$sync_repo" merge-base --is-ancestor "$fork_sha" "$upstream_sha"; then
  report 'ERROR: Fork main has commits absent from upstream. Manual review is required; no branches were changed.'
  exit 1
fi

commit_count=$(git -C "$sync_repo" rev-list --count "$fork_sha..$upstream_sha")
# Never force. Git rejects a competing non-fast-forward update after the ancestry check.
if ! git -C "$sync_repo" push "$fork_url" "$upstream_sha:refs/heads/main"; then
  report 'ERROR: The fast-forward push was rejected or failed. Check the Git error above and remote main before retrying. No force push was attempted.'
  exit 1
fi

remote_sha=$(git -C "$sync_repo" ls-remote --exit-code "$fork_url" refs/heads/main | awk '{print $1}')
if [[ "$remote_sha" != "$upstream_sha" ]]; then
  report "ERROR: Post-push verification found main at \`$remote_sha\`; expected \`$upstream_sha\`. Manual review is required."
  exit 1
fi

report "Synchronized $commit_count upstream commit(s). Fork main now: \`$remote_sha\`."
report 'The release branch was not changed. Review the changed paths before a manual release merge.'
report '### Changed paths'
report '```text'
while IFS= read -r changed_path; do
  report "$changed_path"
done < <(git -C "$sync_repo" diff --name-only "$fork_sha" "$upstream_sha")
report '```'
