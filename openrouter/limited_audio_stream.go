package openrouter

import (
	"fmt"
	"io"
)

type limitedAudioStream struct {
	body      io.ReadCloser
	remaining int64
}

func (s *limitedAudioStream) Read(p []byte) (int, error) {
	buffer := p
	if int64(len(buffer)) > s.remaining+1 {
		buffer = buffer[:s.remaining+1]
	}
	n, err := s.body.Read(buffer)
	if int64(n) > s.remaining {
		return 0, fmt.Errorf("generate OpenRouter speech: response exceeds %d bytes", maxAudioSize)
	}
	s.remaining -= int64(n)
	return n, err
}

func (s *limitedAudioStream) Close() error {
	return s.body.Close()
}
