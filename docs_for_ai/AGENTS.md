## Mainline

<!-- mainline-agents-md-version: 3 -->

This project uses Mainline to record the intent behind AI-assisted code changes.

### Before changing code

    mainline status --json

If there is no active intent, start one:

    mainline start "<short description of the user's goal>" --json

For unfamiliar subsystems, query history (auto-syncs with the team):

    mainline context <keyword> --json

### While working

After each meaningful logical change, record a turn:

    mainline append "<specific description of what changed>" --json

### When the task is complete

1. Make sure all code changes are committed:

       git add <files> && git commit -m "<message>"

2. Prepare a seal package:

       mainline seal --prepare --json

3. Generate JSON matching the returned schema. Include rich tags in
   the fingerprint (primary subsystem, synonyms, parent concepts,
   related technologies):

       "tags": ["auth", "authentication", "security", "jwt", "session"]

4. Submit it:

       mainline seal --submit --json < seal.json

   Mainline syncs with the team and runs phase1 conflict checks
   automatically inside --submit. If the JSON response includes a
   "conflicts" array, surface those conflicts to the user clearly
   before continuing.

### Semantic conflict checks

When asked to check semantic conflicts (auto-syncs first):

    mainline check --prepare --intent <id> --json

Generate a CheckJudgmentResult JSON matching the schema, then submit:

    mainline check --submit --json < judgment.json

### Do not run unless explicitly asked by the user

    mainline merge
    mainline pin <intent> <commit>
    mainline revert
