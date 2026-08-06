package terminal

// mouseFilter splits a PTY input stream into plain byte segments and parsed
// VT mouse events (SGR 1006 and legacy X10). It exists for Windows, where
// conhost builds before 22621 silently drop VT mouse sequences written to
// the ConPTY input pipe: the daemon must intercept them and inject native
// MOUSE_EVENT input records instead (microsoft/terminal#376). The parsing
// itself is platform independent so it can be unit tested anywhere.
type mouseFilter struct {
	pending []byte // partial mouse sequence carried across Write calls
}

// mouseSeqEvent is one decoded VT mouse event.
type mouseSeqEvent struct {
	// btn uses SGR semantics: bits 0-1 button (3 = none), +4 shift,
	// +8 meta, +16 ctrl, +32 motion, +64 wheel.
	btn     int
	x, y    int // 1-based cell coordinates
	release bool
	raw     []byte // original sequence, for passthrough
}

// mouseSegment is either plain input bytes (ev == nil) or a mouse event.
type mouseSegment struct {
	data []byte
	ev   *mouseSeqEvent
}

const maxPendingMouseSeq = 24

// feed consumes the next chunk of input and returns it split into segments.
// A mouse sequence truncated at the end of the chunk is buffered until the
// next call. Ambiguous prefixes ("\x1b", "\x1b[") are NOT buffered — they
// are far more likely to be keys, and delaying keys is worse than missing
// a split mouse event.
func (f *mouseFilter) feed(p []byte) []mouseSegment {
	buf := p
	if len(f.pending) > 0 {
		buf = append(f.pending, p...)
		f.pending = nil
	}

	var segs []mouseSegment
	plainStart := 0
	i := 0
	flushPlain := func(end int) {
		if end > plainStart {
			segs = append(segs, mouseSegment{data: buf[plainStart:end]})
		}
	}

	for i < len(buf) {
		if buf[i] != 0x1b {
			i++
			continue
		}
		if i+2 >= len(buf) {
			// "\x1b" or "\x1b[" at the end of the chunk: ambiguous with
			// key escapes, pass through rather than delay keys.
			i++
			continue
		}
		if buf[i+1] != '[' {
			i++
			continue
		}
		switch buf[i+2] {
		case '<':
			ev, n, incomplete := parseSGRMouse(buf[i:])
			if incomplete {
				flushPlain(i)
				f.pending = append(f.pending, buf[i:]...)
				return segs
			}
			if ev == nil {
				i++
				continue
			}
			flushPlain(i)
			segs = append(segs, mouseSegment{ev: ev})
			i += n
			plainStart = i
		case 'M':
			if len(buf)-i < 6 {
				flushPlain(i)
				f.pending = append(f.pending, buf[i:]...)
				return segs
			}
			ev := parseX10Mouse(buf[i : i+6])
			flushPlain(i)
			segs = append(segs, mouseSegment{ev: ev})
			i += 6
			plainStart = i
		default:
			i++
		}
	}
	flushPlain(len(buf))
	return segs
}

// parseSGRMouse parses "\x1b[<b;x;yM" / "...m" at the start of buf.
// Returns (nil, _, true) when buf ends mid-sequence, (nil, _, false) when
// the bytes are not a valid SGR mouse sequence.
func parseSGRMouse(buf []byte) (*mouseSeqEvent, int, bool) {
	// buf[0:3] == ESC [ <
	i := 3
	var nums [3]int
	for n := 0; n < 3; n++ {
		start := i
		v := 0
		for i < len(buf) && buf[i] >= '0' && buf[i] <= '9' {
			v = v*10 + int(buf[i]-'0')
			i++
		}
		if i == start {
			if i >= len(buf) && i <= maxPendingMouseSeq {
				return nil, 0, true
			}
			return nil, 0, false
		}
		nums[n] = v
		if n < 2 {
			if i >= len(buf) {
				return nil, 0, i <= maxPendingMouseSeq
			}
			if buf[i] != ';' {
				return nil, 0, false
			}
			i++
		}
	}
	if i >= len(buf) {
		return nil, 0, i <= maxPendingMouseSeq
	}
	if buf[i] != 'M' && buf[i] != 'm' {
		return nil, 0, false
	}
	ev := &mouseSeqEvent{
		btn:     nums[0],
		x:       nums[1],
		y:       nums[2],
		release: buf[i] == 'm',
		raw:     append([]byte(nil), buf[:i+1]...),
	}
	return ev, i + 1, false
}

// parseX10Mouse parses the 6-byte legacy "\x1b[Mbxy" encoding.
func parseX10Mouse(seq []byte) *mouseSeqEvent {
	btn := int(seq[3]) - 32
	ev := &mouseSeqEvent{
		btn: btn,
		x:   int(seq[4]) - 32,
		y:   int(seq[5]) - 32,
		raw: append([]byte(nil), seq...),
	}
	// X10 encodes release as button 3 with no wheel/motion bits.
	if btn&0x43 == 3 {
		ev.release = true
	}
	return ev
}
