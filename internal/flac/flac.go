// Package flac decodes FLAC audio: the format Sonic Pi ships its samples in.
// It handles every stream the reference encoder writes (fixed and LPC
// predictors, Rice-coded residuals, stereo decorrelation, any sample size up
// to 32 bits). The input is untrusted, so every read is bounds-checked and a
// bad stream is an error, never a panic.
package flac

import (
	"errors"
	"fmt"
)

// Stream is a decoded FLAC file. Samples[c][i] is sample i of channel c, as
// an integer of Bits bits.
type Stream struct {
	Rate     int
	Channels int
	Bits     int
	Samples  [][]int32
}

// Info is a FLAC file's STREAMINFO: enough to know its length without
// decoding it.
type Info struct {
	Rate, Channels, Bits int
	Frames               int64 // samples per channel (0: unknown)
}

// Seconds is the stream's length.
func (i Info) Seconds() float64 {
	if i.Rate <= 0 {
		return 0
	}
	return float64(i.Frames) / float64(i.Rate)
}

var errShort = errors.New("flac: truncated stream")

// ReadInfo reads STREAMINFO from the start of a FLAC file.
func ReadInfo(b []byte) (Info, error) {
	var in Info
	if len(b) < 4 || string(b[:4]) != "fLaC" {
		return in, errors.New("flac: not a FLAC file")
	}
	p := 4
	for {
		if p+4 > len(b) {
			return in, errShort
		}
		last := b[p]&0x80 != 0
		typ := b[p] & 0x7f
		n := int(b[p+1])<<16 | int(b[p+2])<<8 | int(b[p+3])
		p += 4
		if p+n > len(b) {
			return in, errShort
		}
		if typ == 0 {
			if n < 34 {
				return in, errors.New("flac: short STREAMINFO")
			}
			s := b[p:]
			in.Rate = int(s[10])<<12 | int(s[11])<<4 | int(s[12])>>4
			in.Channels = int(s[12]>>1&7) + 1
			in.Bits = (int(s[12]&1)<<4 | int(s[13])>>4) + 1
			in.Frames = int64(s[13]&0xf)<<32 | int64(s[14])<<24 | int64(s[15])<<16 | int64(s[16])<<8 | int64(s[17])
			return in, nil
		}
		p += n
		if last {
			return in, errors.New("flac: no STREAMINFO")
		}
	}
}

// Decode decodes a whole FLAC file.
func Decode(b []byte) (*Stream, error) {
	in, err := ReadInfo(b)
	if err != nil {
		return nil, err
	}
	if in.Rate <= 0 || in.Bits < 4 || in.Bits > 32 {
		return nil, fmt.Errorf("flac: unsupported stream (%d Hz, %d-bit)", in.Rate, in.Bits)
	}
	// skip the metadata blocks
	p := 4
	for {
		if p+4 > len(b) {
			return nil, errShort
		}
		last := b[p]&0x80 != 0
		n := int(b[p+1])<<16 | int(b[p+2])<<8 | int(b[p+3])
		p += 4 + n
		if p > len(b) {
			return nil, errShort
		}
		if last {
			break
		}
	}
	st := &Stream{Rate: in.Rate, Channels: in.Channels, Bits: in.Bits, Samples: make([][]int32, in.Channels)}
	if in.Frames > 0 && in.Frames < 1<<28 {
		for c := range st.Samples {
			st.Samples[c] = make([]int32, 0, in.Frames)
		}
	}
	r := &reader{b: b, pos: p * 8}
	for r.pos/8+2 <= len(b) {
		r.align()
		if r.pos/8+2 > len(b) {
			break
		}
		// a frame starts with the sync code 0b11111111111110
		if b[r.pos/8] != 0xff || b[r.pos/8+1]&0xfc != 0xf8 {
			return nil, fmt.Errorf("flac: lost sync at byte %d", r.pos/8)
		}
		if err := st.frame(r, in); err != nil {
			return nil, err
		}
	}
	return st, nil
}

type reader struct {
	b   []byte
	pos int // in bits
	err error
}

func (r *reader) align() { r.pos = (r.pos + 7) &^ 7 }

// bits reads n (0..32) bits, most significant first.
func (r *reader) bits(n int) uint32 {
	if n == 0 {
		return 0
	}
	if r.pos+n > len(r.b)*8 {
		r.err = errShort
		r.pos = len(r.b) * 8
		return 0
	}
	var v uint64
	end := r.pos + n
	for r.pos < end {
		byt := r.b[r.pos>>3]
		off := r.pos & 7
		take := 8 - off
		if take > end-r.pos {
			take = end - r.pos
		}
		chunk := uint64(byt>>(8-off-take)) & (1<<take - 1)
		v = v<<take | chunk
		r.pos += take
	}
	return uint32(v)
}

func (r *reader) signed(n int) int32 {
	if n == 0 {
		return 0
	}
	v := r.bits(n)
	if n < 32 && v&(1<<(n-1)) != 0 {
		v |= ^uint32(0) << n
	}
	return int32(v)
}

// unary counts zero bits before a one.
func (r *reader) unary() uint32 {
	var n uint32
	for {
		if r.pos >= len(r.b)*8 {
			r.err = errShort
			return n
		}
		byt := r.b[r.pos>>3] << (r.pos & 7)
		if byt == 0 { // the rest of this byte is zeros
			z := 8 - r.pos&7
			n += uint32(z)
			r.pos += z
			continue
		}
		for byt&0x80 == 0 {
			n++
			r.pos++
			byt <<= 1
		}
		r.pos++
		return n
	}
}

func (st *Stream) frame(r *reader, in Info) error {
	r.bits(14) // sync
	r.bits(1)
	r.bits(1) // blocking strategy
	bsCode := int(r.bits(4))
	srCode := int(r.bits(4))
	chCode := int(r.bits(4))
	ssCode := int(r.bits(3))
	r.bits(1)
	// the frame or sample number, UTF-8 style
	// (a first byte with n leading ones has n-1 continuation bytes)
	if first := r.bits(8); first&0x80 != 0 {
		for m := uint32(0x40); first&m != 0 && m > 1; m >>= 1 {
			r.bits(8)
		}
	}
	var bs int
	switch {
	case bsCode == 1:
		bs = 192
	case bsCode >= 2 && bsCode <= 5:
		bs = 576 << (bsCode - 2)
	case bsCode == 6:
		bs = int(r.bits(8)) + 1
	case bsCode == 7:
		bs = int(r.bits(16)) + 1
	case bsCode >= 8:
		bs = 256 << (bsCode - 8)
	default:
		return errors.New("flac: reserved block size")
	}
	switch srCode {
	case 12:
		r.bits(8)
	case 13, 14:
		r.bits(16)
	}
	r.bits(8) // CRC-8
	bits := in.Bits
	if ssCode != 0 {
		bits = [8]int{0, 8, 12, 0, 16, 20, 24, 32}[ssCode]
		if bits == 0 {
			return errors.New("flac: reserved sample size")
		}
	}
	nch := chCode + 1
	if chCode >= 8 {
		nch = 2
	}
	if chCode > 10 {
		return errors.New("flac: reserved channel assignment")
	}
	if nch != in.Channels {
		return errors.New("flac: frame channel count differs from the stream")
	}
	if r.err != nil {
		return r.err
	}
	ch := make([][]int32, nch)
	for c := 0; c < nch; c++ {
		b := bits
		// the side channel has one more bit
		if (chCode == 8 && c == 1) || (chCode == 9 && c == 0) || (chCode == 10 && c == 1) {
			b++
		}
		s, err := subframe(r, bs, b)
		if err != nil {
			return err
		}
		ch[c] = s
	}
	switch chCode {
	case 8: // left, side
		for i := range ch[1] {
			ch[1][i] = ch[0][i] - ch[1][i]
		}
	case 9: // side, right
		for i := range ch[0] {
			ch[0][i] += ch[1][i]
		}
	case 10: // mid, side
		for i := range ch[0] {
			mid := int64(ch[0][i])<<1 | int64(ch[1][i])&1
			side := int64(ch[1][i])
			ch[0][i] = int32((mid + side) >> 1)
			ch[1][i] = int32((mid - side) >> 1)
		}
	}
	r.align()
	r.bits(16) // CRC-16
	if r.err != nil {
		return r.err
	}
	for c := range ch {
		st.Samples[c] = append(st.Samples[c], ch[c]...)
	}
	return nil
}

func subframe(r *reader, n, bits int) ([]int32, error) {
	if r.bits(1) != 0 {
		return nil, errors.New("flac: bad subframe padding")
	}
	typ := int(r.bits(6))
	wasted := 0
	if r.bits(1) == 1 {
		wasted = int(r.unary()) + 1
		bits -= wasted
	}
	if bits <= 0 || bits > 33 {
		return nil, errors.New("flac: bad sample size")
	}
	out := make([]int32, n)
	switch {
	case typ == 0: // constant
		v := r.signed(min(bits, 32))
		for i := range out {
			out[i] = v
		}
	case typ == 1: // verbatim
		for i := range out {
			out[i] = r.signed(min(bits, 32))
		}
	case typ >= 8 && typ <= 12: // fixed predictor
		order := typ - 8
		if order > n {
			return nil, errors.New("flac: predictor order exceeds block")
		}
		for i := 0; i < order; i++ {
			out[i] = r.signed(min(bits, 32))
		}
		if err := residual(r, out, order); err != nil {
			return nil, err
		}
		fixed(out, order)
	case typ >= 32: // LPC
		order := typ - 31
		if order > n {
			return nil, errors.New("flac: predictor order exceeds block")
		}
		for i := 0; i < order; i++ {
			out[i] = r.signed(min(bits, 32))
		}
		prec := int(r.bits(4)) + 1
		if prec == 16 {
			return nil, errors.New("flac: bad coefficient precision")
		}
		shift := int(r.signed(5))
		if shift < 0 {
			return nil, errors.New("flac: negative LPC shift")
		}
		coef := make([]int32, order)
		for i := range coef {
			coef[i] = r.signed(prec)
		}
		if err := residual(r, out, order); err != nil {
			return nil, err
		}
		for i := order; i < n; i++ {
			var sum int64
			for j, c := range coef {
				sum += int64(c) * int64(out[i-1-j])
			}
			out[i] += int32(sum >> shift)
		}
	default:
		return nil, fmt.Errorf("flac: reserved subframe type %d", typ)
	}
	if r.err != nil {
		return nil, r.err
	}
	if wasted > 0 {
		for i := range out {
			out[i] <<= wasted
		}
	}
	return out, nil
}

func fixed(s []int32, order int) {
	for i := order; i < len(s); i++ {
		switch order {
		case 1:
			s[i] += s[i-1]
		case 2:
			s[i] += 2*s[i-1] - s[i-2]
		case 3:
			s[i] += 3*s[i-1] - 3*s[i-2] + s[i-3]
		case 4:
			s[i] += 4*s[i-1] - 6*s[i-2] + 4*s[i-3] - s[i-4]
		}
	}
}

// residual reads the Rice-coded prediction errors into s[order:].
func residual(r *reader, s []int32, order int) error {
	method := r.bits(2)
	if method > 1 {
		return errors.New("flac: reserved residual coding")
	}
	pbits, esc := 4, uint32(15)
	if method == 1 {
		pbits, esc = 5, 31
	}
	porder := int(r.bits(4))
	parts := 1 << porder
	n := len(s)
	if n%parts != 0 || n>>porder < order {
		return errors.New("flac: bad partition order")
	}
	i := order
	for p := 0; p < parts; p++ {
		cnt := n >> porder
		if p == 0 {
			cnt -= order
		}
		k := r.bits(pbits)
		if k == esc {
			nb := int(r.bits(5))
			for j := 0; j < cnt; j++ {
				s[i] = r.signed(nb)
				i++
			}
			continue
		}
		for j := 0; j < cnt; j++ {
			q := r.unary()
			v := q<<k | r.bits(int(k))
			s[i] = int32(v>>1) ^ -int32(v&1)
			i++
		}
		if r.err != nil {
			return r.err
		}
	}
	return nil
}
