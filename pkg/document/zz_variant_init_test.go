package document_test

import (
	"os"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

func init() {
	crdt.RightImmediate = os.Getenv("RO_LIVE") != "1"
	crdt.ConditionalRight = os.Getenv("RO_COND") == "1"
	crdt.LeftByID = os.Getenv("RO_LEFTID") == "1"
	crdt.TreeOrigins = os.Getenv("TREE_NO_ORIGIN") == ""
	crdt.TreeSkeletonGC = os.Getenv("TREE_PHYS_GC") == ""
}
