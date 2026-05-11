<!-- mainline:agents:start version=24 checksum=sha256:e9cdb21d00130a8e4a103abb2574252a08f1740a187d5e1d479c6ade07e80fdc -->
## Mainline

<!-- mainline-agents-md-version: 24 -->

**Stop AI coding agents from repeating old engineering mistakes.**

This repository uses Mainline, a Git-native memory layer that tells coding agents why the code is the way it is before they edit it. Agents must use the Mainline skill workflow for non-trivial engineering work and read agent autonomy stop lines from preflight/status. Autonomy is advisory; hard gates and current user instructions take priority. Review autonomy may push a non-main branch and stops at PR; it never authorizes pushing main, merge, or release.
<!-- mainline:agents:end -->
