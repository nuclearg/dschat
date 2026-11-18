package chatsvr

import (
	"testing"

	"dschat/app/core/model"
)

func turn(id, parent string, at int64) model.Turn {
	return model.Turn{ID: id, ParentTurnID: parent, CreatedAt: at, Answer: &model.Answer{Status: model.StatusDone}}
}

func TestVisiblePathFollowsSelection(t *testing.T) {
	turns := []model.Turn{
		turn("T1", "0", 1),
		turn("T2", "T1", 2),
		turn("T3", "T2", 3),
		turn("T3b", "T2", 4),
	}
	got := visiblePath(turns, nil)
	if len(got) != 3 || got[0].ID != "T1" || got[1].ID != "T2" || got[2].ID != "T3b" {
		t.Fatalf("default path = %v, want T1,T2,T3b (newest sibling)", ids(got))
	}
	got = visiblePath(turns, map[string]string{"T2": "T3"})
	if len(got) != 3 || got[2].ID != "T3" {
		t.Fatalf("selected path = %v, want T1,T2,T3", ids(got))
	}
}

func TestChainStopsAtRoot(t *testing.T) {
	turns := []model.Turn{
		turn("T1", "0", 1),
		turn("T2", "T1", 2),
		turn("T3", "T2", 3),
	}
	got := chain(turns, "T3")
	if len(got) != 3 || got[0].ID != "T1" || got[2].ID != "T3" {
		t.Fatalf("chain = %v", ids(got))
	}
	if got := chain(turns, model.RootTurnID); len(got) != 0 {
		t.Fatalf("chain(root) = %v, want empty", ids(got))
	}
}

func ids(ts []model.Turn) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}
