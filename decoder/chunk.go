package decoder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

const (
	minimumLength = 18
)

var (
	ErrInsufficientData   = errors.New("insufficient amount of data to chunk")
	ErrInvalidVoicePacket = errors.New("invalid voice packet")
	ErrMismatchChecksum   = errors.New("mismatching voice data checksum")
)

type Chunk struct {
	SteamID    uint64
	SampleRate uint16
	Length     uint16
	Data       []byte
	Checksum   uint32
}

func DecodeChunk(b []byte) (*Chunk, error) {
	bLen := len(b)

	if bLen < minimumLength {
		return nil, fmt.Errorf("%w (received: %d bytes, expected at least %d bytes)", ErrInsufficientData, bLen, minimumLength)
	}

	chunk := &Chunk{}
	offset := 0

	chunk.SteamID = binary.LittleEndian.Uint64(b[offset:])
	offset += 8

	payloadType := b[offset]
	offset++

	if payloadType != 0x0B {
		return nil, fmt.Errorf("%w (received %x, expected %x)", ErrInvalidVoicePacket, payloadType, 0x0B)
	}

	chunk.SampleRate = binary.LittleEndian.Uint16(b[offset:])
	offset += 2

	voiceType := b[offset]
	offset++

	chunk.Length = binary.LittleEndian.Uint16(b[offset:])
	offset += 2

	switch voiceType {
	case 0x6:
		remaining := bLen - offset - 4
		chunkLen := int(chunk.Length)

		if remaining < chunkLen {
			return nil, fmt.Errorf("%w (received: %d bytes, expected at least %d bytes)", ErrInsufficientData, bLen, (bLen + (chunkLen - remaining)))
		}

		chunk.Data = b[offset : offset+chunkLen]
		offset += chunkLen
	case 0x0:
		// no-op, detect silence if chunk.Data is empty
		// the length would the number of silence frames
	default:
		return nil, fmt.Errorf("%w (expected 0x6 or 0x0 voice data, received %x)", ErrInvalidVoicePacket, voiceType)
	}

	remaining := bLen - offset

	if remaining != 4 {
		return nil, fmt.Errorf("%w (has %d bytes remaining, expected 4 bytes remaining)", ErrInvalidVoicePacket, remaining)
	}

	chunk.Checksum = binary.LittleEndian.Uint32(b[offset:])

	actualChecksum := crc32.ChecksumIEEE(b[0 : bLen-4])

	if chunk.Checksum != actualChecksum {
		return nil, fmt.Errorf("%w (received %x, expected %x)", ErrMismatchChecksum, chunk.Checksum, actualChecksum)
	}

	return chunk, nil
}
