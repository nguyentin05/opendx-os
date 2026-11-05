# Post-change checklist

Run this after a related group of edits to check that the result is coherent.

1. Check the diff/changed content against the assigned scope. The contract has exactly one canonical definition; links, examples, personas, and consumers still match it.
2. Check parser/format validity and LF line endings for text files. Apply headers by file type per [Rule 04](../rules/04-pof-50-compliance.md); do not insert headers into every format.
3. Check architecture boundaries per [Rule 01](../rules/01-architecture-and-boundaries.md); distinguish domain literals in the runtime from illustrative fixtures/tests.
4. Check by phase and scope at [pre-pr-gate](pre-pr-gate.md). Where code exists, tests must check behavior/invariants rather than only matching the implementation's text.

Report clearly what was checked, what failed observably, and what was not run. Do not turn the absence of a future runner into a defect of the design.
