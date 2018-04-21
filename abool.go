package bulkhttpclient

import (
	"sync/atomic"
)

// AtomicBool use *AtomicBool in structs to avoid copy
type AtomicBool int32

// NewAtomicBool ...
func NewAtomicBool() *AtomicBool {
	return new(AtomicBool)
}

// Set ...
func (ab *AtomicBool) Set() {
	atomic.StoreInt32((*int32)(ab), 1)
}

// IsSet ...
func (ab *AtomicBool) IsSet() bool {
	return atomic.LoadInt32((*int32)(ab)) == 1
}
