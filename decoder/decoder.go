package decoder

import (
	"encoding/binary"
	"gopkg.in/hraban/opus.v2"
)

const (
	FrameSize = 480
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
	return d.DecodeInto(b, make([]float32, 0, 1024))
}

func (d *OpusDecoder) DecodeInto(b []byte, output []float32) ([]float32, error) {
	for len(b) != 0 {
		if len(b) < 2 {
			return nil, ErrInvalidVoicePacket
		}

		chunkLen := int16(binary.LittleEndian.Uint16(b[:2]))
		b = b[2:]

		if chunkLen == -1 {
			d.currentFrame = 0
			break
		}

		if chunkLen < 0 {
			return nil, ErrInvalidVoicePacket
		}

		if len(b) < 2 {
			return nil, ErrInvalidVoicePacket
		}

		currentFrame := binary.LittleEndian.Uint16(b[:2])
		b = b[2:]

		if len(b) < int(chunkLen) {
			return nil, ErrInvalidVoicePacket
		}

		chunk := b[:chunkLen]
		b = b[chunkLen:]

		previousFrame := d.currentFrame

		if currentFrame >= previousFrame {
			if currentFrame > previousFrame {
				var err error
				output, err = d.decodeLossInto(currentFrame-previousFrame, output)
				if err != nil {
					return nil, err
				}
			}

			var err error
			output, err = d.decodeSteamChunkInto(chunk, output)
			if err != nil {
				return nil, err
			}
			d.currentFrame = currentFrame + 1
		}
	}

	return output, nil
}

func (d *OpusDecoder) decodeSteamChunk(b []byte) ([]float32, error) {
	return d.decodeSteamChunkInto(b, make([]float32, 0, FrameSize))
}

func (d *OpusDecoder) decodeSteamChunkInto(b []byte, output []float32) ([]float32, error) {
	n, err := d.decoder.DecodeFloat32(b, d.decodeBuf)
	if err != nil {
		return nil, err
	}

	return append(output, d.decodeBuf[:n]...), nil
}

func (d *OpusDecoder) decodeLoss(samples uint16) ([]float32, error) {
	return d.decodeLossInto(samples, make([]float32, 0, FrameSize*int(samples)))
}

func (d *OpusDecoder) decodeLossInto(samples uint16, output []float32) ([]float32, error) {
	loss := min(samples, 10)

	for i := 0; i < int(loss); i += 1 {
		if err := d.decoder.DecodePLCFloat32(d.decodeBuf); err != nil {
			return nil, err
		}

		output = append(output, d.decodeBuf...)
	}

	return output, nil
}

type RawOpusDecoder struct {
	decoder   *opus.Decoder
	decodeBuf []float32
}

func NewRawOpusDecoder(sampleRate, channels int) (*RawOpusDecoder, error) {
	decoder, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		return nil, err
	}

	return &RawOpusDecoder{
		decoder:   decoder,
		decodeBuf: make([]float32, 5760*channels),
	}, nil
}

func (d *RawOpusDecoder) DecodeInto(data []byte, output []float32) ([]float32, error) {
	n, err := d.decoder.DecodeFloat32(data, d.decodeBuf)
	if err != nil {
		return nil, err
	}

	return append(output, d.decodeBuf[:n]...), nil
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
