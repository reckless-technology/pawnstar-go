#!/usr/bin/env python3
"""Self-test of parity_check.py: it must pass identical colour-reversed pairs and fail each way they can differ."""
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import parity_check  # noqa: E402

FEN = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"
MOVES = [("e4", "+0.30/8 0.010s, n=1000, sd=12, nps=100000"), ("e5", "-0.30/8 0.011s, n=1100, sd=13, nps=100000")]


def game(white, black, result, moves=MOVES, round_tag="1", fen=FEN):
    lines = [f'[Round "{round_tag}"]', f'[White "{white}"]', f'[Black "{black}"]', f'[Result "{result}"]',
             f'[FEN "{fen}"]', "", ""]
    text = ""
    for index, (move, comment) in enumerate(moves):
        text += (f"{index // 2 + 1}. " if index % 2 == 0 else "") + f"{move} {{{comment}}} "
    return "\n".join(lines) + text + result + "\n\n"


def run(*pgn_texts, expect=None):
    with tempfile.TemporaryDirectory() as directory:
        for index, text in enumerate(pgn_texts):
            with open(os.path.join(directory, f"chunk-{index}.pgn"), "w", newline="\n") as handle:
                handle.write(text)
        return parity_check.check(directory, expect)


class ParityCheck(unittest.TestCase):
    def test_identical_pair_passes_whatever_the_clock(self):
        slower = [(move, comment.replace("0.010s", "0.900s").replace("nps=100000", "nps=7")) for move, comment in MOVES]
        report, ok = run(game("candidate", "baseline", "1/2-1/2") + game("baseline", "candidate", "1/2-1/2", slower))
        self.assertTrue(ok, report)

    def test_a_different_node_count_is_reported_and_fails_only_when_strict(self):
        other = [MOVES[0], (MOVES[1][0], MOVES[1][1].replace("n=1100", "n=1101"))]
        text = game("candidate", "baseline", "1/2-1/2") + game("baseline", "candidate", "1/2-1/2", other)
        report, ok = run(text)
        self.assertTrue(ok, report)
        self.assertTrue(any("NODE-COUNT" in line and "ply 2" in line for line in report), report)
        with tempfile.TemporaryDirectory() as directory:
            with open(os.path.join(directory, "chunk-0.pgn"), "w", newline="\n") as handle:
                handle.write(text)
            report, ok = parity_check.check(directory, None, True)
        self.assertFalse(ok)
        self.assertTrue(any("diverge at ply 2" in line for line in report), report)

    def test_a_different_move_fails(self):
        other = [MOVES[0], ("c5", MOVES[1][1])]
        report, ok = run(game("candidate", "baseline", "1/2-1/2") + game("baseline", "candidate", "1/2-1/2", other))
        self.assertFalse(ok)

    def test_a_different_score_fails(self):
        other = [MOVES[0], (MOVES[1][0], MOVES[1][1].replace("-0.30", "-0.31"))]
        report, ok = run(game("candidate", "baseline", "1/2-1/2") + game("baseline", "candidate", "1/2-1/2", other))
        self.assertFalse(ok)

    def test_a_different_depth_in_a_comment_fails(self):
        other = [(MOVES[0][0], MOVES[0][1].replace("/8", "/7")), MOVES[1]]
        report, ok = run(game("candidate", "baseline", "1/2-1/2") + game("baseline", "candidate", "1/2-1/2", other))
        self.assertFalse(ok)

    def test_a_score_not_exactly_half_fails_even_with_identical_moves(self):
        report, ok = run(game("candidate", "baseline", "1-0") + game("baseline", "candidate", "1-0"))
        self.assertTrue(ok, "a win each way is exactly half")
        report, ok = run(game("candidate", "baseline", "1-0") + game("baseline", "candidate", "1/2-1/2"))
        self.assertFalse(ok)

    def test_a_lone_game_is_not_a_pair(self):
        report, ok = run(game("candidate", "baseline", "1/2-1/2"))
        self.assertFalse(ok)

    def test_the_games_expected_are_counted(self):
        text = game("candidate", "baseline", "1/2-1/2") + game("baseline", "candidate", "1/2-1/2")
        self.assertTrue(run(text, expect=2)[1])
        self.assertFalse(run(text, expect=4)[1])

    def test_rounds_in_different_files_are_different_pairs(self):
        first = game("candidate", "baseline", "1/2-1/2") + game("baseline", "candidate", "1/2-1/2")
        report, ok = run(first, first)
        self.assertTrue(ok, report)

    def test_crlf_files_read_the_same(self):
        text = (game("candidate", "baseline", "1/2-1/2") + game("baseline", "candidate", "1/2-1/2")).replace("\n", "\r\n")
        with tempfile.TemporaryDirectory() as directory:
            with open(os.path.join(directory, "chunk-0.pgn"), "w", newline="") as handle:
                handle.write(text)
            self.assertTrue(parity_check.check(directory)[1])

    def test_no_games_fails(self):
        self.assertFalse(run("")[1])


if __name__ == "__main__":
    unittest.main()
