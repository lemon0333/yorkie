package document_test

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func costSync(t *testing.T, docs []*document.Document) {
	packs := make([]*change.Pack, len(docs))
	for i, d := range docs {
		packs[i] = d.CreateChangePack()
	}
	for i, p := range packs {
		for j, d := range docs {
			if i != j {
				require.NoError(t, d.ApplyChangePack(change.NewPack(p.DocumentKey, change.NewCheckpoint(0, 0), p.Changes, time.InitialVersionVector, nil)))
			}
		}
	}
	for i, p := range packs {
		var last uint32
		if len(p.Changes) > 0 {
			last = p.Changes[len(p.Changes)-1].ClientSeq()
		}
		require.NoError(t, docs[i].ApplyChangePack(change.NewPack(p.DocumentKey, change.NewCheckpoint(0, last), nil, time.InitialVersionVector, nil)))
	}
}

func costEdit(t *testing.T, d *document.Document, from, to int, s string) {
	require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetText("t").Edit(from, to, s)
		return nil
	}))
}

func heap() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// TestZZGCCost: two writers type 10k characters keystroke by keystroke, then
// X% is deleted (runs of 1..20, or scattered single characters), all sync,
// both GC at the min version vector. Reports what d0 keeps.
func TestZZGCCost(t *testing.T) {
	if os.Getenv("ZZ_COST") == "" {
		t.Skip("set ZZ_COST=1 to measure")
	}
	for _, mode := range []string{"runs", "single"} {
		for _, pct := range []int{0, 10, 30, 50} {
			base := heap()
			docs := make([]*document.Document, 2)
			for i := range docs {
				a, _ := time.ActorIDFromHex(fmt.Sprintf("%024x", i+1))
				docs[i] = document.New("cost")
				docs[i].SetActor(a)
			}
			require.NoError(t, docs[0].Update(func(root *json.Object, _ *presence.Presence) error {
				root.SetNewText("t")
				return nil
			}))
			costSync(t, docs)
			rnd := rand.New(rand.NewSource(42))
			// Each writer types in its own place: d0 at the front half's
			// end, d1 at the very end. Sync every 50 keystrokes.
			cur := []int{0, 0}
			for k := 0; k < 5000; k++ {
				for i, d := range docs {
					l := len([]rune(d.Root().GetText("t").String()))
					at := cur[i]
					if i == 1 {
						at = l
					}
					if at > l {
						at = l
					}
					costEdit(t, d, at, at, string(rune('a'+rnd.Intn(26))))
					cur[i] = at + 1
				}
				if k%50 == 49 {
					costSync(t, docs)
				}
			}
			costSync(t, docs)
			total := len([]rune(docs[0].Root().GetText("t").String()))
			target := total * pct / 100
			deleted := 0
			for deleted < target {
				d := docs[deleted%2]
				l := len([]rune(d.Root().GetText("t").String()))
				w := 1
				if mode == "runs" {
					w = 1 + rnd.Intn(20)
				}
				w = min(w, target-deleted, l)
				at := rnd.Intn(l - w + 1)
				costEdit(t, d, at, at+w, "")
				deleted += w
				if deleted%200 < w {
					costSync(t, docs)
				}
			}
			costSync(t, docs)
			vv := docs[0].VersionVector()
			o := docs[1].VersionVector()
			vv.Min(&o)
			for _, d := range docs {
				d.GarbageCollect(vv)
			}
			costSync(t, docs)
			require.Equal(t, docs[0].Root().GetText("t").String(), docs[1].Root().GetText("t").String())

			text := docs[0].Root().GetText("t")
			live := len([]rune(text.String()))
			nodes := len(text.Nodes())
			snap, err := converter.SnapshotToBytes(docs[0].RootObject(), nil)
			require.NoError(t, err)
			docs[1] = nil
			h := heap() - base
			runtime.KeepAlive(docs)
			per := 0.0
			if live > 0 {
				per = float64(len(snap)) / float64(live)
			}
			t.Logf("COST mode=%s del=%d%% live=%d nodes=%d snapshot=%d bytes/live=%.2f heap=%d heap/live=%.1f",
				mode, pct, live, nodes, len(snap), per, h, float64(h)/float64(max(live, 1)))
		}
	}
}
