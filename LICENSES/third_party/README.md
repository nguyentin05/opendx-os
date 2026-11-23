# Third-Party License Texts

`THIRD_PARTY_LICENSES.txt` reproduces the license and NOTICE texts of every
third-party Go module linked into the project's binaries (and the Go standard
library) and of the npm packages bundled into the Workspace UI. It is generated
by `scripts/third_party_notices.py`; regenerate it after changing `go.mod`,
`go.sum` or `workspace-ui/package-lock.json`, and update the inventory in
`../../COPYRIGHT.txt`. `python3 scripts/third_party_notices.py --check` fails
when the file is stale.

[`native-rag-parser.md`](native-rag-parser.md) covers the Poppler executables in
the parser image.
