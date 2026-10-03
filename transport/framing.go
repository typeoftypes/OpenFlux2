package transport

import (
	"encoding/binary"
	"fmt"

	"github.com/klauspost/compress/zstd"
)

// Wire format for a batched frame (one Yandex/transport message can now carry
// many tunnel packets):
//
//	[0]   version byte (batchFormatVersion)
//	[1]   flags (bit0 = payload is zstd-compressed)
//	[2:]  payload: a sequence of [2-byte big-endian length][packet] records,
//	      optionally zstd-compressed as a whole.
//
// Only wire-v2 frames are accepted. The retired wire-v3 prototype is gone:
// capability negotiation now lives in NegotiatedTransport (transport/negotiated.go),
// inside the authenticated envelope.
const (
	batchFormatVersion = 0x02
	batchFlagZstd      = 0x01
	batchFlagPadded    = 0x02
	maxFrameBytes      = 1 << 20
	maxFrameRecords    = 1024
)

var (
	zstdEnc *zstd.Encoder
	zstdDec *zstd.Decoder
)

func init() {
	var err error
	// SpeedDefault (~level 3): far better ratio than LZ4 at a CPU cost that is
	// irrelevant next to the Yandex channel's latency. Single-shot EncodeAll /
	// DecodeAll are safe for concurrent use on a shared instance.
	zstdEnc, err = zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(1),
	)
	if err != nil {
		panic(fmt.Sprintf("zstd encoder init: %v", err))
	}
	zstdDec, err = zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		// Bound the damage from a malformed/hostile frame injected into the
		// shared document: cap decompressed memory.
		zstd.WithDecoderMaxMemory(8<<20),
	)
	if err != nil {
		panic(fmt.Sprintf("zstd decoder init: %v", err))
	}
}

// frameBatch concatenates packets into length-prefixed records.
func frameBatch(pkts [][]byte) []byte {
	total := 0
	for _, p := range pkts {
		total += 2 + len(p)
	}
	out := make([]byte, 0, total)
	var lenbuf [2]byte
	for _, p := range pkts {
		binary.BigEndian.PutUint16(lenbuf[:], uint16(len(p)))
		out = append(out, lenbuf[:]...)
		out = append(out, p...)
	}
	return out
}

// encodeBatch serializes packets into a single wire-v2 frame, compressing
// the whole batch with zstd only when that actually shrinks it.
func encodeBatch(pkts [][]byte) []byte {
	return encodeBatchWithPad(pkts, PadConfig{})
}

func encodeBatchWithPad(pkts [][]byte, pad PadConfig) []byte {
	framed := frameBatch(pkts)
	compressed := zstdEnc.EncodeAll(framed, nil)

	if len(compressed) < len(framed) {
		padding := batchPad(pad, 2+len(compressed)+2)
		flags := byte(batchFlagZstd)
		if padding > 0 {
			flags |= batchFlagPadded
		}
		out := make([]byte, 2, 2+len(compressed)+2+padding)
		out[0] = batchFormatVersion
		out[1] = flags
		out = append(out, compressed...)
		return appendPadding(out, padding)
	}
	padding := batchPad(pad, 2+len(framed)+2)
	flags := byte(0)
	if padding > 0 {
		flags |= batchFlagPadded
	}
	out := make([]byte, 2, 2+len(framed)+2+padding)
	out[0] = batchFormatVersion
	out[1] = flags
	out = append(out, framed...)
	return appendPadding(out, padding)
}

func appendPadding(out []byte, n int) []byte {
	if n <= 0 {
		return out
	}
	// A two-byte trailer stores the padding length. Padding is deliberately
	// outside compression so it does not alter compression characteristics.
	for i := 0; i < n; i++ {
		out = append(out, byte(i*31+17))
	}
	return append(out, byte(n>>8), byte(n))
}

// decodeBatch reverses encodeBatch, returning the original packets.
// Only wire-v2 frames are accepted; anything else is rejected.
func decodeBatch(data []byte) ([][]byte, error) {
	if len(data) > maxFrameBytes+2 {
		return nil, fmt.Errorf("batch frame exceeds size limit")
	}
	if len(data) < 2 {
		return nil, fmt.Errorf("batch frame too short: %d bytes", len(data))
	}
	if data[0] != batchFormatVersion {
		return nil, fmt.Errorf("unknown batch version 0x%02x", data[0])
	}
	flags := data[1]
	if flags & ^byte(batchFlagZstd|batchFlagPadded) != 0 {
		return nil, fmt.Errorf("unknown batch flags 0x%02x", flags)
	}
	payload := data[2:]
	if flags&batchFlagPadded != 0 {
		if len(payload) < 2 {
			return nil, fmt.Errorf("padded batch has no padding length")
		}
		n := int(binary.BigEndian.Uint16(payload[len(payload)-2:]))
		if n < 1 || n+2 > len(payload) {
			return nil, fmt.Errorf("invalid batch padding length %d", n)
		}
		payload = payload[:len(payload)-n-2]
	}

	framed := payload
	if flags&batchFlagZstd != 0 {
		var err error
		framed, err = zstdDec.DecodeAll(payload, nil)
		if err != nil {
			return nil, fmt.Errorf("zstd decode: %w", err)
		}
	}
	if len(framed) > maxFrameBytes {
		return nil, fmt.Errorf("decoded batch exceeds size limit")
	}

	var pkts [][]byte
	for len(framed) > 0 {
		if len(pkts) >= maxFrameRecords {
			return nil, fmt.Errorf("batch exceeds record limit")
		}
		if len(framed) < 2 {
			return nil, fmt.Errorf("truncated length prefix")
		}
		n := int(binary.BigEndian.Uint16(framed[:2]))
		framed = framed[2:]
		if len(framed) < n {
			return nil, fmt.Errorf("truncated packet: need %d, have %d", n, len(framed))
		}
		pkt := make([]byte, n)
		copy(pkt, framed[:n])
		pkts = append(pkts, pkt)
		framed = framed[n:]
	}
	return pkts, nil
}
