# Compatibility

Two programs compile against this module: the hosted Steermi service and the
`steermi` CLI. They are released independently, so a developer may run an
older CLI against a newer service. The rules below keep that safe.

## What is a contract

| Surface | Where it is locked |
|---|---|
| JSON field names of `Run`, `Command`, `OutputRef`, `Check`, `Decision` | `testdata/compat/wire.json` |
| `Digest` / `Run.EvidenceDigest` output for the same input | `testdata/compat/wire.json` |
| Commands an API caller may send (`CommandType.Public`) | `testdata/compat/wire.json` |
| Capability allowlist per runner and scope, and for meetings | `testdata/compat/capabilities.json` |
| Runner scopes, locations and identity digest | `testdata/compat/definitions.json` |
| Built-in skill digests and the default flow | `testdata/compat/skills_flows.json` |
| Launch IDs, secret hashing, PKCE S256, branch names | `testdata/compat/locallaunch.json` and RFC 7636 vector in `compat_test.go` |
| Authorization boundaries (stale input, self-acceptance, verified checks, generations) | `TestCompatBoundaries` in `compat_test.go` |

`go test ./...` fails when any of these change.

## Changing a contract

1. Make the change and run `make compat-update` to rewrite the golden files.
2. Review the diff of `testdata/compat`. Every changed line is a behavior
   change for both consumers.
3. Record it under **Unreleased** in `CHANGELOG.md`: what changed, which
   consumer must upgrade first, and whether older CLIs keep working.
4. Prefer additive changes: a new optional JSON field, a new capability
   granted to fewer callers, a new command that older clients never send.

Changes that need care:

- **Digests.** `Digest` hashes the JSON encoding. Adding a field to a struct
  that is digested (the evidence tuple) changes every digest, which
  invalidates pending human decisions. Treat it as a breaking change.
- **Capabilities.** Removing a tool from an allowlist is safe for security but
  may break a workflow; adding one widens what an agent can do and needs a
  security review.
- **Prompts.** Run prompts live in the built-in skills
  (`skill/builtin`); `skills_flows.json` stores their digests. A prompt
  change is allowed but must be deliberate: bump `builtin.Version` and add a
  `builtin.Changelog` line (shown to organizations that duplicated it).

## Release order

The service and CLI pin an exact version. Release this module first, then
upgrade and release the service, then the CLI. A change that the service must
understand before the CLI sends it ships in the service first.
