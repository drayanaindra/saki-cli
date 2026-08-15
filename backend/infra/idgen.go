package infra

import (
	"crypto/rand"
	"fmt"
)

// UUIDGen generates RFC-4122 v4 run ids (matching apps/server's randomUUID() shape, so run ids
// from either backend look the same).
type UUIDGen struct{}

func (UUIDGen) New() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
