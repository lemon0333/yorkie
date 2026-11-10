package document_test

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestZZPinCost: two replicas type ~10k chars one per edit with concurrent
// bursts, then delete a share of the text in small ranges, sync and GC.
// Reports how many tombstones survive GC and the snapshot size.
func TestZZPinCost(t *testing.T) {
	for _, delShare := range []int{10, 30, 50} {
		d1, d2, a1, a2 := newReplicas(t)
		require.NoError(t, d1.Update(func(root *json.Object, _ *presence.Presence) error {
			root.SetNewText("t")
			return nil
		}))
		crossSync(t, d1, d2)
		rnd := rand.New(rand.NewSource(7))
		for round := 0; round < 100; round++ {
			for _, d := range []*document.Document{d1, d2} {
				n := len([]rune(intentText(d)))
				at := rnd.Intn(n + 1)
				for i := 0; i < 50; i++ {
					require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
						root.GetText("t").Edit(at+i, at+i, "x")
						return nil
					}))
				}
			}
			crossSync(t, d1, d2)
		}
		total := len([]rune(intentText(d1)))
		deleted := 0
		for deleted < total*delShare/100 {
			d := d1
			if rnd.Intn(2) == 0 {
				d = d2
			}
			n := len([]rune(intentText(d)))
			at := rnd.Intn(n - 5)
			k := 1 + rnd.Intn(5)
			require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
				root.GetText("t").Edit(at, at+k, "")
				return nil
			}))
			deleted += k
			if rnd.Intn(10) == 0 {
				crossSync(t, d1, d2)
			}
		}
		crossSync(t, d1, d2)
		require.Equal(t, intentText(d1), intentText(d2))
		d1.GarbageCollect(helper.MaxVersionVector(a1, a2))
		nodes, tombs := 0, 0
		leftRef, rightRef := map[string]bool{}, map[string]bool{}
		all := d1.Root().GetText("t").Nodes()
		for _, nd := range all {
			o := nd.Origin()
			if o == nil {
				continue
			}
			own := nd.ID().CreatedAt().Key()
			if k := o.Left.CreatedAt().Key(); k != own {
				leftRef[k] = true
			}
			if o.Right != nil {
				if k := o.Right.CreatedAt().Key(); k != own {
					rightRef[k] = true
				}
			}
		}
		byLeft, byRight, byBoth := 0, 0, 0
		for _, nd := range all {
			nodes++
			if nd.RemovedAt() != nil {
				tombs++
				k := nd.ID().CreatedAt().Key()
				switch {
				case leftRef[k] && rightRef[k]:
					byBoth++
				case leftRef[k]:
					byLeft++
				case rightRef[k]:
					byRight++
				}
			}
		}
		t.Logf("PINSPLIT delete=%d%% leftOnly=%d rightOnly=%d both=%d", delShare, byLeft, byRight, byBoth)
		bytes, err := converter.ObjectToBytes(d1.RootObject())
		require.NoError(t, err)
		live := len([]rune(intentText(d1)))
		t.Logf("PINCOST delete=%d%% typed=%d deleted~%d live=%d nodes=%d tombstonesAfterGC=%d snapshot=%dB (%.1f B/live char)",
			delShare, total, deleted, live, nodes, tombs, len(bytes), float64(len(bytes))/float64(live))
	}
}
