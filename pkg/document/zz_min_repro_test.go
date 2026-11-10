package document_test

import (
	"testing"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
)

// TestZZMinRepro: the smallest divergence found under LeftByID.
//
//  1. Shared text "XY". d1 types A then B after X: "XABY" (B's left origin
//     is A).
//  2. d2 deletes A: "XBY". Everyone syncs, so A is a tombstone everywhere
//     and the deletion is causally stable.
//  3. Concurrently, with A still a tombstone on both, d1 types p and d2
//     types q right after X (between X and B).
//  4. Some replicas GC (purging A) before exchanging p and q.
func TestZZMinRepro(t *testing.T) {
	type step struct{ gc1, gc2 bool }
	for _, c := range []step{{false, false}, {true, false}, {false, true}, {true, true}} {
		d1, d2 := intentShared(t, "XY")
		intentInsert(t, d1, 1, "A")
		intentInsert(t, d1, 2, "B")
		crossSync(t, d1, d2)
		zzEdit(t, d2, func(tx *json.Text) { tx.Edit(1, 2, "") })
		crossSync(t, d1, d2)
		minVV := d1.VersionVector()

		intentInsert(t, d1, 1, "p")
		intentInsert(t, d2, 1, "q")
		if c.gc1 {
			d1.GarbageCollect(minVV)
		}
		if c.gc2 {
			d2.GarbageCollect(minVV)
		}
		crossSync(t, d1, d2)
		t.Logf("MIN gc(d1)=%v gc(d2)=%v  d1=%q d2=%q %s", c.gc1, c.gc2, intentText(d1), intentText(d2), same(d1, d2))
	}
}

func same(a, b *document.Document) string {
	if intentText(a) == intentText(b) {
		return "converged"
	}
	return "DIVERGED"
}
