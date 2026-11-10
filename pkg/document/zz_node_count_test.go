package document_test

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestZZNodeCount types ~10k characters one per Edit on two replicas, with
// concurrent bursts and some deletions, syncs, GCs, and reports how many
// nodes the Text keeps per character and the snapshot bytes per character.
func TestZZNodeCount(t *testing.T) {
	d1, d2, a1, a2 := newReplicas(t)
	require.NoError(t, d1.Update(func(root *json.Object, _ *presence.Presence) error {
		root.SetNewText("t")
		return nil
	}))
	crossSync(t, d1, d2)

	rnd := rand.New(rand.NewSource(7))
	n1, n2 := 0, 0
	for round := 0; round < 100; round++ {
		// each replica types a 50-char burst at its own caret, concurrently
		for _, side := range []int{1, 2} {
			d, at := d1, n1
			if side == 2 {
				d, at = d2, n2
			}
			length := len([]rune(d.Root().GetText("t").String()))
			if at > length {
				at = length
			}
			for i := 0; i < 50; i++ {
				require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
					root.GetText("t").Edit(at+i, at+i, "x")
					return nil
				}))
			}
			if rnd.Intn(4) == 0 && at+5 < length {
				require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
					root.GetText("t").Edit(at, at+3, "")
					return nil
				}))
			}
		}
		crossSync(t, d1, d2)
		length := len([]rune(d1.Root().GetText("t").String()))
		n1, n2 = rnd.Intn(length+1), rnd.Intn(length+1)
	}
	require.Equal(t, d1.Root().GetText("t").String(), d2.Root().GetText("t").String())
	d1.GarbageCollect(helper.MaxVersionVector(a1, a2))

	bytes, err := converter.ObjectToBytes(d1.RootObject())
	require.NoError(t, err)
	nodes, tombs := 0, 0
	for _, n := range d1.Root().GetText("t").Nodes() {
		nodes++
		if n.RemovedAt() != nil {
			tombs++
		}
	}
	chars := len([]rune(d1.Root().GetText("t").String()))
	t.Logf("NODES chars=%d nodes=%d tombstones=%d nodes/char=%.2f snapshotBytes=%d B/char=%.1f",
		chars, nodes, tombs, float64(nodes)/float64(chars), len(bytes), float64(len(bytes))/float64(chars))
}
