<!-- mainline:agents:start version=28 checksum=sha256:7d47b5355fb5a47f07d94fb2efc10dd0d5441f8f93d91b63bd95ae54ab8a40ac -->
## Mainline

<!-- mainline-agents-md-version: 28 -->

**Stop AI coding agents from repeating old engineering mistakes.**

This repository uses Mainline, a Git-native memory layer that tells coding agents why the code is the way it is before they edit it. Agents must use the Mainline skill workflow for non-trivial engineering work and read agent autonomy stop lines from preflight/status. Autonomy is advisory; hard gates and current user instructions take priority. Review autonomy may push a non-main branch and stops at PR; it never authorizes pushing main, merge, or release. Seal-time conflicts are phase-1 overlap warnings: agents classify overlap warnings before escalating and do not paste raw JSON by default. mainline publish publishes intent metadata, not product releases. mainline agents update refreshes this repo guidance; update global skills separately with npx --yes skills update mainline --global --yes or the matching skills add.
<!-- mainline:agents:end -->
