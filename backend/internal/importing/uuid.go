package importing

import (
	"crypto/rand"
	"fmt"
	"io"
)

type uuidGenerator func() (string, error)

func randomUUIDv4() (string, error) {
	return uuidV4From(rand.Reader)
}

func uuidV4From(source io.Reader) (string, error) {
	if source == nil {
		return "", importError(ErrorUUIDGeneration, false, "generate UUID", fmt.Errorf("random source is required"))
	}
	var value [16]byte
	if _, err := io.ReadFull(source, value[:]); err != nil {
		return "", importError(ErrorUUIDGeneration, false, "generate UUID", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	), nil
}
