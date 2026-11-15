# Original parser qualification fixtures

All fixtures here were authored for this repository under Apache-2.0.
`generate_fixtures.py` deterministically builds the PDFs without external input:
`table.pdf` is four text pages with a labelled approval table on page 4;
`scan.pdf` contains a generated 8×8 grayscale image and no text;
`too-many-pages.pdf` contains 201 text pages. `policy.md` has original English
and Vietnamese policy examples, a labelled table and code.

These fixtures qualify a narrow text/layout envelope, not general PDF or OCR
accuracy. The sandbox runner also probes source, decoded-output and deadline
bounds using these documents. No private or downloaded document is included.
