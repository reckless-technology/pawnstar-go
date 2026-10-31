package engine

import _ "embed"

// embeddedBook is the opening book (3,328 lines, 3,244 of them from the CC0-dedicated Lichess open
// database - see the file's own header and THIRD-PARTY-NOTICES.md) compiled into the binary via
// the go:embed directive below, so the engine always has a book with no external file to ship.
// Its single source of truth is engine/book.txt, which .gitattributes marks -text so no CRLF
// checkout can change the bytes this directive compiles in.
//
//go:embed book.txt
var embeddedBook string
