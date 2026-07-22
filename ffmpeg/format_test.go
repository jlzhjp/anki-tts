package ffmpeg

import "testing"

func TestFormat(t *testing.T) {
	tests := []struct {
		input     string
		want      Format
		muxer     string
		extension string
	}{
		{input: "MP3", want: FormatMP3, muxer: "mp3", extension: "mp3"},
		{input: " wav ", want: FormatWAV, muxer: "wav", extension: "wav"},
		{input: "opus", want: FormatOpus, muxer: "opus", extension: "opus"},
		{input: "aac", want: FormatAAC, muxer: "adts", extension: "aac"},
		{input: "m4a", want: FormatM4A, muxer: "ipod", extension: "m4a"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			format, err := ParseFormat(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if format != test.want || format.Muxer() != test.muxer || format.Extension() != test.extension {
				t.Fatalf("format = %q, muxer = %q, extension = %q", format, format.Muxer(), format.Extension())
			}
		})
	}
}

func TestParseFormatRejectsUnsupportedFormat(t *testing.T) {
	if _, err := ParseFormat("matroska"); err == nil {
		t.Fatal("expected unsupported format error")
	}
}
