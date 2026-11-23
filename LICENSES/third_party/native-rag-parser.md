# Native RAG parser dependencies

The Go Markdown adapter uses [goldmark 1.8.7](https://github.com/yuin/goldmark/tree/v1.8.7),
Copyright (c) 2019 Yusuke Inuzuka, under the MIT license. Its unmodified license
text is included in [THIRD_PARTY_LICENSES.txt](THIRD_PARTY_LICENSES.txt).

The separately built parser image installs Alpine 3.23
[`poppler-utils` 25.12.0-r0](https://pkgs.alpinelinux.org/packages?branch=v3.23&name=poppler-utils),
whose package license is GPL-2.0-or-later. Poppler is an independent executable
invoked by the parser. Consult the [upstream project](https://poppler.freedesktop.org/)
and its release source for copyright, license texts and corresponding source.
Any distributor of the container must retain the package notices and satisfy the
applicable source obligations for Poppler and its included dependencies. This
notice does not relicense Poppler under the repository's Apache-2.0 license.

The React testing additions are development-only: React Testing Library 16.3.3,
DOM Testing Library 10.4.2 (MIT), and jsdom 26.1.0 (MIT); their dependency licenses
are supplied in their installed packages. They are not bundled in the UI runtime.
