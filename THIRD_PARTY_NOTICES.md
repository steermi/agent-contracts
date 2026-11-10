# Third-party notices

This module's source is original work licensed under Apache-2.0. It depends on
the following modules, which are downloaded by the Go toolchain and keep their
own licenses. Nothing from them is copied into this repository.

| Module | Used by | License |
|---|---|---|
| `golang.org/x/text` | `locallaunch` (Unicode normalization for branch names) | BSD-3-Clause |
| `github.com/stretchr/testify` | tests only | MIT |
| `github.com/davecgh/go-spew` | tests only (via testify) | ISC |
| `github.com/pmezard/go-difflib` | tests only (via testify) | BSD-3-Clause |
| `gopkg.in/yaml.v3` | `skill` (SKILL.md front matter); also tests via testify | MIT and Apache-2.0 |

Regenerate the list after a dependency change with
`go list -m all` and each module's `LICENSE` file.
