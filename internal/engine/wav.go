package engine

import (
	"bufio"
	"encoding/binary"
	"os"

	"github.com/susynth/susynth/internal/pcm"
)

// WavWriter streams stereo 24-bit PCM to a WAV file (dithered from the
// engine's float output) and patches the header on Close. 24 bits keep
// everything the engine makes: the floor sits ~144 dB down.
type WavWriter struct {
	f      *os.File
	w      *bufio.Writer
	enc    *pcm.Encoder
	sr     int
	frames int64
	buf    []byte
}

const wavBytesPerFrame = 6 // two 24-bit samples

// NewWavWriter creates path and writes a placeholder header.
func NewWavWriter(path string, sr int) (*WavWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &WavWriter{f: f, w: bufio.NewWriterSize(f, 1<<16), enc: pcm.NewEncoder(pcm.Int24), sr: sr}
	if _, err := f.Write(w.header()); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

func (w *WavWriter) header() []byte {
	data := uint32(w.frames * wavBytesPerFrame)
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 36+data)
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:], 2) // channels
	binary.LittleEndian.PutUint32(h[24:], uint32(w.sr))
	binary.LittleEndian.PutUint32(h[28:], uint32(w.sr*wavBytesPerFrame))
	binary.LittleEndian.PutUint16(h[32:], wavBytesPerFrame)
	binary.LittleEndian.PutUint16(h[34:], 24)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], data)
	return h
}

// Write appends interleaved stereo samples (full scale ±1).
func (w *WavWriter) Write(s []float32) error {
	w.buf = w.enc.Append(w.buf[:0], s)
	if _, err := w.w.Write(w.buf); err != nil {
		return err
	}
	w.frames += int64(len(s) / 2)
	return nil
}

// Frames returns the number of stereo frames written so far.
func (w *WavWriter) Frames() int64 { return w.frames }

// Close patches the header and closes the file.
func (w *WavWriter) Close() error {
	err := w.w.Flush()
	if _, herr := w.f.WriteAt(w.header(), 0); err == nil {
		err = herr
	}
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	return err
}
