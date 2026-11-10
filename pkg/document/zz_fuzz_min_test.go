package document_test

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestZZFuzzMin searches small random scenarios for a divergence and prints
// the shortest one found: replicas, rounds of concurrent single-character
// inserts/deletes near one spot, optional GC on some replicas, then sync.
func TestZZFuzzMin(t *testing.T) {
	trials := 200000
	if v := os.Getenv("ZZ_TRIALS"); v != "" {
		trials, _ = strconv.Atoi(v)
	}
	best := ""
	bestLen := 1 << 30
	found, errs := 0, 0
	errTrace := ""
	for seed := 0; seed < trials; seed++ {
		rnd := rand.New(rand.NewSource(int64(seed)))
		n := 2 + rnd.Intn(2)
		docs := intentReplicas(t, n, "XY")
		var trace []string
		ch := 'a'
		ok := func() (ok bool) {
			defer func() {
				if r := recover(); r != nil {
					ok = false
				}
			}()
			rounds := 1 + rnd.Intn(3)
			for round := 0; round < rounds; round++ {
				for i, d := range docs {
					for k := rnd.Intn(3); k > 0; k-- {
						l := len([]rune(intentText(d)))
						if l > 0 && rnd.Intn(3) == 0 {
							at := rnd.Intn(l)
							trace = append(trace, fmt.Sprintf("r%d d%d delete %d (%q)", round, i, at, string([]rune(intentText(d))[at])))
							zzEdit(t, d, func(tx *json.Text) { tx.Edit(at, at+1, "") })
						} else {
							at := rnd.Intn(l + 1)
							c := string(ch)
							ch++
							trace = append(trace, fmt.Sprintf("r%d d%d insert %q at %d (%q)", round, i, c, at, intentText(d)))
							zzEdit(t, d, func(tx *json.Text) { tx.Edit(at, at, c) })
						}
					}
				}
				if os.Getenv("ZZ_NOGC") == "" && rnd.Intn(2) == 0 {
					vv := zzMinVV(docs)
					for i, d := range docs {
						if rnd.Intn(2) == 0 {
							trace = append(trace, fmt.Sprintf("r%d GC d%d", round, i))
							d.GarbageCollect(vv)
						}
					}
				}
				trace = append(trace, fmt.Sprintf("r%d sync", round))
				if err := zzSyncAll(docs); err != nil {
					errTrace = fmt.Sprintf("seed=%d err=%v\n  %s", seed, err, strings.Join(trace, "\n  "))
					errs++
					return false
				}
			}
			return true
		}()
		if !ok {
			continue
		}
		var texts []string
		div := false
		for _, d := range docs {
			texts = append(texts, intentText(d))
			if intentText(d) != intentText(docs[0]) {
				div = true
			}
		}
		if div {
			found++
			if len(trace) < bestLen {
				bestLen = len(trace)
				best = fmt.Sprintf("seed=%d replicas=%d texts=%q\n  %s", seed, n, texts, strings.Join(trace, "\n  "))
			}
		}
	}
	t.Logf("FUZZ diverged=%d errors=%d of %d; shortest:\n%s\nfirst error:\n%s", found, errs, trials, best, errTrace)
	_ = document.New
}

func zzSyncAll(docs []*document.Document) error {
	packs := make([]*change.Pack, len(docs))
	for i, d := range docs {
		packs[i] = d.CreateChangePack()
	}
	for i, p := range packs {
		for j, d := range docs {
			if i == j {
				continue
			}
			if err := d.ApplyChangePack(change.NewPack(p.DocumentKey, change.NewCheckpoint(0, 0), p.Changes, time.InitialVersionVector, nil)); err != nil {
				return err
			}
		}
	}
	for i, p := range packs {
		var lastSeq uint32
		if len(p.Changes) > 0 {
			lastSeq = p.Changes[len(p.Changes)-1].ClientSeq()
		}
		if err := docs[i].ApplyChangePack(change.NewPack(p.DocumentKey, change.NewCheckpoint(0, lastSeq), nil, time.InitialVersionVector, nil)); err != nil {
			return err
		}
	}
	return nil
}
