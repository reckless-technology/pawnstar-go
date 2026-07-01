package engine

import _ "embed"

// embeddedBook is the opening book (385 TSCP lines) compiled into the binary via go:embed, so the engine
// always has a book with no external file to ship. Its single source of truth is engine/book.txt.
//
//go:embed book.txt
var embeddedBook string
