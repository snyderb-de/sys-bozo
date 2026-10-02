package tui

import (
	"context"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/snyderb-de/sys-bozo/internal/repostate"
)

func TestRepoValidationCannotAuthorizeReplacementReview(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "replaced", true: "cancelled-and-replaced"}[cancel], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			m := testGuidedModel()
			m.screen = screenReview
			m.width, m.height = 80, 24
			m.validateRepo = func(context.Context, repostate.Operation) error { return nil }
			op := repostate.Operation{Repo: "/fixture", Kind: repostate.ActionCommit, Commands: []repostate.Command{{Name: "must-not-execute", Args: []string{"A"}, Interactive: true}}}
			m.acceptRepoAction(repoActionPreparedMsg{operation: op})
			validateA := m.confirmReviewedPlan()
			if validateA == nil {
				t.Fatal("review A did not request validation")
			}
			if cancel {
				next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
				m = next.(Model)
				if cmd != nil || m.screen != screenRepoTriage || m.reviewed.Repo != nil {
					t.Fatal("Escape did not cancel review")
				}
			}
			op.Commands[0].Args = []string{"B"}
			m.acceptRepoAction(repoActionPreparedMsg{operation: op})
			// Simulate A completing only after a different review B is installed.
			next, runCmd := m.Update(validateA())
			m = next.(Model)
			if runCmd != nil || m.screen != screenReview || m.mode != modeView || len(m.queue) != 0 || m.reviewed.Repo.Validating {
				t.Fatalf("old validation started replacement review: screen=%v queue=%#v", m.screen, m.queue)
			}
			// B still needs its own confirmation and successful validation.
			validateB := m.confirmReviewedPlan()
			if validateB == nil {
				t.Fatal("replacement review cannot be confirmed")
			}
			next, _ = m.Update(validateB())
			m = next.(Model)
			if m.screen != screenRunning || len(m.queue) != 1 || !reflect.DeepEqual(m.queue[0].Args, []string{"B"}) {
				t.Fatalf("replacement confirmation failed: screen=%v queue=%#v", m.screen, m.queue)
			}
		})
	}
}

func TestRepoValidationCompletionIsConsumedOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := testGuidedModel()
	m.screen = screenReview
	m.validateRepo = func(context.Context, repostate.Operation) error { return nil }
	op := repostate.Operation{Repo: "/fixture", Kind: repostate.ActionRestore, Commands: []repostate.Command{{Name: "must-not-execute", Args: []string{"original"}, Interactive: true}}}
	m.acceptRepoAction(repoActionPreparedMsg{operation: op})
	validate := m.confirmReviewedPlan()
	msg := validate()
	// Even a changed model copy cannot substitute unvalidated command bytes.
	m.reviewed.Repo.Operation.Commands[0].Args[0] = "changed"
	next, _ := m.Update(msg)
	m = next.(Model)
	if len(m.queue) != 1 || !reflect.DeepEqual(m.queue[0].Args, []string{"original"}) {
		t.Fatalf("executed operation differs from validated snapshot: %#v", m.queue)
	}
	m.queuePos = 1
	next, cmd := m.Update(msg)
	m = next.(Model)
	if cmd != nil || m.queuePos != 1 {
		t.Fatal("duplicate validation restarted execution")
	}
}

func TestRepoValidationErrorCannotContaminateReplacementReview(t *testing.T) {
	m := testGuidedModel()
	m.screen = screenReview
	m.validateRepo = func(context.Context, repostate.Operation) error { return repostate.ErrStaleStatus }
	op := repostate.Operation{Repo: "/fixture", Kind: repostate.ActionRestore}
	m.acceptRepoAction(repoActionPreparedMsg{operation: op})
	validateA := m.confirmReviewedPlan()
	m.acceptRepoAction(repoActionPreparedMsg{operation: op})
	next, cmd := m.Update(validateA())
	m = next.(Model)
	if cmd != nil || m.screen != screenReview || m.reviewed.Repo.Notice != "" || m.reviewed.Repo.Validating {
		t.Fatal("stale validation error changed the replacement review")
	}
}
