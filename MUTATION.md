# Residual mutants (gremlins v0.6.0, `make mutation`, measured 2026-11-15)

These mutants cannot be killed or covered by a test. They are not silenced in
`.gremlins.yaml`; the `cart` package carries them, which is why the gates sit
at 98/98 and not at the fleet's 99/99.

| Package | File:line | Mutator | Status | Why |
| --- | --- | --- | --- | --- |
| cart | `delivery.go:157:80` (`setSelection` sort) | CONDITIONALS_BOUNDARY (`<` to `<=`) | LIVED, equivalent | `setSelection` replaces a selection with the same group number, so group numbers in `c.selections` are unique; `sort.Slice` never compares an element with itself, so `<` and `<=` order a set of distinct keys identically. |
| cart | `requote.go:39:80` (`RequoteDelivery` sort) | CONDITIONALS_BOUNDARY (`<` to `<=`) | LIVED, equivalent | The groups come from a map keyed by group number, so the keys are unique: same argument. |
| cart | `lifecycle.go:15:22`, `lifecycle.go:15:27` (`IdleAfter = 30 * 24 * time.Hour`) | ARITHMETIC_BASE | NOT COVERED, artefact | A `const` initialiser is evaluated at compile time, so the cover profile has no statement for it. `IdleSince`, which uses it, is tested at the exact 30-day boundary. |

`mergedLines` (`lifecycle.go`) used a tagless `switch { case ...: }`, which the
Go cover tool does not instrument (4 NOT COVERED mutants); it is now an
if-chain with the same behaviour.
