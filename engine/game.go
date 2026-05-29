package engine

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// startFen is the FEN string for the standard chess starting position.
const startFen = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"

// timeControl holds the clock configuration passed to the search goroutine.
type timeControl struct {
	clockType         clockType     // how time is managed (standard, fixed depth, fixed time, infinite)
	timeRemaining     time.Duration // time left on the clock for the side to move
	numMovesRemaining int           // moves until next time control (0 = sudden death)
	depth             int           // target depth for clockFixedDepth mode
	stopTime          time.Time     // hard deadline; search sets isCancelPending when exceeded
}

// newTimeControl returns a default timeControl (5-minute standard game, 40 moves).
func newTimeControl() timeControl {
	return timeControl{
		clockType:         clockStandard,
		timeRemaining:     5 * time.Minute,
		depth:             10,
		numMovesRemaining: 40,
	}
}

// Game is the top-level engine state.  It owns the transposition table, history
// table, opening book, chess clock, and a stack of positions for copy-make search.
// Create one with NewGame and drive it via HandleCommand.
type Game struct {
	transpositionTable transpositionTable // shared across all search goroutines
	historyTable       historyTable       // shared across all search goroutines
	timeControl        timeControl        // clock configuration for the current search
	evalTable          evalHashTable      // shared evaluation cache
	openingBook        openingBook        // pre-loaded book moves indexed by Zobrist hash
	nodeCount          int                // total nodes searched (main goroutine only)
	wg                 sync.WaitGroup     // waited on by stopThinking to join the search goroutine
	parallelSlots      chan struct{}      // semaphore: capacity = maxNumGoRoutines, limits parallel workers
	posStack           []position         // game history + current search path (main goroutine)
	isCancelPending    atomic.Bool        // set true to signal all goroutines to stop
}

// NewGame creates an engine instance ready to accept UCI commands.
func NewGame() *Game {
	slots := make(chan struct{}, maxNumGoRoutines)
	for range maxNumGoRoutines {
		slots <- struct{}{}
	}
	game := &Game{
		transpositionTable: newTranspositionTable(hashTableMB),
		timeControl:        newTimeControl(),
		openingBook:        newOpeningBookFromFile(),
		evalTable:          newEvalHashTable(evalHashTableSize),
		parallelSlots:      slots,
	}
	game.newGameFromFen(startFen)
	return game
}

// HandleCommand processes a single UCI command line.
// Returns true if the engine should quit.
// This is the public interface to the game engine.
func (game *Game) HandleCommand(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	fields := strings.Fields(line)
	switch fields[0] {
	case "uci":
		fmt.Println("id name Pawnstar-Go")
		fmt.Println("id author Jonny Reckless")
		fmt.Println("uciok")
	case "isready":
		fmt.Println("readyok")
	case "ucinewgame":
		game.stopThinking()
		game.newGameFromFen(startFen)
	case "position":
		game.handleUciPosition(fields[1:])
	case "go":
		game.handleUciGo(fields[1:])
	case "stop":
		game.stopThinking()
	case "quit":
		game.stopThinking()
		return true
	}
	return false
}

// newGameFromFen resets the engine state to the position described by fen.
func (game *Game) newGameFromFen(fen string) {
	game.posStack = make([]position, 0, 512)
	game.posStack = append(game.posStack, positionFromFen(fen))
	game.nodeCount = 0
	game.isCancelPending.Store(false)
	game.timeControl = newTimeControl()
}

// currentPosition returns a pointer to the current (last) position on the stack.
func (game *Game) currentPosition() *position {
	return &game.posStack[len(game.posStack)-1]
}

// playMove appends a copy of the current position to the stack and applies m to it.
func (game *Game) playMove(m move) {
	game.posStack = append(game.posStack, game.posStack[len(game.posStack)-1])
	pos := &game.posStack[len(game.posStack)-1]
	pos.makeMove(m)
}

// playMoveString finds the legal move matching UCI string s, plays it, and returns it.
// Returns moveNone if s does not match any legal move.
func (game *Game) playMoveString(s string) move {
	moves := game.currentPosition().generateLegalMoves()
	for _, move := range moves {
		if move.String() == s {
			game.playMove(move)
			return move
		}
	}
	return moveNone()
}

// pieceVals maps piece type to centipawn value, used in move scoring (MVV-LVA).
var pieceVals = [7]int{0, 100, 300, 300, 500, 900, 10000}

// scoreAndSortMovesInner assigns sort scores and sorts moves.  It is shared by
// searchState (with per-ply killers) and the root search (zero killers).
// Captures and promotions are scored via SEE; quiet moves use an enemy-attack
// penalty plus killer bonuses and history.
func scoreAndSortMovesInner(pos *position, history *historyTable, killers [2]move, moves []move, ply int) {
	occupied := pos.occupied()
	color := pos.colorToMove
	enemyColor := enemyOf(color)
	enemyPieces := pos.piecesOfColor(enemyColor)
	enemyPawns := enemyPieces & pos.pawns
	var enemyAttacks bitboard
	if color == white {
		enemyAttacks = enemyPawns.shiftSouthwest() | enemyPawns.shiftSoutheast()
	} else {
		enemyAttacks = enemyPawns.shiftNorthwest() | enemyPawns.shiftNortheast()
	}
	nonPawnEnemy := enemyPieces &^ enemyPawns
	for nonPawnEnemy != 0 {
		sq := nonPawnEnemy.popLsb()
		enemyAttacks |= pieceAttacks(pos.squares[sq], occupied, sq)
	}
	for i, m := range moves {
		piece := m.piece()
		var score int
		if m.captured() != noPiece || m.promoted() != noPiece {
			// SEE replaces MVV-LVA for captures and promotions.
			seeScore, _ := evaluateStaticExchange(pos, m)
			score = seeScore * 1000
		} else {
			// Quiet move: penalise walking into an enemy attack; boost killers.
			if (enemyAttacks & squareBB(m.to())).isNotEmpty() {
				score -= pieceVals[piece] * 1000
			}
			if !killers[0].isNone() && killers[0].equal(m) {
				score += killerScore0
			} else if !killers[1].isNone() && killers[1].equal(m) {
				score += killerScore1
			}
		}
		score += int(history.getCount(ply, m))
		score += pstValue(color, piece, m.to()) - pstValue(color, piece, m.from())
		moves[i] = m.withScore(score)
	}
	sortMovesUnstable(moves)
}

// startThinking launches a search goroutine for the current position and time
// control.  Any previously running search is stopped first.
func (game *Game) startThinking() {
	game.stopThinking()
	game.isCancelPending.Store(false)
	game.wg.Add(1)
	go func() {
		game.searchEntry()
		game.wg.Done()
	}()
}

// stopThinking signals the search goroutine to halt and blocks until it exits.
func (game *Game) stopThinking() {
	game.isCancelPending.Store(true)
	game.wg.Wait()
	game.isCancelPending.Store(false)
}

// searchEntry is the body of the search goroutine.  It runs the search, plays the
// best move on the internal position, and prints the UCI bestmove response.
func (game *Game) searchEntry() {
	m := searchRootNode(game)
	if m != moveNone() {
		game.playMove(m)
		fmt.Println("bestmove " + m.String())
	} else {
		fmt.Println("bestmove 0000")
	}
}

// handleUciPosition handles the "position" command, setting the position from a FEN
// string or the standard starting position, then applying any supplied move list.
func (game *Game) handleUciPosition(args []string) {
	fen := startFen
	movesIdx := -1
	if len(args) > 0 {
		switch args[0] {
		case "startpos":
			for i, a := range args {
				if a == "moves" {
					movesIdx = i + 1
					break
				}
			}
		case "fen":
			i := 1
			for i < len(args) && args[i] != "moves" {
				i++
			}
			fen = strings.Join(args[1:i], " ")
			if i < len(args) {
				movesIdx = i + 1
			}
		}
	}
	game.stopThinking()
	game.newGameFromFen(fen)
	if movesIdx >= 0 {
		for _, mv := range args[movesIdx:] {
			game.playMoveString(mv)
		}
	}
}

// handleUciGo handles the "go" command, parsing the clock parameters and starting the search.
func (game *Game) handleUciGo(args []string) {
	tc := &game.timeControl
	color := game.currentPosition().colorToMove
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "wtime":
			if color == white {
				tc.clockType = clockStandard
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					tc.timeRemaining = time.Duration(v) * time.Millisecond
				}
			}
			i++
		case "btime":
			if color == black {
				tc.clockType = clockStandard
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					tc.timeRemaining = time.Duration(v) * time.Millisecond
				}
			}
			i++
		case "movestogo":
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				tc.numMovesRemaining = v
			}
			i++
		case "depth":
			tc.clockType = clockFixedDepth
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				tc.depth = v
			}
			i++
		case "movetime":
			tc.clockType = clockFixedTime
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				tc.timeRemaining = time.Duration(v) * time.Millisecond
			}
			i++
		case "infinite":
			tc.clockType = clockInfinite
		}
	}
	game.startThinking()
}
