package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
)

// lessonSource marks memories that were learned from the agent's own mistakes,
// so they can be surfaced back into the prompt as durable lessons.
const lessonSource = "lesson"

// toolFailure captures one errored tool call for post-turn reflection.
type toolFailure struct {
	Tool  string
	Args  string
	Error string
}

// learnFromErrors reflects on the tool failures from a turn the agent recovered
// from and, if there is a reusable lesson, stores it as a global memory. Run in
// the background so it never slows a turn. This is how antares "grows": next
// time, the lesson is injected into the prompt (see lessonsBlock).
func (a *Agent) learnFromErrors(ctx context.Context, userMsg, reply string, failures []toolFailure) {
	if len(failures) == 0 || a.db == nil || !a.config().Memory.Enabled {
		return
	}
	client, model, _, err := a.newAuxClient("")
	if err != nil {
		return
	}

	var fb strings.Builder
	for _, f := range failures {
		fmt.Fprintf(&fb, "- %s(%s) failed: %s\n", f.Tool, trimForModel(f.Args, 200), trimForModel(f.Error, 300))
	}

	sys := "You improve an AI agent by turning its mistakes into durable lessons. Given tool errors from a turn " +
		"(which the agent ultimately recovered from), write ONE concise, reusable, imperative lesson that would " +
		"prevent this class of error next time — e.g. \"When <tool> fails with <symptom>, do <fix> first.\" Keep it " +
		"under 180 characters, general (not tied to this exact input or target), and directly actionable. If there " +
		"is no generalizable lesson, or it duplicates an existing lesson below, reply exactly: NONE\n\n" +
		"Existing lessons:\n" + a.recentLessons(ctx, 60)

	tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := client.Chat(tctx, llm.Request{
		Model:     model,
		System:    sys,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "Task: " + trimForModel(userMsg, 200) + "\n\nTool errors:\n" + fb.String()}},
		MaxTokens: 120,
	})
	if err != nil || resp == nil {
		return
	}
	lesson := strings.TrimSpace(strings.Trim(strings.TrimSpace(resp.Content), "\"'`"))
	if lesson == "" || strings.EqualFold(lesson, "NONE") || len(lesson) > 400 {
		return
	}
	if err := a.db.PutMemory(ctx, &store.Memory{
		ID:      newID("mem"),
		Scope:   "global",
		Content: lesson,
		Tags:    `["lesson"]`,
		Source:  lessonSource,
	}); err != nil {
		slog.Debug("save lesson failed", "error", err)
		return
	}
	slog.Debug("learned a lesson from an error", "lesson", lesson)
}

// recentLessons returns the stored lessons, newest first, as a bullet list.
func (a *Agent) recentLessons(ctx context.Context, n int) string {
	if a.db == nil {
		return ""
	}
	items, err := a.db.ListMemories(ctx, "global", "", n)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, m := range items {
		if m.Source == lessonSource {
			b.WriteString("- " + m.Content + "\n")
		}
	}
	return b.String()
}

// lessonsBlock renders learned lessons for the system prompt, so the agent
// applies past corrections and does not repeat the same mistakes.
func (a *Agent) lessonsBlock(ctx context.Context) string {
	if a.db == nil || !a.config().Memory.Enabled {
		return ""
	}
	items, err := a.db.ListMemories(ctx, "global", "", 60)
	if err != nil {
		return ""
	}
	var lessons []string
	seen := make(map[string]struct{})
	for _, m := range items {
		if m.Source == lessonSource {
			lesson := strings.TrimSpace(m.Content)
			if !usableLesson(lesson) {
				continue
			}
			key := strings.ToLower(lesson)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			lessons = append(lessons, lesson)
		}
		if len(lessons) >= 12 {
			break
		}
	}
	if len(lessons) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Lessons you've learned\n\n")
	b.WriteString("Hard-won from past mistakes on this machine — apply them so you do not repeat the same errors:\n")
	for _, l := range lessons {
		b.WriteString("- " + l + "\n")
	}
	return b.String()
}

// usableLesson keeps malformed auxiliary-model output out of the system
// prompt. Historical rows include fragments such as "NONE", "Wait", and
// duplicated partial sentences; presenting them as instructions makes tool
// behavior less predictable and wastes context.
func usableLesson(s string) bool {
	if len(s) < 32 || len(s) > 400 {
		return false
	}
	if strings.EqualFold(s, "none") || strings.HasSuffix(strings.ToLower(s), " none") {
		return false
	}
	if strings.EqualFold(s, "wait") || strings.EqualFold(s, ".") {
		return false
	}
	return true
}
