<!-- mainline:agents:start version=29 checksum=sha256:f9b14aecee64ba020619bfc37ef4ea7d5b4594fa7c8c936176e299e3ffc7c035 -->
## Mainline

<!-- mainline-agents-md-version: 29 -->

**Stop AI coding agents from repeating old engineering mistakes.**

This repository uses Mainline, a Git-native memory layer that tells coding agents why the code is the way it is before they edit it. Agents must use the Mainline skill workflow for non-trivial engineering work and read agent autonomy stop lines from preflight/status. Read-only diagnosis or proposal-only work may use read-only Mainline commands but must not start an intent until the work crosses into non-trivial edits or another durable engineering record. Autonomy is advisory; hard gates and current user instructions take priority. Review autonomy may push a non-main branch and stops at PR; it never authorizes pushing main, merge, or release. Seal-time conflicts are phase-1 overlap warnings: agents classify overlap warnings before escalating and do not paste raw JSON by default. mainline publish publishes intent metadata, not product releases. mainline agents update refreshes this repo guidance; update global skills separately with npx --yes skills update mainline --global --yes or the matching skills add.
<!-- mainline:agents:end -->
