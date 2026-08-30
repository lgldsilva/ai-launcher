# Real-world corpus for script classification

`agy, claude, cursor-agent, devin, grok, kimi, omp, opencode` are the vendor
bootstrappers that were found written to their own command path on a real
machine (2026-08-28). `ai-memory.wrapper.sh` is a managed wrapper that IS the
command and must never be classified as an installer.

They are kept verbatim because hand-written excerpts hid a real gap during
development: the Claude installer prints `Usage: <path>/claude [stable|latest…]`,
which no marker list covered, so a transcription-only fixture would have passed.
