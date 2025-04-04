### Pin My Workflows (PMW)

Pin my workflows(pmw) is a command‑line tool designed to automate the process of pinning GitHub Actions workflow version tags to their corresponding commit SHA hashes. It scans your repository’s workflow files, identifies workflow usages that are not from an allowed organization and that aren’t pinned to a full commit SHA, and then uses the GitHub API to resolve the version tag.

### Features

 - Scans .github/workflows for workflow uses: lines that use version tags and replaces them with their corresponding commit SHA.

 - Allow user to add a list of organisations that workflow can be tracked via version control (This is useful when you want to stay up-to-date with minor versions of workflows from reputable organisations)

 - Prompt user to accept change, add workflow to allowed organisations  (skipping future prompts).

 - Store the list of pinned version to skip future prompts of the same action with same version tag.

 - Nested Tag Resolution: I found some workflow had tag reference another tag with commit, this app should be able to iterates and reach a commit SHA.

Example:

![image](/images/example.png)