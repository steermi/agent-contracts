# Build report: <title>

Started from: [`<short SHA>`](<commit web_url>) · Ended at: [`<short SHA>`](<commit web_url>) (or "uncommitted changes")

Branch: [`<branch>`](<branch web_url>) · Merge request: [!N](<merge request web_url>) (GitLab) or [#N](<pull request web_url>) (GitHub)

<!-- Link every branch, commit and merge request with the web_url your
tools return or one built from the repository's web URL. Without a URL,
write the name or SHA in backticks; never invent a link. -->

## Summary

<What changed and why, in a few sentences.>

## Changes

- `<path>`: <what and why>

## Acceptance criteria

| Criterion | Status | Evidence |
|---|---|---|
| <criterion> | met / not met / partly | `<file:line>`, `<test name>` |

## Checks

| Command | Result |
|---|---|
| `<command>` | passed / failed: <short output> / **not run**: <reason> |

Checks were run by the agent in this session; they are not CI results.

## Follow-ups

- <Separate problems found, not fixed here>

## Status

Ready for independent review. Not accepted.
