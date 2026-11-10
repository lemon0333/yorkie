package document_test

import (
	"os"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

func init() {
	crdt.RightImmediate = os.Getenv("RO_IMMEDIATE") == "1"
	crdt.ConditionalRight = os.Getenv("RO_COND") == "1"
	crdt.LeftByID = os.Getenv("RO_LEFTID") == "1"
}
