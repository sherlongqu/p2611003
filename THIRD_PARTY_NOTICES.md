# Third-party notices

p2611003 is released under the MIT License. It includes or depends on the
following third-party components.

## Noto Sans SC

The files under `assets/fonts/` are Noto Sans SC and are distributed under the
SIL Open Font License, Version 1.1. The license text is in
`assets/fonts/OFL.txt`.

## Mermaid

The bundled `static/mermaid.min.js` is Mermaid, distributed under the MIT
License.

Copyright (c) 2014-2026 Knut Sveidqvist and Mermaid contributors.

<https://github.com/mermaid-js/mermaid>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Go modules

The Go module dependencies and their versions are listed in `go.mod` and
`go.sum`. Their license texts are available from their respective module
repositories.

## CA certificates

`ca-certificates.crt` is a bundle of public root CA certificates derived from
the Mozilla CA program. It is included so the statically linked image can make
HTTPS requests for image localization and HTML inlining.
