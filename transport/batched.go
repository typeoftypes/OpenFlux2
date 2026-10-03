package transport

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/p1neappleXpress/OpenFlux/utils"
)

// Defaults for the coalescing layer. Tunable at runtime via env vars so the
// batch size can be matched to the channel's per-message limits without a
// rebuild (OPENFLUX_BATCH_BYTES / OPENFLUX_BATCH_COUNT / OPENFLUX_BATCH_LINGER_MS).
const (
	defaultMaxBatchBytes = 16384
	defaultMaxBatchCount = 128
	defaultLingerMs      = 3
	batchQueueDepth      = 512 // At most 32 MiB of queued packet data.
)

// Traffic-shaping constants. See batchPad and the batch flag
// batchFlagPadded: padding rounds small frames up toward a random target so
// the payload-size distribution stops tracking the tunneled traffic.
const (
	defaultJitterMs    = 2
	defaultPadMinBytes = 0
	defaultPadMaxBytes = 64
)

// PadConfig controls traffic shaping of the batched layer. Padding turns the
// payload-size distribution from a sharp, traffic-correlated signal into a
// less precise size signal. This does not guarantee resistance to traffic
// classification. Padding is opt-in and requires updated decoders on BOTH
// peers: older wire-v2 implementations reject the padding flag.
type PadConfig struct {
	Enabled bool
	Min     int
	Max     int
}

// batchPad returns the number of random padding bytes to append to this
// frame, or 0 when padding is disabled. Small frames get the most padding
// (they leak the most about the underlying traffic), full-size frames none.
func batchPad(conf PadConfig, payloadLen int) int {
	if !conf.Enabled || conf.Max <= 0 {
		return 0
	}
	if payloadLen >= conf.Max {
		return 0
	}
	room := conf.Max - payloadLen
	if room <= 0 {
		return 0
	}
	lo := conf.Min
	if lo > room {
		lo = room
	}
	hi := room
	if hi <= 0 {
		return 0
	}
	n := lo
	if hi > lo {
		n += rand.Intn(hi - lo + 1)
	}
	return n
}

// padEnv reads an env-var pair of the form "min,max" (or a single number,
// taken as max with min 0) and returns the padding configuration it spells.
func padEnv(name string) PadConfig {
	v := os.Getenv(name)
	if v == "" {
		return PadConfig{Enabled: false}
	}
	conf := PadConfig{Enabled: true, Min: defaultPadMinBytes, Max: defaultPadMaxBytes}
	if i := strings.Index(v, ","); i >= 0 {
		if n, err := strconv.Atoi(v[:i]); err == nil && n >= 0 {
			conf.Min = n
		}
		if n, err := strconv.Atoi(v[i+1:]); err == nil && n >= 0 {
			conf.Max = n
		}
	} else if n, err := strconv.Atoi(v); err == nil && n > 0 {
		conf.Max = n
	}
	if conf.Max < conf.Min {
		conf.Min, conf.Max = conf.Max, conf.Min
	}
	// The trailer has a uint16 length and the frame has a bounded size.
	conf.Max = min(conf.Max, 65535)
	conf.Min = min(conf.Min, conf.Max)
	if conf.Max <= 0 {
		conf = PadConfig{Enabled: false}
	}
	return conf
}

// BatchedTransport replaces the old per-packet CompressedTransport. It queues
// outgoing tunnel packets, coalesces bursts into a single framed+zstd batch per
// inner transport message, and splits batches back into packets on receive.
//
// This is the symmetric layer: client and exit node must both use it (they do,
// because main.go wraps both the same way).
//
// BatchedTransport speaks wire-format v2 only. Capability negotiation lives
// in NegotiatedTransport (transport/negotiated.go); the retired wire-v3
// prototype is no longer supported and fails startup if forced.
type BatchedTransport struct {
	Transport

	queue         chan []byte
	lingerMs      int
	maxBatchBytes int
	maxBatchCount int
	pad           PadConfig

	running    atomic.Bool
	lifecycle  sync.Mutex
	stopOnce   sync.Once
	stopCh     chan struct{}
	sendErrors atomic.Uint64

	mu     sync.RWMutex
	userCb func([]byte)
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func NewBatchedTransport(inner Transport) *BatchedTransport {
	b := &BatchedTransport{
		Transport:     inner,
		queue:         make(chan []byte, batchQueueDepth),
		lingerMs:      envInt("OPENFLUX_BATCH_LINGER_MS", defaultLingerMs),
		maxBatchBytes: min(envInt("OPENFLUX_BATCH_BYTES", defaultMaxBatchBytes), maxFrameBytes-65537),
		maxBatchCount: min(envInt("OPENFLUX_BATCH_COUNT", defaultMaxBatchCount), maxFrameRecords-1),
		pad:           padEnv("OPENFLUX_BATCH_PAD"),
		stopCh:        make(chan struct{}),
	}
	utils.Debugf("[BATCH] created: lingerMs=%d maxBatchBytes=%d maxBatchCount=%d queueCap=%d padding=%v",
		b.lingerMs, b.maxBatchBytes, b.maxBatchCount, batchQueueDepth, b.pad)
	return b
}

func (b *BatchedTransport) Start() error {
	utils.Debugf("[BATCH] Start() called")
	b.lifecycle.Lock()
	defer b.lifecycle.Unlock()
	if os.Getenv("OPENFLUX_EXPERIMENTAL_WIRE_V3") == "1" {
		return fmt.Errorf("unauthenticated wire-v3 negotiation has been retired; unset OPENFLUX_EXPERIMENTAL_WIRE_V3 and use --negotiate with encryption on both peers")
	}
	select {
	case <-b.stopCh:
		return fmt.Errorf("batched transport is stopped")
	default:
	}
	if b.running.Load() {
		utils.Debugf("[BATCH] Start: already running")
		return nil
	}
	utils.Debugf("[BATCH] Start: starting inner transport")
	if err := b.Transport.Start(); err != nil {
		utils.Debugf("[BATCH] Start: inner Start failed: %v", err)
		return err
	}
	b.running.Store(true)
	go b.flushLoop()
	utils.Debugf("[BATCH] Start: OK")
	return nil
}

func (b *BatchedTransport) Stop() error {
	utils.Debugf("[BATCH] Stop() called")
	b.lifecycle.Lock()
	defer b.lifecycle.Unlock()
	select {
	case <-b.stopCh:
		return nil
	default:
	}
	b.running.Store(false)
	b.stopOnce.Do(func() { close(b.stopCh) })
	return b.Transport.Stop()
}

// Send copies the packet (the caller's buffer may be reused) and enqueues it
// for batching. A full queue returns an explicit error; TCP may retransmit,
// while UDP callers must treat it as datagram loss.
func (b *BatchedTransport) Send(data []byte) error {
	if !b.running.Load() {
		utils.Debugf("[BATCH] Send: not running, dropping %d bytes", len(data))
		return fmt.Errorf("batched transport is not running")
	}
	if len(data) > 65535 {
		utils.Debugf("[BATCH] Send: packet too large %d", len(data))
		return fmt.Errorf("packet too large for batch record: %d bytes", len(data))
	}
	p := make([]byte, len(data))
	copy(p, data)
	select {
	case b.queue <- p:
		utils.Debugf("[BATCH] Send: enqueued %d bytes (queue %d/%d)",
			len(p), len(b.queue), cap(b.queue))
		return nil
	default:
		// Backpressure: try once more with a brief wait before dropping,
		// giving the flush loop time to drain. This reduces packet loss
		// during traffic bursts significantly.
		select {
		case b.queue <- p:
			utils.Debugf("[BATCH] Send: enqueued %d bytes after brief wait (queue %d/%d)",
				len(p), len(b.queue), cap(b.queue))
			return nil
		case <-time.After(2 * time.Millisecond):
			utils.Debugf("[BATCH] Send: QUEUE FULL, dropped %d bytes", len(p))
			return fmt.Errorf("batch queue full")
		case <-b.stopCh:
			return fmt.Errorf("batched transport is stopped")
		}
	}
}

func (b *BatchedTransport) Receive(callback func([]byte)) {
	utils.Debugf("[BATCH] Receive: callback installed")
	b.mu.Lock()
	b.userCb = callback
	b.mu.Unlock()

	// Frames here are plaintext and can carry control messages with cookie
	// jars, so their hexdumps need --sensitive as well as -ddd.
	b.Transport.Receive(func(data []byte) {
		utils.Debugf("[BATCH] Recv: %d wire bytes", len(data))
		if utils.IsVerbose() && utils.Sensitive() {
			utils.Debugf("[BATCH] Recv wire hexdump:\n%s", hex.Dump(data))
		}
		pkts, err := decodeBatch(data)
		if err != nil {
			utils.Debugf("[BATCH] decode error (%d bytes): %v", len(data), err)
			if utils.IsVerbose() && utils.Sensitive() {
				utils.Debugf("[BATCH] bad frame hexdump:\n%s", hex.Dump(data))
			}
			return
		}
		utils.Debugf("[BATCH] Recv: decoded %d packets", len(pkts))
		b.mu.RLock()
		cb := b.userCb
		b.mu.RUnlock()
		if cb == nil {
			utils.Debugf("[BATCH] Recv: no callback, dropping %d packets", len(pkts))
			return
		}
		for i, p := range pkts {
			utils.Debugf("[BATCH] Recv: delivering packet %d/%d size=%d", i+1, len(pkts), len(p))
			if utils.IsVerbose() && utils.Sensitive() {
				utils.Debugf("[BATCH] packet %d hexdump:\n%s", i+1, hex.Dump(p))
			}
			cb(p)
		}
	})
}

// SendErrors counts Transport.Send failures observed by the batching layer.
func (b *BatchedTransport) SendErrors() uint64 { return b.sendErrors.Load() }

func (b *BatchedTransport) recordSendError(err error) {
	b.sendErrors.Add(1)
	utils.Debugf("[BATCH] send error (total=%d): %v", b.sendErrors.Load(), err)
}

func (b *BatchedTransport) flushLoop() {
	utils.Debugf("[BATCH] flushLoop: started")
	for b.running.Load() {
		var first []byte
		select {
		case <-b.stopCh:
			utils.Debugf("[BATCH] flushLoop: stop signal, exiting")
			return
		case first = <-b.queue:
			utils.Debugf("[BATCH] flushLoop: dequeued first packet size=%d (queue %d/%d)",
				len(first), len(b.queue), cap(b.queue))
		}
		batch := [][]byte{first}
		size := 2 + len(first)

		// Phase 1: absorb everything already queued (burst coalescing). This
		// alone collapses a window's worth of segments into one message.
	drainNow:
		for size < b.maxBatchBytes && len(batch) < b.maxBatchCount {
			select {
			case p := <-b.queue:
				batch = append(batch, p)
				size += 2 + len(p)
			default:
				break drainNow
			}
		}
		utils.Debugf("[BATCH] flushLoop: phase1 drained to %d packets, %d bytes", len(batch), size)

		// Phase 2: brief linger to catch stragglers arriving just after the
		// burst. Negligible next to the channel RTT, but it fills batches
		// during steady bulk transfer. A small random jitter (-50%..+50%) is
		// added so the inter-message spacing does not settle into a fixed
		// cadence, which is a recognizable fingerprint of a tunneled flow.
		if b.lingerMs > 0 && size < b.maxBatchBytes && len(batch) < b.maxBatchCount {
			linger := b.lingerMs
			if jitter := envInt("OPENFLUX_BATCH_JITTER_MS", defaultJitterMs); jitter > 0 {
				linger += rand.Intn(2*jitter+1) - jitter
				if linger < 0 {
					linger = 0
				}
			}
			timer := time.NewTimer(time.Duration(linger) * time.Millisecond)
		linger:
			for size < b.maxBatchBytes && len(batch) < b.maxBatchCount {
				select {
				case <-b.stopCh:
					timer.Stop()
					utils.Debugf("[BATCH] flushLoop: stop during linger, exiting")
					return
				case p := <-b.queue:
					batch = append(batch, p)
					size += 2 + len(p)
				case <-timer.C:
					break linger
				}
			}
			timer.Stop()
			utils.Debugf("[BATCH] flushLoop: phase2 linger(%dms) done, %d packets, %d bytes",
				linger, len(batch), size)
		}

		encoded := encodeBatchWithPad(batch, b.pad)
		utils.Debugf("[BATCH] flushLoop: sending batch of %d packets (%d raw -> %d wire bytes)",
			len(batch), size, len(encoded))
		if utils.IsVerbose() && utils.Sensitive() {
			utils.Debugf("[BATCH] flushLoop: batch hexdump:\n%s", hex.Dump(encoded))
		}
		if err := b.Transport.Send(encoded); err != nil {
			b.recordSendError(err)
		} else {
			utils.Debugf("[BATCH] flushLoop: batch sent OK")
		}
	}
	utils.Debugf("[BATCH] flushLoop: exit (running=false)")
}
