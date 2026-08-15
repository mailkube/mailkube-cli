Reading values from files and from stdin

Any flag that could take either literal content or a filename follows one rule:

  @path    read the file at that path
  @-       read standard input
  \@text   a literal value that begins with an @

It applies to --html, --text, --json, --vars and --body. At most one @- per invocation, since
there is only one standard input to consume.

Flags whose value is only ever a path take it bare, because there is nothing to disambiguate:
--attach, --record, --config.

    mailkube emails send --html @body.html --text @- < body.txt
    mailkube emails send --json @message.json
