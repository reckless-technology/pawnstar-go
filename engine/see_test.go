package engine

import "testing"

// SEE values: P=100, N=300, B=300, R=500, Q=900, K=10000.

func TestSEE(t *testing.T) {
	tests := []struct {
		name         string
		fen          string
		move         move
		wantScore    int
		wantChecking bool
	}{
		// ── C reference cases ─────────────────────────────────────────────────

		{
			// Baseline: rook takes an undefended pawn.  No recaptures possible,
			// so SEE just returns the captured piece's value.
			name:      "rook takes pawn, no defenders",
			fen:       "1k1r4/1pp4p/p7/4p3/8/P5P1/1PP4P/2K1R3 w - - 0 1",
			move:      moveCapture(e1, e5, rook, pawn),
			wantScore: 100,
		},
		{
			// Five-piece exchange that exercises the full LVA ordering and the
			// stand-pat logic.  Defenders of e5: Bf6 (LVA), Nd7, Qh8 via Bf6
			// X-ray (becomes visible once the bishop moves), and for white Re2
			// and Qe1.  Unwinding: white's queen correctly stands pat rather
			// than recapturing into a losing queen-for-queen exchange, so the
			// net gain is pawn(100) − bishop(300) = −200.
			name:      "knight takes pawn, loses to defenders",
			fen:       "1k1r3q/1ppn3p/p4b2/4p3/8/P2N2P1/1PP1R1BP/2K1Q3 w - - 0 1",
			move:      moveCapture(d3, e5, knight, pawn),
			wantScore: -200,
		},

		// ── Undefended captures ───────────────────────────────────────────────

		{
			// Simplest non-trivial case: attacker worth more than target, but no
			// recapture is possible.  The king is placed on a8, not h8, because
			// moving to d4 opens the c3–h8 diagonal — a king on h8 would trigger
			// an unexpected isChecking=true.
			name:      "bishop takes undefended knight",
			fen:       "k7/8/8/8/3n4/2B5/8/K7 w - - 0 1",
			move:      moveCapture(c3, d4, bishop, knight),
			wantScore: 300,
		},

		// ── Equal and winning exchanges ───────────────────────────────────────

		{
			// Stand-pat fires correctly: the defending pawn recaptures our pawn,
			// then we have no further attackers.  Net = pawn(100) − pawn(100) = 0.
			// Verifies that equal exchanges score 0 rather than the raw captured value.
			name:      "equal exchange: pawn takes pawn defended by pawn",
			fen:       "7k/8/4p3/3p4/4P3/8/8/K7 w - - 0 1",
			move:      moveCapture(e4, d5, pawn, pawn),
			wantScore: 0,
		},
		{
			// Same position as the equal exchange above but the target is a knight
			// instead of a pawn.  Confirms the value difference is reflected:
			// knight(300) − pawn(100) = +200.
			name:      "pawn takes knight defended only by pawn",
			fen:       "7k/8/4p3/3n4/4P3/8/8/K7 w - - 0 1",
			move:      moveCapture(e4, d5, pawn, knight),
			wantScore: 200,
		},
		{
			// Attacker worth less than target so the exchange is profitable.
			// b3→d5 is a valid diagonal (Δfile=+2, Δrank=+2); the e6 bishop
			// defends d5 along the other diagonal.  Net = rook(500) − bishop(300) = +200.
			name:      "bishop takes rook defended only by bishop",
			fen:       "7k/8/4b3/3r4/8/1B6/8/K7 w - - 0 1",
			move:      moveCapture(b3, d5, bishop, rook),
			wantScore: 200,
		},

		// ── Losing captures ───────────────────────────────────────────────────

		{
			// Classic "hanging rook" test: taking a pawn when the opponent's rook
			// can recapture is a clear material loss.
			// Net = pawn(100) − rook(500) = −400.
			name:      "rook takes pawn defended by rook",
			fen:       "2rk4/8/8/2p5/8/8/8/2RK4 w - - 0 1",
			move:      moveCapture(c1, c5, rook, pawn),
			wantScore: -400,
		},
		{
			// King is the sole defender.  The king has the lowest priority in LVA
			// ordering (it captures last), but it still recaptures when it is the
			// only piece available.  Net = pawn(100) − knight(300) = −200.
			name:      "knight takes pawn defended by king",
			fen:       "8/8/8/4kp2/8/4N3/8/K7 w - - 0 1",
			move:      moveCapture(e3, f5, knight, pawn),
			wantScore: -200,
		},
		{
			// Overpaying for a pawn with the queen.  a2→d5 is a valid diagonal
			// (Δfile=+3, Δrank=+3); the h5 queen defends d5 along rank 5.
			// Net = pawn(100) − queen(900) = −800.
			name:      "queen takes pawn defended by queen",
			fen:       "7k/8/8/3p3q/8/8/Q7/K7 w - - 0 1",
			move:      moveCapture(a2, d5, queen, pawn),
			wantScore: -800,
		},

		// ── X-ray attacker ────────────────────────────────────────────────────

		{
			// Core algorithmic test for X-ray handling.  The bishop on b3 is
			// invisible while the pawn sits on c4.  Once the pawn captures on d5,
			// c4 is vacated and the bishop becomes a second-wave attacker; it
			// recaptures the defending pawn for free.  Without correct X-ray
			// support the score would be 900−100=800; the correct answer is 900.
			name:      "x-ray: pawn takes queen, bishop revealed behind",
			fen:       "7k/8/4p3/3q4/2P5/1B6/8/K7 w - - 0 1",
			move:      moveCapture(c4, d5, pawn, queen),
			wantScore: 900,
		},

		// ── Multi-piece exchange ──────────────────────────────────────────────

		{
			// Three-piece sequence with stand-pat in the middle.  The rook×rook
			// exchange cancels; white bishop then recaptures the black rook.
			// Black has no further attackers so the sequence ends.
			// Net = pawn(100) + rook(500) − rook(500) = +100.
			name:      "rook+bishop exchange over a pawn",
			fen:       "4r1k1/8/8/4p3/3B4/8/8/4R1K1 w - - 0 1",
			move:      moveCapture(e1, e5, rook, pawn),
			wantScore: 100,
		},

		// ── Promotion captures ────────────────────────────────────────────────

		{
			// Exercises the promotion formula:
			//   SEE = captured(500) + promoted(900) − pawn(100) − swapOff = +1300.
			// The black king is placed on f5, which lies on no rank, file, or
			// diagonal through b8, so there is no check and no recapture.
			name:      "promotion: pawn captures rook, promotes to queen, undefended",
			fen:       "1r6/P7/8/5k2/8/8/8/K7 w - - 0 1",
			move:      movePromotion(a7, b8, queen, rook),
			wantScore: 1300,
		},
		{
			// Same promotion score (+1300), but the promoted queen on h8 attacks
			// the black king on e5 along the h8–e5 diagonal (Δfile=−3, Δrank=−3),
			// so isChecking must be true.  Verifies that the check flag is derived
			// from the promoted piece's attack pattern, not the moving pawn's.
			name:         "promotion: pawn captures rook, promotes to queen, gives check",
			fen:          "7r/6P1/8/4k3/8/8/8/K7 w - - 0 1",
			move:         movePromotion(g7, h8, queen, rook),
			wantScore:    1300,
			wantChecking: true,
		},

		// ── Check detection ───────────────────────────────────────────────────

		{
			// Non-promotion check: after Ra1×a7 the rook attacks the king on h7
			// along rank 7.  Score is simply the undefended pawn (+100).  Together
			// with the promotion check case above, both paths through isChecking
			// are exercised independently.
			name:         "rook takes pawn, checks king on same rank",
			fen:          "8/p6k/8/8/8/8/8/RK6 w - - 0 1",
			move:         moveCapture(a1, a7, rook, pawn),
			wantScore:    100,
			wantChecking: true,
		},

		// ── Additional promotion cases ───────────────────────────────────────────

		{
			// Promotion followed by a recapture chain: g7 captures the h8 rook
			// and promotes to queen.  The black rook on a8 recaptures the queen
			// (gain 900), then the white rook on h1 recaptures the black rook
			// (gain 500), then no further pieces remain.
			// evaluateSwapOff = 900 − 500 = 400.
			// SEE = rook(500) + queen(900) − pawn(100) − 400 = +900.
			name:      "promotion with recapture chain",
			fen:       "r6r/6P1/8/3k4/8/8/8/K6R w - - 0 1",
			move:      movePromotion(g7, h8, queen, rook),
			wantScore: 900,
		},
		{
			// Underpromotion to knight capturing a bishop; no defenders.
			// King is placed on a8 so it is not on any rank, file, or diagonal
			// through h8, and the promoted knight on h8 does not attack a8.
			// SEE = bishop(300) + knight(300) − pawn(100) = +500.
			name:      "underpromotion: pawn captures bishop, promotes to knight",
			fen:       "k6b/6P1/8/8/8/8/8/K7 w - - 0 1",
			move:      movePromotion(g7, h8, knight, bishop),
			wantScore: 500,
		},

		// ── Long recapture sequences ──────────────────────────────────────────

		{
			// Three white rooks vs two black rooks, all on the e-file.  White
			// initiates by taking the black pawn on e5.  The rook exchanges cancel
			// pairwise (wR−bR−wR−bR); white's extra rook recaptures last with no
			// further opposition.  evaluateSwapOff = 0 (all rook gains cancel).
			// SEE = pawn(100) − 0 = +100.
			name:      "3-rook chain: white has extra rook, gains the pawn",
			fen:       "4r2k/4r3/8/4p3/8/4R3/4R3/K3R3 w - - 0 1",
			move:      moveCapture(e1, e5, rook, pawn),
			wantScore: 100,
		},
		{
			// Five-piece sequence with stand-pat at two levels.
			// White pawn (d4) takes black rook (e5).  Defenders: bP(d6), bN(d3),
			// bB(f6).  White backup: wN(c4), wB(f4).
			//
			// Forward pass (LVA order):
			//   ply 0: bP takes wP  (gain 100)
			//   ply 1: wN takes bP  (gain 100)  ← wN sees bN+bB still to come
			//   ply 2: bN takes wN  (gain 300)
			//   ply 3: wB takes bN  (gain 300)  ← wB sees bB still to come
			//   ply 4: bB takes wB  (gain 300)
			//
			// Unwind: wB stands pat at ply 3 (300−300=0); wN stands pat at
			// ply 1 (100−300<0 → 0).  evaluateSwapOff returns 100.
			// SEE = rook(500) − 100 = +400.
			name:      "5-piece exchange with stand-pat at two levels",
			fen:       "7k/8/3p1b2/4r3/2NP1B2/3n4/8/K7 w - - 0 1",
			move:      moveCapture(d4, e5, pawn, rook),
			wantScore: 400,
		},

		// ── En passant captures ───────────────────────────────────────────────
		{
			name:      "en passant: wins an undefended pawn",
			fen:       "7k/8/8/3Pp3/8/8/8/K7 w - e6 0 1",
			move:      moveEpCapture(d5, e6),
			wantScore: 100,
		},
		{
			name:      "en passant: equal, recaptured by an adjacent pawn",
			fen:       "7k/5p2/8/3Pp3/8/8/8/K7 w - e6 0 1",
			move:      moveEpCapture(d5, e6),
			wantScore: 0,
		},

		// ── Battery / revealed attacker ───────────────────────────────────────
		{
			// Rook in front of the queen: after the rook trade the queen recaptures.
			name:      "battery: rook in front of queen wins the pawn after the trade",
			fen:       "4r2k/8/8/4p3/8/8/4R3/K3Q3 w - - 0 1",
			move:      moveCapture(e2, e5, rook, pawn),
			wantScore: 100,
		},
		{
			// Bishop fronts a rook on the d-file; after Bxd5 nxd5, Rxd5 collects the pawn.
			name:      "bishop+rook battery beats a pawn-defended knight",
			fen:       "7k/8/4p3/3n4/8/8/6B1/K2R4 w - - 0 1",
			move:      moveCapture(g2, d5, bishop, knight),
			wantScore: 100,
		},

		// ── Losing capture that gives check ───────────────────────────────────
		{
			name:         "queen grabs a king-defended pawn, loses the queen but gives check",
			fen:          "k7/p7/8/8/8/8/8/Q6K w - - 0 1",
			move:         moveCapture(a1, a7, queen, pawn),
			wantScore:    -800,
			wantChecking: true,
		},

		// ── Cross-color pawn recapture: two attackers vs one defender ─────────
		{
			// White answers black's e6xd5 recapture with c4xd5 — the recapturing
			// pawns are different colors, which the swap-off must follow across plies.
			name:      "two pawn attackers vs one defender wins a pawn",
			fen:       "7k/8/4p3/3p4/2P1P3/8/8/K7 w - - 0 1",
			move:      moveCapture(e4, d5, pawn, pawn),
			wantScore: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos := positionFromFen(tt.fen)
			gotScore, gotChecking := evaluateStaticExchange(&pos, tt.move)
			if gotScore != tt.wantScore {
				t.Errorf("score: got %d, want %d", gotScore, tt.wantScore)
			}
			if gotChecking != tt.wantChecking {
				t.Errorf("givesCheck: got %v, want %v", gotChecking, tt.wantChecking)
			}
		})
	}
}

// seeRefBoardLVA finds the least-valuable attacker of sq by color on a seeBoard, re-derived from the
// current occupancy each call. Returns its from-square and piece type. Independent of evaluateSwapOff's
// incremental bookkeeping (and it selects the pawn source per call, so it catches the per-ply bug).
func seeRefBoardLVA(bb *seeBoard, sq square, color color) (square, piece, bool) {
	occ := bb.colors[white] | bb.colors[black]
	friendly := bb.colors[color]
	pawnSrc := pawnAttacksWhite[sq]
	if color == white {
		pawnSrc = pawnAttacksBlack[sq]
	}
	for _, o := range [...]struct {
		pc piece
		t  bitboard
	}{
		{pawn, pawnSrc}, {knight, knightAttacks[sq]},
		{bishop, bishopAttacks(occ, sq)}, {rook, rookAttacks(occ, sq)},
		{queen, bishopAttacks(occ, sq) | rookAttacks(occ, sq)}, {king, kingAttacks[sq]},
	} {
		if atk := o.t & friendly & bb.pieces[o.pc]; atk.isNotEmpty() {
			return atk.lsb(), o.pc, true
		}
	}
	return 0, noPiece, false
}

// seeReferenceSwapOff is a recursive negamax swap-off over a seeBoard (bb is taken by value, so each
// recursion gets its own copy — no undo). It is the independent cross-check for evaluateSwapOff, whose
// forward-pass-then-unwind it must match exactly.
func seeReferenceSwapOff(bb seeBoard, sq square, color color, onSquare piece) int {
	from, attacker, ok := seeRefBoardLVA(&bb, sq, color)
	if !ok {
		return 0 // no attacker: the side to move declines, gaining nothing
	}
	landed := attacker
	sqBB := squareBB(sq)
	if attacker == pawn && (sqBB & (rank1 | rank8)).isNotEmpty() {
		landed = queen // pawn reaching the back rank promotes
	}
	fromBB := squareBB(from)
	bb.pieces[onSquare] ^= sqBB       // remove the captured piece
	bb.colors[enemyOf(color)] ^= sqBB //
	bb.pieces[attacker] ^= fromBB     // lift the attacker off its square
	bb.colors[color] ^= fromBB | sqBB // and land it on sq
	bb.pieces[landed] ^= sqBB         //
	net := seeValues[onSquare] - seeReferenceSwapOff(bb, sq, enemyOf(color), landed)
	if net < 0 {
		return 0 // stand pat rather than recapture into a loss
	}
	return net
}

// seeReference computes SEE independently of evaluateStaticExchange (recursive negamax swap-off) and is
// used only to cross-check it.
func seeReference(pos *position, m move) (int, bool) {
	child := *pos
	child.makeMove(m)
	isChecking := child.isInCheck()
	captured := seeValues[m.captured()]
	if m.kind() == epCapture {
		captured = seeValues[pawn]
	}
	landed := m.piece()
	gain := captured
	if p := m.promoted(); p != noPiece {
		gain += seeValues[p] - seeValues[pawn]
		landed = p
	}
	return gain - seeReferenceSwapOff(newSeeBoard(&child), m.to(), child.colorToMove, landed), isChecking
}

// crossCheckSee walks every capture/promotion in the depth-bounded move tree from pos and asserts the
// fast evaluateStaticExchange matches the recursive seeReference — score AND give-check flag. Ports the
// C++ CrossCheckSee: exercises en passant, promotion captures, and discovered/promotion/ep checks.
func crossCheckSee(t *testing.T, pos *position, depth int) int {
	checks := 0
	for _, m := range pos.generateLegalMoves() {
		if m.captured() != noPiece || m.promoted() != noPiece {
			checks++
			fastScore, fastChk := evaluateStaticExchange(pos, m)
			refScore, refChk := seeReference(pos, m)
			if fastScore != refScore || fastChk != refChk {
				t.Errorf("SEE mismatch on %s (fen %s): fast {%d,%v} ref {%d,%v}",
					m.String(), pos.toFenString(), fastScore, fastChk, refScore, refChk)
			}
		}
		if depth > 0 {
			child := *pos
			child.makeMove(m)
			checks += crossCheckSee(t, &child, depth-1)
		}
	}
	return checks
}

// TestSEECrossCheck verifies the fast SEE against the independent recursive reference over the capture/
// promotion/en-passant-rich standard positions used by the C++ test (startpos, Kiwipete, a perft ep
// position, perft pos4, and a promotion-rich position), to depth 3.
func TestSEECrossCheck(t *testing.T) {
	fens := []string{
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
		"r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1",
		"n1n5/PPPk4/8/8/8/8/4Kppp/5N1N b - - 0 1",
	}
	depth := 3
	if testing.Short() {
		depth = 2
	}
	total := 0
	for _, fen := range fens {
		pos := positionFromFen(fen)
		total += crossCheckSee(t, &pos, depth)
	}
	t.Logf("cross-checked fast SEE == reference over %d captures/promotions", total)
}
