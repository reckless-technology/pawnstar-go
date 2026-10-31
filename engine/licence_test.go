package engine

// The `license` UCI command and the notice it prints. The notice is an obligation of distributing the
// embedded network, so it is gated rather than left to inspection: the text is asserted, and asserted
// against THIRD-PARTY-NOTICES.md, which it is the condensed form of.

import (
	"os"
	"strings"
	"testing"
)

// licenceAttributionLines are the two ODbL attribution lines, one per run behind v20, as the extractor
// that read the archive writes them into every training shard's provenance sidecar. They are the
// notice: they must appear, unaltered, in the printed form and in THIRD-PARTY-NOTICES.md alike.
var licenceAttributionLines = []string{
	"Position data from the Leela Chess Zero self-play training data " +
		"(https://storage.lczero.org/files/training_data/), run test91, used under the " +
		"Open Database License 1.0 (http://opendatacommons.org/licenses/odbl/1.0/).",
	"Position data from the Leela Chess Zero self-play training data " +
		"(https://storage.lczero.org/files/training_data/), run test60, used under the " +
		"Open Database License 1.0 (http://opendatacommons.org/licenses/odbl/1.0/).",
}

// collapse reduces text to one space-separated string, so a claim can be asserted whatever the line it
// happens to be wrapped at: both the printed notice and the markdown file wrap for width, and the file
// wraps the attribution inside a blockquote, whose leading "> " is markup rather than text.
func collapse(text string) string {
	var words []string
	for _, line := range strings.Split(text, "\n") {
		words = append(words, strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), ">"))...)
	}
	return strings.Join(words, " ")
}

// TestLicenceCommandPrintsTheNotice: `license` prints the engine's licence and every third-party
// notice the shipped data obliges, and quits nothing.
func TestLicenceCommandPrintsTheNotice(t *testing.T) {
	game := NewGame()
	out := captureStdout(t, func() {
		if game.HandleCommand("license") {
			t.Error("`license` asked the engine to quit")
		}
	})
	flat := collapse(out)
	wants := []string{
		"GNU General Public License, version 3",
		"NO WARRANTY",
		"THIRD-PARTY-NOTICES.md",
		"trained entirely on Leela Chess Zero self-play training data",
		"test91 (62.83% of the pool) and test60 (37.17%)",
		"Database Contents License 1.0 (http://opendatacommons.org/licenses/dbcl/1.0/)",
		`ODbL "Produced Work": the obligation is this notice`,
		"ODbL does not require the network itself to be ODbL-licensed",
		"not GPL merely because GPLv3 code trained it",
		"PlentyChess, by Yoshie2000",
		"no third-party code dependencies",
		// The opening book's provenance, which is the one claim in this notice that used to be WRONG:
		// the book was derived from TSCP's book.txt and called public domain, and TSCP is Copyright
		// 1997 Tom Kerrigan under an informal non-commercial permission - GPL-incompatible, and not a
		// dedication at all. The book is now built from the Lichess open database, whose CC0
		// dedication makes the claim true. Both halves are pinned: that the new source and its
		// dedication are named, and (below) that the old claim is gone.
		"Lichess open database",
		"https://database.lichess.org/",
		"CC0 1.0",
		"https://creativecommons.org/publicdomain/zero/1.0/",
		// CC0 asks for no attribution at all, so the notice must not imply that this port owes one:
		// overstating an obligation is its own kind of false provenance claim, and this check is what
		// stops a later edit quietly turning the courtesy back into a duty.
		"CC0 waives attribution",
	}
	for _, want := range append(wants, licenceAttributionLines...) {
		if !strings.Contains(flat, collapse(want)) {
			t.Errorf("`license` did not print %q", want)
		}
	}
	// A notice that still said "TSCP" would be the exact defect this replaced.
	for _, unwanted := range []string{"TSCP", "Kerrigan"} {
		if strings.Contains(flat, unwanted) {
			t.Errorf("`license` still claims %q as the book's provenance", unwanted)
		}
	}
}

// TestLicenceNoticeAgreesWithTheNoticesFile: the printed notice is the condensed form of
// THIRD-PARTY-NOTICES.md, so the claims that make up the obligation must appear in both. Editing one
// and not the other is the failure this guards - a shipped binary that attributes differently from the
// file it points at is worse than either alone.
func TestLicenceNoticeAgreesWithTheNoticesFile(t *testing.T) {
	raw, err := os.ReadFile("../THIRD-PARTY-NOTICES.md")
	if err != nil {
		t.Fatalf("read THIRD-PARTY-NOTICES.md: %v", err)
	}
	file := collapse(string(raw))
	printed := collapse(strings.Join(licenceNotice, "\n"))
	for _, want := range append(licenceAttributionLines,
		"https://storage.lczero.org/files/training_data/",
		"Open Database License 1.0",
		"Database Contents License 1.0",
		"test91",
		"test60",
		"62.83%",
		"37.17%",
		"Produced Work",
		"Yoshie2000",
		"Lichess open database",
		"https://database.lichess.org/",
		"CC0 1.0",
		"https://creativecommons.org/publicdomain/zero/1.0/",
	) {
		flat := collapse(want)
		if !strings.Contains(file, flat) {
			t.Errorf("THIRD-PARTY-NOTICES.md does not carry %q", want)
		}
		if !strings.Contains(printed, flat) {
			t.Errorf("the printed notice does not carry %q", want)
		}
	}
}
