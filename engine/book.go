package engine

// OpeningBook is a stub; the Go port does not implement the opening book.
type OpeningBook struct{}

func (b *OpeningBook) GetMove(hash uint64) Move { return MoveNone() }
