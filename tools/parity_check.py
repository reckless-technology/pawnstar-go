#!/usr/bin/env python3
"""Check a fixed-depth, one-thread match between two engines that must play identical games.

parity_check.py PGN_DIR [--expect-games N] [--summary FILE]

Pawnstar-Go is bit-exact with Pawnstar (C++) at a fixed depth on one thread, so a depth-limited match between the two
from the same openings is deterministic. fastchess plays each opening twice with the colours reversed (`-games 2
-repeat`), and a game is a function of the two engines' (identical) searches, so:

  1. the two games of a colour-reversed pair must be identical: the same moves, and for every move the same score,
     depth and selective depth (the move comments fastchess writes). Node counts are compared too and reported, and fail
     the check only with --strict-nodes; the clock figures (time, nps) are never compared, being not deterministic;
  2. so the pair's results agree and each pair splits its points evenly, and the candidate's score over the whole
     match is exactly 50 %.

A pair is the games of one file with one Round tag. Exit status 0 only if every pair agrees, the count is the one
expected and the score is exactly half; otherwise the divergent pairs are listed, each at its first differing ply.
"""
import argparse
import glob
import os
import re
import sys

COMMENT = re.compile(r"\{([^}]*)\}")
CLOCK_FIGURES = re.compile(r"\b\d+(?:\.\d+)?s\b|\bnps=\d+")
NODES = re.compile(r"\bn=(\d+)")
RESULTS = ("1-0", "0-1", "1/2-1/2", "*")
MOVE_NUMBER = re.compile(r"^\d+\.(\.\.)?$")


def read_games(path):
    """Yield (headers, plies) for each game of one PGN file; a ply is (move, comment with the clock figures removed)."""
    with open(path, encoding="utf-8", newline=None) as handle:
        text = handle.read()
    headers, body, games = {}, [], []

    def finish():
        if headers or body:
            games.append((dict(headers), "\n".join(body)))
        headers.clear()
        body.clear()

    in_body = False
    for line in text.splitlines():
        if line.startswith("["):
            if in_body:
                finish()
                in_body = False
            match = re.match(r'\[(\w+) "(.*)"\]\s*$', line)
            if match:
                headers[match.group(1)] = match.group(2)
        elif line.strip():
            in_body = True
            body.append(line)
    finish()
    for headers, body in games:
        plies, position = [], 0
        for found in COMMENT.finditer(body):
            plies.extend((token, None, None) for token in tokens(body[position:found.start()]))
            if plies:
                comment = CLOCK_FIGURES.sub("", found.group(1))
                nodes = NODES.search(comment)
                plies[-1] = (plies[-1][0], " ".join(NODES.sub("", comment).split()), nodes and nodes.group(1))
            position = found.end()
        plies.extend((token, None, None) for token in tokens(body[position:]))
        yield headers, plies


def tokens(text):
    return [t for t in text.split() if not MOVE_NUMBER.match(t) and t not in RESULTS]


def first_difference(first, second, with_nodes=False):
    """The first ply at which the games differ in move, score, depth or selective depth (and node count if asked)."""
    cut = 3 if with_nodes else 2
    for ply, (a, b) in enumerate(zip(first, second), start=1):
        if a[:cut] != b[:cut]:
            return ply, a[:cut], b[:cut]
    if len(first) != len(second):
        return min(len(first), len(second)) + 1, None, None
    return None


def check(directory, expect_games=None, strict_nodes=False):
    """Returns (report lines, ok)."""
    paths = sorted(glob.glob(os.path.join(directory, "**", "*.pgn"), recursive=True))
    paths = [p for p in paths if os.path.basename(p) != "all.pgn"]  # the pool's concatenation repeats every game
    pairs, games, candidate_points = {}, 0, 0.0
    for path in paths:
        for headers, plies in read_games(path):
            games += 1
            pairs.setdefault((path, headers.get("Round", "?")), []).append((headers, plies))
            outcome = headers.get("Result")
            white_is_candidate = headers.get("White") == "candidate"
            if outcome == "1/2-1/2":
                candidate_points += 0.5
            elif (outcome == "1-0") == white_is_candidate and outcome in ("1-0", "0-1"):
                candidate_points += 1
    problems, agreeing, node_notes = [], 0, []
    for (path, round_tag), members in sorted(pairs.items()):
        label = f"{os.path.basename(path)} round {round_tag}"
        if len(members) != 2:
            problems.append(f"{label}: {len(members)} game(s), not a colour-reversed pair")
            continue
        (head_a, plies_a), (head_b, plies_b) = members
        if head_a.get("White") == head_b.get("White"):
            problems.append(f"{label}: both games have {head_a.get('White')} as White")
            continue
        if head_a.get("FEN") != head_b.get("FEN"):
            problems.append(f"{label}: the two games start from different positions")
            continue
        difference = first_difference(plies_a, plies_b, strict_nodes)
        if difference is not None:
            ply, a, b = difference
            problems.append(f"{label}: games diverge at ply {ply} (FEN {head_a.get('FEN')}): "
                            f"{head_a.get('White')} as White {a}  vs  {head_b.get('White')} as White {b}")
        elif head_a.get("Result") != head_b.get("Result"):
            problems.append(f"{label}: identical moves but results {head_a.get('Result')} and {head_b.get('Result')}")
        else:
            agreeing += 1
            other = first_difference(plies_a, plies_b, True)
            if other is not None:
                node_notes.append(f"{label}: same moves and scores, but node counts differ at ply {other[0]}: "
                                  f"{other[1][2]} vs {other[2][2]} (FEN {head_a.get('FEN')})")
    report = [f"games {games} in {len(pairs)} pairs from {len(paths)} file(s)",
              f"pairs identical in moves, scores and depths: {agreeing} of {len(pairs)}"]
    report += [f"NODE-COUNT {line}" for line in node_notes[:10]]
    if node_notes:
        report.append(f"{len(node_notes)} pair(s) differ in node count only (--strict-nodes makes that a failure)")
    if expect_games is not None and games != expect_games:
        problems.append(f"{games} games read, {expect_games} expected")
    if games and candidate_points * 2 != games:
        problems.append(f"the candidate scored {candidate_points} of {games} ({100 * candidate_points / games:.4f} %), "
                        "not exactly 50 %")
    elif games:
        report.append(f"candidate score {candidate_points} of {games}: exactly 50 %")
    if not games:
        problems.append("no games found")
    report += [f"DIVERGENCE {line}" for line in problems[:25]]
    if len(problems) > 25:
        report.append(f"... and {len(problems) - 25} more")
    return report, not problems


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("directory")
    parser.add_argument("--expect-games", type=int)
    parser.add_argument("--strict-nodes", action="store_true", help="a node-count difference fails too")
    parser.add_argument("--summary", help="append the report to this file (GITHUB_STEP_SUMMARY)")
    args = parser.parse_args(argv)
    report, ok = check(args.directory, args.expect_games, args.strict_nodes)
    print("\n".join(report))
    if args.summary:
        with open(args.summary, "a", encoding="utf-8") as handle:
            handle.write(f"### Parity: {'PASS' if ok else 'FAIL'}\n```\n" + "\n".join(report) + "\n```\n")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
