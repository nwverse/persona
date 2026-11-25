package blockers

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestBlockersStaysPure guards the package against accidental IO, network, clock,
// database, or non-stdlib dependencies: blockers must remain a pure function of its inputs.
func TestBlockersStaysPure(t *testing.T) {
	allowed := []string{"cmp", "encoding/json", "errors", "fmt", "slices", "strings", "sync"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("expected non-empty Go source files in blockers package")
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if !slices.Contains(allowed, p) {
				t.Errorf("%s imports %s: blockers package must stay pure (allowed stdlib only: %v)", name, p, allowed)
			}
		}
	}
}

func TestBuiltinResolvers(t *testing.T) {
	reg := DefaultRegistry()

	t.Run("todo_done", func(t *testing.T) {
		facts := FactSnapshot{
			Todos: map[string]TodoFact{
				"t_open":      {ID: "t_open"},
				"t_done":      {ID: "t_done", Done: true},
				"t_accepted":  {ID: "t_accepted", Done: true, Accepted: true},
				"t_cancelled": {ID: "t_cancelled", Done: true, Cancelled: true},
			},
		}
		r, _ := reg.Lookup(KindTodoDone)

		res, err := r.Resolve(Blocker{ID: "b1", TodoID: "child", Kind: KindTodoDone, TargetRef: "t_open"}, facts)
		if err != nil || res.Resolved || res.Escalate {
			t.Fatalf("open todo: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(Blocker{ID: "b2", TodoID: "child", Kind: KindTodoDone, TargetRef: "t_done"}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("done todo: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(Blocker{ID: "b3", TodoID: "child", Kind: KindTodoDone, TargetRef: "t_done", ConditionJSON: `{"require_accepted":true}`}, facts)
		if err != nil || res.Resolved {
			t.Fatalf("done-not-accepted todo with require_accepted: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(Blocker{ID: "b4", TodoID: "child", Kind: KindTodoDone, TargetRef: "t_accepted", ConditionJSON: `{"require_accepted":true}`}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("accepted todo: got (%+v, %v)", res, err)
		}
		// Cancelled fails closed and escalates.
		res, err = r.Resolve(Blocker{ID: "b5", TodoID: "child", Kind: KindTodoDone, TargetRef: "t_cancelled"}, facts)
		if err != nil || res.Resolved || !res.Escalate {
			t.Fatalf("cancelled todo should fail closed and escalate: got (%+v, %v)", res, err)
		}
	})

	t.Run("todo_stage", func(t *testing.T) {
		facts := FactSnapshot{
			Todos: map[string]TodoFact{
				"t_api":       {ID: "t_api", Stage: "contract_ready", Contracts: []string{"api_v1"}},
				"t_cancelled": {ID: "t_cancelled", Stage: "done", Contracts: []string{"api_v1"}, Cancelled: true},
			},
		}
		r, _ := reg.Lookup(KindTodoStage)

		res, err := r.Resolve(Blocker{
			ID: "s1", TodoID: "web", Kind: KindTodoStage, TargetRef: "t_api",
			ConditionJSON: `{"required_stage":"contract_ready","required_contracts":["api_v1"]}`,
		}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("matching stage+contracts: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(Blocker{
			ID: "s2", TodoID: "web", Kind: KindTodoStage, TargetRef: "t_api",
			ConditionJSON: `{"required_stage":"pr_open"}`,
		}, facts)
		if err != nil || res.Resolved {
			t.Fatalf("stage not reached: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(Blocker{
			ID: "s3", TodoID: "web", Kind: KindTodoStage, TargetRef: "t_api",
			ConditionJSON: `{"required_contracts":["api_v1","schema_frozen"]}`,
		}, facts)
		if err != nil || res.Resolved {
			t.Fatalf("missing contract: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(Blocker{
			ID: "s4", TodoID: "web", Kind: KindTodoStage, TargetRef: "t_cancelled",
			ConditionJSON: `{"required_stage":"contract_ready"}`,
		}, facts)
		if err != nil || res.Resolved || !res.Escalate {
			t.Fatalf("cancelled stage prereq: got (%+v, %v)", res, err)
		}
	})

	t.Run("external_pr", func(t *testing.T) {
		prMerged := "https://github.com/example/repo/pull/101"
		prClosed := "https://github.com/example/repo/pull/102"
		prOpen := "https://github.com/example/repo/pull/103"
		facts := FactSnapshot{
			PRs: map[string]PRFact{
				prMerged: {URL: prMerged, State: "merged", Merged: true},
				prClosed: {URL: prClosed, State: "closed"},
				prOpen:   {URL: prOpen, State: "open"},
			},
		}
		r, _ := reg.Lookup(KindExternalPR)

		res, err := r.Resolve(Blocker{ID: "p1", TodoID: "t1", Kind: KindExternalPR, TargetRef: prMerged + "/"}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("merged PR: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(Blocker{ID: "p2", TodoID: "t2", Kind: KindExternalPR, TargetRef: prClosed}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("closed PR default: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(Blocker{ID: "p3", TodoID: "t3", Kind: KindExternalPR, TargetRef: prClosed, ConditionJSON: `{"require_merged":true}`}, facts)
		if err != nil || res.Resolved || !res.Escalate {
			t.Fatalf("closed PR with require_merged: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(Blocker{ID: "p4", TodoID: "t4", Kind: KindExternalPR, TargetRef: prOpen}, facts)
		if err != nil || res.Resolved || res.Escalate {
			t.Fatalf("open PR: got (%+v, %v)", res, err)
		}
	})

	t.Run("write_conflict", func(t *testing.T) {
		facts := FactSnapshot{
			Todos: map[string]TodoFact{
				"t_running": {ID: "t_running", HasActiveAgent: true},
				"t_done":    {ID: "t_done", Done: true},
			},
		}
		r, _ := reg.Lookup(KindWriteConflict)
		res, err := r.Resolve(Blocker{ID: "w1", TodoID: "t2", Kind: KindWriteConflict, TargetRef: "t_running"}, facts)
		if err != nil || res.Resolved {
			t.Fatalf("running conflict: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(Blocker{ID: "w2", TodoID: "t2", Kind: KindWriteConflict, TargetRef: "t_done"}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("done conflict: got (%+v, %v)", res, err)
		}
	})

	t.Run("merge_train", func(t *testing.T) {
		facts := FactSnapshot{
			MergeTrains: map[string]MergeTrainFact{
				"github.com/example/repo": {
					RepoKey:       "github.com/example/repo",
					HeadTodoID:    "t_head",
					HeadState:     "active",
					ActiveTodoIDs: []string{"t_head", "t_next"},
				},
				"github.com/example/done-repo": {
					RepoKey:    "github.com/example/done-repo",
					HeadTodoID: "t_old",
					HeadState:  "merged",
				},
			},
		}
		r, _ := reg.Lookup(KindMergeTrain)

		// Still waiting while attempts < 2.
		res, err := r.Resolve(Blocker{
			ID: "m1", TodoID: "t_next", Kind: KindMergeTrain, TargetRef: "t_head",
			ConditionJSON: `{"repo_key":"github.com/example/repo"}`, Attempts: 1,
		}, facts)
		if err != nil || res.Resolved || res.Escalate {
			t.Fatalf("waiting merge train: got (%+v, %v)", res, err)
		}

		// Attempts == 2 escalates.
		res, err = r.Resolve(Blocker{
			ID: "m2", TodoID: "t_next", Kind: KindMergeTrain, TargetRef: "t_head",
			ConditionJSON: `{"repo_key":"github.com/example/repo"}`, Attempts: 2,
		}, facts)
		if err != nil || res.Resolved || !res.Escalate {
			t.Fatalf("escalated merge train: got (%+v, %v)", res, err)
		}

		// Merged head resolves.
		res, err = r.Resolve(Blocker{
			ID: "m3", TodoID: "t_next", Kind: KindMergeTrain, TargetRef: "t_old",
			ConditionJSON: `{"repo_key":"github.com/example/done-repo"}`, Attempts: 0,
		}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("cleared merge train: got (%+v, %v)", res, err)
		}
	})

	t.Run("owner_decision", func(t *testing.T) {
		facts := FactSnapshot{
			Todos: map[string]TodoFact{
				"t_wait":    {ID: "t_wait", HasOpenDecision: true},
				"t_cleared": {ID: "t_cleared", HasOpenDecision: false},
			},
		}
		r, _ := reg.Lookup(KindOwnerDecision)
		res, err := r.Resolve(Blocker{ID: "d1", TodoID: "t_wait", Kind: KindOwnerDecision, TargetRef: "t_wait"}, facts)
		if err != nil || res.Resolved {
			t.Fatalf("pending decision: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(Blocker{ID: "d2", TodoID: "t_cleared", Kind: KindOwnerDecision, TargetRef: "t_cleared"}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("cleared decision: got (%+v, %v)", res, err)
		}
	})
}

func TestCustomResolverExtensionAndReconcileUnblockedSemantics(t *testing.T) {
	reg := DefaultRegistry()
	const kindCustomCI Kind = "custom_ci_gate"
	if err := reg.Register(kindCustomCI, ResolverFunc(func(b Blocker, facts FactSnapshot) (Resolution, error) {
		if facts.Todos[b.TargetRef].Done {
			return Resolution{Resolved: true, Reason: "custom_green"}, nil
		}
		return Resolution{Resolved: false, Reason: "custom_pending"}, nil
	})); err != nil {
		t.Fatalf("Register custom resolver: %v", err)
	}

	facts := FactSnapshot{
		Todos: map[string]TodoFact{
			"dep_done": {ID: "dep_done", Done: true},
			"dep_open": {ID: "dep_open", Done: false},
		},
		PRs: map[string]PRFact{
			"https://github.com/example/repo/pull/9": {URL: "https://github.com/example/repo/pull/9", State: "merged", Merged: true},
		},
	}

	// todo_a has 2 active blockers: BOTH resolve -> unblocked.
	// todo_b has 2 active blockers: 1 resolves, 1 stays active -> NOT unblocked.
	// todo_c has 1 active merge_train blocker at attempts=2 -> escalated, NOT unblocked.
	active := []Blocker{
		{ID: "b_c1", TodoID: "todo_c", Kind: KindMergeTrain, TargetRef: "dep_open", State: StateActive, Attempts: 2},
		{ID: "b_b2", TodoID: "todo_b", Kind: KindTodoDone, TargetRef: "dep_open", State: StateActive},
		{ID: "b_a2", TodoID: "todo_a", Kind: kindCustomCI, TargetRef: "dep_done", State: StateActive},
		{ID: "b_b1", TodoID: "todo_b", Kind: KindExternalPR, TargetRef: "https://github.com/example/repo/pull/9", State: StateActive},
		{ID: "b_a1", TodoID: "todo_a", Kind: KindExternalPR, TargetRef: "https://github.com/example/repo/pull/9", State: StateActive},
		{ID: "b_ignored", TodoID: "todo_done_already", Kind: KindTodoDone, TargetRef: "dep_done", State: StateResolved},
	}

	got := Reconcile(active, facts, reg)
	wantResolved := []string{"b_a1", "b_a2", "b_b1"}
	wantEscalated := []string{"b_c1"}
	wantUnblocked := []string{"todo_a"}

	if !reflect.DeepEqual(got.ResolvedIDs, wantResolved) {
		t.Fatalf("ResolvedIDs = %v, want %v", got.ResolvedIDs, wantResolved)
	}
	if !reflect.DeepEqual(got.EscalatedIDs, wantEscalated) {
		t.Fatalf("EscalatedIDs = %v, want %v", got.EscalatedIDs, wantEscalated)
	}
	if !reflect.DeepEqual(got.UnblockedTodoIDs, wantUnblocked) {
		t.Fatalf("UnblockedTodoIDs = %v, want %v", got.UnblockedTodoIDs, wantUnblocked)
	}
}

func TestSharedActiveBlockersDeterministicAggregation(t *testing.T) {
	active := []Blocker{
		{ID: "b1", TodoID: "t3", Kind: KindExternalPR, TargetRef: "pr/1", State: StateActive},
		{ID: "b2", TodoID: "t1", Kind: KindExternalPR, TargetRef: "pr/1", State: StateActive},
		{ID: "b3", TodoID: "t2", Kind: KindExternalPR, TargetRef: "pr/1", State: StateActive},
		// Duplicate todo on same target counts once.
		{ID: "b4", TodoID: "t2", Kind: KindMergeTrain, TargetRef: "pr/1", State: StateActive},
		// Only 1 distinct todo -> excluded.
		{ID: "b5", TodoID: "t9", Kind: KindTodoDone, TargetRef: "solo", State: StateActive},
		// Resolved blocker -> excluded.
		{ID: "b6", TodoID: "t8", Kind: KindTodoDone, TargetRef: "solo", State: StateResolved},
		// Two targets tied at count=2 -> ordered lexicographically by TargetRef.
		{ID: "b7", TodoID: "t2", Kind: KindTodoDone, TargetRef: "dep_b", State: StateActive},
		{ID: "b8", TodoID: "t1", Kind: KindTodoDone, TargetRef: "dep_b", State: StateActive},
		{ID: "b9", TodoID: "t2", Kind: KindTodoDone, TargetRef: "dep_a", State: StateActive},
		{ID: "b10", TodoID: "t1", Kind: KindTodoDone, TargetRef: "dep_a", State: StateActive},
	}

	got := SharedActiveBlockers(active)
	if len(got) != 3 {
		t.Fatalf("expected 3 shared groups, got %+v", got)
	}
	if got[0].TargetRef != "pr/1" || got[0].Count != 3 || !reflect.DeepEqual(got[0].TodoIDs, []string{"t1", "t2", "t3"}) {
		t.Fatalf("group[0] mismatch: %+v", got[0])
	}
	if got[1].TargetRef != "dep_a" || got[1].Count != 2 {
		t.Fatalf("group[1] mismatch: %+v", got[1])
	}
	if got[2].TargetRef != "dep_b" || got[2].Count != 2 {
		t.Fatalf("group[2] mismatch: %+v", got[2])
	}
}
