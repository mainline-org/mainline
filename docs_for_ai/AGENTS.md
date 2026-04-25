## Mainline

<!-- mainline-agents-md-version: 1 -->

This project uses Mainline to record the intent behind AI-assisted code changes.
You are expected to use Mainline while modifying code.

### Before changing code

    mainline status --json
    mainline start "<short description of the user's goal>" --json
    mainline context --json

### While working

    mainline append "<specific description of what changed>" --json

### When the task is complete

    mainline seal --prepare --json
    mainline seal --submit - --json

### Do not run unless explicitly asked by the user

    mainline merge
    mainline revert
