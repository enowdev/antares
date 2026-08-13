package agent

import "testing"

func TestUsableLessonRejectsAuxiliaryFragments(t *testing.T) {
	for _, lesson := range []string{"NONE", "Wait", ".", "When `edit_file"} {
		if usableLesson(lesson) {
			t.Errorf("malformed lesson accepted: %q", lesson)
		}
	}
	if !usableLesson("When edit_file fails with old_string not found, read the current file and copy exact whitespace before retrying.") {
		t.Fatal("valid lesson rejected")
	}
}

func TestUsableLessonRejectsTrailingNone(t *testing.T) {
	if usableLesson("When a tool fails, inspect the concrete error and retry with corrected arguments. NONE") {
		t.Fatal("auxiliary NONE suffix should not enter the prompt")
	}
}
