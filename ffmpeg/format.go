package ffmpeg

import (
	"fmt"
	"strings"
)

// Format identifies a supported audio output format.
type Format string

const (
	FormatAAC  Format = "aac"
	FormatFLAC Format = "flac"
	FormatM4A  Format = "m4a"
	FormatMP3  Format = "mp3"
	FormatOgg  Format = "ogg"
	FormatOpus Format = "opus"
	FormatWAV  Format = "wav"
)

// ParseFormat parses a supported audio output format.
func ParseFormat(value string) (Format, error) {
	format := Format(strings.ToLower(strings.TrimSpace(value)))
	switch format {
	case FormatAAC, FormatFLAC, FormatM4A, FormatMP3, FormatOgg, FormatOpus, FormatWAV:
		return format, nil
	default:
		return "", fmt.Errorf("format must be one of aac, flac, m4a, mp3, ogg, opus, or wav, got %q", value)
	}
}

// Muxer returns the name accepted by FFmpeg's -f option.
func (f Format) Muxer() string {
	switch f {
	case FormatAAC:
		return "adts"
	case FormatM4A:
		return "ipod"
	default:
		return string(f)
	}
}

// Extension returns the filename extension for encoded audio.
func (f Format) Extension() string { return string(f) }
