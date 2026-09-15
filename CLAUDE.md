<!-- harness:rules:begin -->
Written by harness from rules/. Do not edit between these two markers.
https://github.com/aaronspruit/harness

### Write in Simplified Technical English

Write all documentation in Simplified Technical English (ASD-STE100). This
covers the README, the agent instruction file, comments and docstrings, commit
messages, pull request titles and bodies, release notes, and each message that a
person reads. Apply the rules before you draft, and not after.

The standard is free at https://asd-ste100.org. If the session holds the
`simple-english` skill, invoke it first.

### Keep breaking-change detail in the pull request

Do not put migration steps, upgrade instructions, or "this breaks X" in the
README or in the agent instruction file. Put them in the pull request body. If
the repository uses `changelog:` labels, apply the correct one. That label
carries the detail into the release notes.

The README and the agent instruction file describe how the code works now. The
release notes are the only record of what changed. A design note can say why the
current behavior exists. It must not tell a user what to do about an older
install.

### Write comments about the present, not the past

A comment says what the current code does, and why. It does not say what the
code replaced, or what an older version did. `git log` and `git blame` hold
that, and a stale "X still serves Y" line is wrong the moment Y changes.

When you migrate something, write the new file as if it was always that way.
Strip the same kind of history out of every file that the change touches. The
migration story goes in the commit message and in the pull request.

### Write the fewest sentences that carry the fact

State each fact in one place, and link to that place from anywhere else that
needs it. Do not repeat what a linked file already says. Do not write a
preamble, a list of what comes next, or a closing restatement. If a section
changes nothing about what the reader does, delete the section instead of
shortening it.

### Give the recommendation first

When the work needs a decision, or when you compare options, write the
recommendation in the first sentence. Then give the reasons. Then give the one
fact that argues against it. Do not spread the recommendation through the
analysis. Do not end with a list of options and no choice.

This rule applies to an answer in a session, to a pull request body, and to a
design document.

### Write what you find back to the issue

When you read an issue to plan work, add a comment to that issue. Add a second
comment when you make a branch for it. The comment holds what you decided, why,
what the work covers, and what it leaves out. It holds no install step. Those go
in the pull request, under the rule for breaking changes.

When a later finding changes the answer, comment again. Do not leave the old
comment standing alone. An issue that carries no comment makes the next session
do the same research again.
<!-- harness:rules:end -->
