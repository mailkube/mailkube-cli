Output formats, the terminal rule, and the stream split

The CLI writes human output when stdout is a terminal and JSON when it is not. Piping,
redirecting or running in CI therefore needs no flag at all, and a command that works at a
prompt works unchanged in a script.

  -o text     force the human rendering
  -o json     force indented JSON
  -o ndjson   force one compact JSON object per line
  -o yaml     force YAML

stdout carries the success payload and nothing else. Progress, warnings, prompts, hints and
error reports all go to stderr, and on failure stdout stays empty. That is what makes

    mailkube <command> | jq .

safe: the parser never sees half a document.

--jq '<expression>' projects the output before it is written, using an embedded implementation,
so it behaves the same on a machine with no jq installed. A string result is written raw rather
than quoted, so it can be captured straight into a shell variable.

Colour follows the terminal and the two conventions every tool honours: NO_COLOR turns it off,
CLICOLOR_FORCE turns it on. --no-color does the same as the former for one invocation.
MAILKUBE_ASCII=1 replaces the badge glyphs with ASCII ones for a terminal that cannot draw them.
