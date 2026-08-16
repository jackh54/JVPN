package server

import "io"

// serializedTUN serializes concurrent session writes onto a single TUN device.
type serializedTUN struct {
	dst io.Writer
	ch  chan []byte
}

func NewSerializedTUN(dst io.Writer, depth int) io.Writer {
	if depth < 64 {
		depth = 64
	}
	t := &serializedTUN{dst: dst, ch: make(chan []byte, depth)}
	go t.loop()
	return t
}

func (t *serializedTUN) Write(p []byte) (int, error) {
	buf := append([]byte(nil), p...)
	t.ch <- buf
	return len(p), nil
}

func (t *serializedTUN) loop() {
	for pkt := range t.ch {
		if _, err := t.dst.Write(pkt); err != nil {
			return
		}
	}
}
