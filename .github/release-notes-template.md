## Highlights

{{RELEASE HIGHLIGHTS}}

<!--
Replace the placeholder above before publishing the draft, then delete this
comment. Write for someone upgrading a running container, not for someone
reading the diff.

Breaking changes deserve a callout block rather than a bullet. Two kinds
recur here, so check for both:

  - A renamed or removed environment variable, which stops an existing
    deployment at startup.
  - A change to the `data/` layout, or to the trip JSON schema. The archive
    merge keys off the file paths and the JSON fields, so a change here
    changes what the next run reads as "already archived".

Say plainly what breaks and what the operator has to do about it:

> [!WARNING]
> **Breaking:** `OLD_VAR` is replaced by `NEW_VAR`, and setting both now
> fails at startup. See the migration notes in the README.

Otherwise a short paragraph per notable feature is enough. Everything routine
is already covered by the generated sections below.
-->

---
