package engine

// Color represents the side to move.
type Color uint8

const (
	White Color = 0
	Black Color = 1
)

func EnemyOf(c Color) Color {
	return c ^ 1
}
