<!-- mainline:agents:start version=27 checksum=sha256:4ccc55f1727f7efcd3655877a3b467cef851255d9c41fbbd56f6720dc955ab4d -->
## Mainline

<!-- mainline-agents-md-version: 27 -->

**Stop AI coding agents from repeating old engineering mistakes.**

This repository uses Mainline, a Git-native memory layer that tells coding agents why the code is the way it is before they edit it. Agents must use the Mainline skill workflow for non-trivial engineering work and read agent autonomy stop lines from preflight/status. Autonomy is advisory; hard gates and current user instructions take priority. Review autonomy may push a non-main branch and stops at PR; it never authorizes pushing main, merge, or release. mainline publish publishes intent metadata, not product releases. mainline agents update refreshes this repo guidance; update global skills separately with npx --yes skills update mainline --global --yes or the matching skills add.
<!-- mainline:agents:end -->
