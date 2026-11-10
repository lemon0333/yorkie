package crdt

import (
	"testing"
	"unsafe"
)

func TestZZNodeSize(t *testing.T) {
	t.Logf("SIZE RGATreeSplitNode=%d", unsafe.Sizeof(RGATreeSplitNode[*TextValue]{}))
}
