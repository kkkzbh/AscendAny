package httpapi

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"sync/atomic"
)

type requestIDGenerator struct {
	prefix  [8]byte
	counter atomic.Uint64
}

func newRequestIDGenerator(random io.Reader) (*requestIDGenerator, error) {
	if random == nil {
		return nil, fmt.Errorf("request ID random source is required")
	}
	var generator requestIDGenerator
	if _, err := io.ReadFull(random, generator.prefix[:]); err != nil {
		return nil, fmt.Errorf("seed request ID generator: %w", err)
	}
	return &generator, nil
}

func (generator *requestIDGenerator) Next() string {
	var raw [16]byte
	copy(raw[:8], generator.prefix[:])
	binary.BigEndian.PutUint64(raw[8:], generator.counter.Add(1))
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}
