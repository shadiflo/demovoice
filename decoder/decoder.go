package decoder

import (
	"bytes"
	"encoding/binary"
	"gopkg.in/hraban/opus.v2"
	"sync"
)

const (
	FrameSize = 480
)

// Buffer pools to reduce allocations
var (
	float32Pool = sync.Pool{
		New: func() interface{} {
			buf := make([]float32, FrameSize)
			return &buf
		},
	}
	chunkPool = sync.Pool{
		New: func() interface{} {
			buf := make([]byte, 1024)
			return &buf
		},
	}
)

type OpusDecoder struct {
	decoder *opus.Decoder

	currentFrame uint16
	// Reusable decode buffer
	decodeBuf []float32
}

func NewOpusDecoder(sampleRate, channels int) (*OpusDecoder, error) {
	decoder, err := opus.NewDecoder(sampleRate, channels)

	if err != nil {
		return nil, err
	}

	return &OpusDecoder{
		decoder:      decoder,
		currentFrame: 0,
		decodeBuf:    make([]float32, FrameSize),
	}, nil
}

func (d *OpusDecoder) Decode(b []byte) ([]float32, error) {
	buf := bytes.NewBuffer(b)

	output := make([]float32, 0, 1024)

	for buf.Len() != 0 {
		var chunkLen int16
		if err := binary.Read(buf, binary.LittleEndian, &chunkLen); err != nil {
			return nil, err
		}

		if chunkLen == -1 {
			d.currentFrame = 0
			break
		}

		var currentFrame uint16
		if err := binary.Read(buf, binary.LittleEndian, &currentFrame); err != nil {
			return nil, err
		}

		previousFrame := d.currentFrame

		chunk := make([]byte, chunkLen)
		n, err := buf.Read(chunk)
		if err != nil {
			return nil, err
		}

		if n != int(chunkLen) {
			return nil, ErrInvalidVoicePacket
		}

		if currentFrame >= previousFrame {
			if currentFrame == previousFrame {
				d.currentFrame = currentFrame + 1

				decoded, err := d.decodeSteamChunk(chunk)

				if err != nil {
					return nil, err
				}

				output = append(output, decoded...)
			} else {
				decoded, err := d.decodeLoss(currentFrame - previousFrame)

				if err != nil {
					return nil, err
				}

				output = append(output, decoded...)
			}
		}
	}

	return output, nil
}

func (d *OpusDecoder) decodeSteamChunk(b []byte) ([]float32, error) {
	n, err := d.decoder.DecodeFloat32(b, d.decodeBuf)

	if err != nil {
		return nil, err
	}

	// Return a copy since we reuse the buffer
	result := make([]float32, n)
	copy(result, d.decodeBuf[:n])
	return result, nil
}

func (d *OpusDecoder) decodeLoss(samples uint16) ([]float32, error) {
	loss := min(samples, 10)

	o := make([]float32, 0, FrameSize*loss)

	for i := 0; i < int(loss); i += 1 {
		t := make([]float32, FrameSize)

		if err := d.decoder.DecodePLCFloat32(t); err != nil {
			return nil, err
		}

		o = append(o, t...)
	}

	return o, nil
}

func NewDecoder(sampleRate, channels int) (decoder *opus.Decoder, err error) {
	decoder, err = opus.NewDecoder(sampleRate, channels)
	return
}

func Decode(decoder *opus.Decoder, data []byte) (pcm []float32, err error) {
	pcm = make([]float32, 1024)

	nlen, err := decoder.DecodeFloat32(data, pcm)
	if err != nil {
		return
	}

	return pcm[:nlen], nil
}
