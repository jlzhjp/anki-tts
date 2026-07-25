package ffmpeg

import (
	"fmt"
	"strings"
)

// Format identifies a supported audio output format.
type Format string

const (
	// FormatAAC identifies raw AAC output.
	FormatAAC Format = "aac"
	// FormatFLAC identifies FLAC output.
	FormatFLAC Format = "flac"
	// FormatM4A identifies MPEG-4 audio output.
	FormatM4A Format = "m4a"
	// FormatMP3 identifies MP3 output.
	FormatMP3 Format = "mp3"
	// FormatOgg identifies Ogg output.
	FormatOgg Format = "ogg"
	// FormatOpus identifies Opus output.
	FormatOpus Format = "opus"
	// FormatWAV identifies WAV output.
	FormatWAV Format = "wav"
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
	case FormatFLAC, FormatMP3, FormatOgg, FormatOpus, FormatWAV:
		return string(f)
	default:
		return ""
	}
}

// Extension returns the filename extension for encoded audio.
func (f Format) Extension() string { return string(f) }
