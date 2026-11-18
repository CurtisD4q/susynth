package engine

import (
	"encoding/binary"
	"io"
	"os"
)

// WavWriter streams 16-bit stereo PCM to a WAV file and patches the header on Close.
type WavWriter struct {
	f      *os.File
	sr     int
	frames int64
}

// NewWavWriter creates path and writes a placeholder header.
func NewWavWriter(path string, sr int) (*WavWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &WavWriter{f: f, sr: sr}
	if err := w.header(); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

func (w *WavWriter) header() error {
	data := uint32(w.frames * 4)
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 36+data)
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:], 2) // channels
	binary.LittleEndian.PutUint32(h[24:], uint32(w.sr))
	binary.LittleEndian.PutUint32(h[28:], uint32(w.sr*4))
	binary.LittleEndian.PutUint16(h[32:], 4)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], data)
	_, err := w.f.WriteAt(h, 0)
	return err
}

// Write appends interleaved stereo samples.
func (w *WavWriter) Write(s []int16) error {
	buf := make([]byte, len(s)*2)
	for i, v := range s {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
	}
	if _, err := w.f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if _, err := w.f.Write(buf); err != nil {
		return err
	}
	w.frames += int64(len(s) / 2)
	return nil
}

// Frames returns the number of stereo frames written so far.
func (w *WavWriter) Frames() int64 { return w.frames }

// Close patches the header and closes the file.
func (w *WavWriter) Close() error {
	err := w.header()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	return err
}
