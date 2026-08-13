---
name: debug-transcript
description: Use when a clank model decision looks wrong (bad hypothesis, wrong action, unexpected confidence) and you need to see what the LLM actually saw and said, not reconstruct it from logs.
---

# Reading the actual model transcript

When a model decision looks wrong, read the actual transcript — actual evidence beats
reconstruction.

`Store` checkpoints every turn of the reason loop to S3, at
`transcripts/<fingerprint>/<RunID>/*.json`.

To find the `RunID` for a specific run: match its nanosecond timestamp to the
`"reasoned"` `slog` line for that run — that line is what ties a fingerprint to the exact
`RunID` whose transcript directory holds the turn-by-turn `Message`/`Completion`/
`ToolCall` history the model actually produced.
