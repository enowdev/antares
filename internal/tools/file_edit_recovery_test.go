package tools

import (
	"strings"
	"testing"
)

// The adjacent-insertion recovery reports success, so every case where it
// guesses wrong is a silent file corruption. These tests pin the guards that
// keep it from guessing.

// A NUMBER| paste must never reach the fuzzy path. The prefix survives in the
// anchor's token set, so the stale line still scored high against the real
// one — and the inserted row was written to the file carrying its literal
// "13|" prefix while the tool reported "1 replacement(s)".
func TestSpliceRefusesReadFilePrefixedPaste(t *testing.T) {
	content := "# Pools\n\n- **pool39v2** | 14 hand | active\n- **pool40** | 9 hand | active\n"
	old := "12|- **pool39v2** | 14 hand | active"
	nw := old + "\n13|- **pool41** | 3 hand | new"

	got, ok := spliceAdjacentInsertion(content, old, nw)
	if ok {
		t.Fatalf("spliced a NUMBER|-prefixed paste; file would gain a literal prefix:\n%q", got)
	}
}

// A deleted anchor whose sibling row survives must not be spliced onto that
// sibling. Rows of one table share almost every token by construction, so
// token overlap cleared both the 0.78 floor and the 0.12 margin. Edit distance
// does not separate them either — the dates differ by one character, exactly
// like a typo — which is why the guard keys on digits.
func TestSpliceRefusesDeletedAnchorWithSimilarSibling(t *testing.T) {
	content := "# Release notes\n\nThis document tracks published build artifacts.\n\n| 2026-03-01 | nightly | artifacts uploaded to the mirror |\n"
	old := "| 2026-02-01 | nightly | artifacts uploaded to the mirror |"
	nw := old + "\n| 2026-04-01 | stable | artifacts signed and uploaded |"

	got, ok := spliceAdjacentInsertion(content, old, nw)
	if ok {
		t.Fatalf("spliced against an anchor that is not in the file:\n%q", got)
	}
}

// The digit guard must not reject a light rewording that keeps its numbers.
// (The similarity floor of 0.78 already bounds how far the wording may drift;
// this pins that digits do not add a second, stricter rejection on top.)
func TestSpliceAllowsRewordingThatKeepsDigits(t *testing.T) {
	content := "# Status\n\n| v2 | 14 items | active on the primary host right now |\n| v3 | 2 items | idle |\n"
	old := "| v2 | 14 items | active on the primary host |"
	nw := old + "\n| v9 | 8 items | new |"

	got, ok := spliceAdjacentInsertion(content, old, nw)
	if !ok {
		t.Fatal("a lightly reworded anchor with identical digits should still recover")
	}
	if !strings.Contains(got, "| v9 | 8 items | new |") {
		t.Fatalf("insertion missing:\n%q", got)
	}
	if !strings.Contains(got, "| v2 | 14 items | active on the primary host right now |") {
		t.Fatalf("anchor was mutated instead of preserved byte-for-byte:\n%q", got)
	}
}

func TestDigitsOfLine(t *testing.T) {
	if digitsOfLine("| 2026-02-01 | nightly |") == digitsOfLine("| 2026-03-01 | nightly |") {
		t.Error("different dates must yield different digit strings")
	}
	if digitsOfLine("compiles the binry") != digitsOfLine("compiles the binary") {
		t.Error("a typo in a digit-free line must not change the digits")
	}
	if got := digitsOfLine("a1b2c3"); got != "123" {
		t.Errorf("digitsOfLine = %q, want %q", got, "123")
	}
}

// The guards must not kill the case the recovery exists for: the same line,
// lightly abbreviated, with a row added after it.
func TestSpliceStillRecoversLightlyRewordedAnchor(t *testing.T) {
	content := "# Commands\n\n| make build | compiles the static binary for the host platform |\n| make lint | vet |\n"
	old := "| make build | compiles the static binary for the host platfrm |" // one typo
	nw := old + "\n| make test | runs the suite |"

	got, ok := spliceAdjacentInsertion(content, old, nw)
	if !ok {
		t.Fatal("recovery declined a genuine near-edit anchor; the guards are too tight")
	}
	if !strings.Contains(got, "| make build | compiles the static binary for the host platform |") {
		t.Fatalf("anchor line was mutated instead of preserved byte-for-byte:\n%q", got)
	}
	if !strings.Contains(got, "| make test | runs the suite |") {
		t.Fatalf("the insertion is missing:\n%q", got)
	}
	if strings.Count(got, "| make lint | vet |") != 1 {
		t.Fatalf("an unrelated line was duplicated or lost:\n%q", got)
	}
}

func TestNearEditDistance(t *testing.T) {
	if !nearEditDistance("hello world", "hello world", 0.25) {
		t.Error("identical strings must be near")
	}
	if !nearEditDistance("compiles the binary", "compiles the binry", 0.25) {
		t.Error("a one-character typo must stay near")
	}
	if nearEditDistance("| 2026-02-01 | nightly | uploaded |", "| 2026-03-01 | stable | signed |", 0.25) {
		t.Error("two different table rows must not be near")
	}
	if nearEditDistance("short", "a much much longer line entirely", 0.25) {
		t.Error("a large length gap must not be near")
	}
}

// read_file and edit_file must agree on what a lone CR means. When they
// disagree, read_file hands the model line numbers that do not exist in the
// file's real line structure — producing exactly the stale anchors this PR is
// trying to eliminate.
func TestLoneCRIsDataUnlessTheFileIsCRTerminated(t *testing.T) {
	// LF file with a CR inside a value: one logical line per LF.
	lfWithData := "note ends\rrest\nsecond line\n"
	if got := len(lineSpans(lfWithData)); got != 2 {
		t.Errorf("LF file with an embedded CR: got %d lines, want 2", got)
	}
	// Genuine classic-Mac file: CR terminates.
	crFile := "alpha\rbeta\rgamma"
	if got := len(lineSpans(crFile)); got != 3 {
		t.Errorf("CR-terminated file: got %d lines, want 3", got)
	}
	// CRLF stays one line per pair.
	crlf := "a\r\nb\r\nc\r\n"
	if got := len(lineSpans(crlf)); got != 3 {
		t.Errorf("CRLF file: got %d lines, want 3", got)
	}
}
