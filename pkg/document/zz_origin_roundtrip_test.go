package document_test

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

func originString(o *crdt.InsertOrigin) string {
	if o == nil {
		return "nil"
	}
	r := "end"
	if o.Right != nil {
		r = o.Right.ToTestString()
	}
	return o.Left.ToTestString() + "|" + r
}

// TestZZOriginSnapshotRoundTrip edits two replicas concurrently, encodes the
// result as a snapshot, decodes it, and checks every node's origin survives.
func TestZZOriginSnapshotRoundTrip(t *testing.T) {
	for seed := int64(1); seed <= 50; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		d1, d2 := intentShared(t, "0123456789")
		for round := 0; round < 5; round++ {
			for _, d := range []*document.Document{d1, d2} {
				for k := 0; k < 4; k++ {
					n := len([]rune(intentText(d)))
					at := rnd.Intn(n + 1)
					to := at
					if rnd.Intn(4) == 0 && at < n {
						to = at + 1
					}
					intentInsertRange(t, d, at, to, fmt.Sprint(rnd.Intn(10)))
				}
			}
			crossSync(t, d1, d2)
		}
		require.Equal(t, intentText(d1), intentText(d2))

		bytes, err := converter.ObjectToBytes(d1.RootObject())
		require.NoError(t, err)
		obj, err := converter.BytesToObject(bytes)
		require.NoError(t, err)
		decoded := obj.Get("t").(*crdt.Text)
		orig := d1.Root().GetText("t").Nodes()
		got := decoded.Nodes()
		require.Len(t, got, len(orig))
		for i := range orig {
			require.Equal(t, originString(orig[i].Origin()), originString(got[i].Origin()), "seed %d node %d", seed, i)
		}
	}
}

func intentInsertRange(t *testing.T, d *document.Document, from, to int, s string) {
	t.Helper()
	require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetText("t").Edit(from, to, s)
		return nil
	}))
}
