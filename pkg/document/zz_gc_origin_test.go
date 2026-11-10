package document_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// TestZZPurgedRightOrigin: d2 types next to a tombstone it has already seen
// removed, so the insert's right origin is that tombstone. d1 purges the
// tombstone under a version vector that is a legitimate minimum (it covers
// what both replicas reported, and d2 has not pushed the insert yet), then
// receives the insert.
func TestZZPurgedRightOrigin(t *testing.T) {
	d1, d2 := intentShared(t, "X")
	intentInsert(t, d1, 1, "Y")
	intentInsert(t, d1, 2, "Z")
	crossSync(t, d1, d2)

	require.NoError(t, d1.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetText("t").Edit(1, 2, "")
		return nil
	}))
	crossSync(t, d1, d2)
	require.Equal(t, "XZ", intentText(d2))

	// Both replicas have reported seeing the removal; nothing else is pending.
	minVV := d1.VersionVector()

	// d2 still holds Y as a tombstone and types between X and Z.
	intentInsert(t, d2, 1, "e")

	purged := d1.GarbageCollect(minVV)
	t.Logf("purged on d1: %d", purged)

	crossSync(t, d1, d2)
	require.Equal(t, intentText(d1), intentText(d2))
	require.Equal(t, "XeZ", intentText(d1))
}
