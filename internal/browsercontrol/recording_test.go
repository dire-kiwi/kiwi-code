package browsercontrol

import (
	"errors"
	"testing"
)

func TestResolveRecordingRange(t *testing.T) {
	tests := []struct {
		header string
		want   RecordingRange
	}{
		{"", RecordingRange{Start: 0, End: 9, Size: 10, Total: 10}},
		{"bytes=0-3", RecordingRange{Start: 0, End: 3, Size: 4, Total: 10}},
		{"Bytes=4-", RecordingRange{Start: 4, End: 9, Size: 6, Total: 10}},
		{"bytes=-3", RecordingRange{Start: 7, End: 9, Size: 3, Total: 10}},
		{"bytes=8-99", RecordingRange{Start: 8, End: 9, Size: 2, Total: 10}},
	}
	for _, test := range tests {
		got, err := ResolveRecordingRange(test.header, 10)
		if err != nil || got != test.want {
			t.Fatalf("ResolveRecordingRange(%q) = %#v, %v; want %#v", test.header, got, err, test.want)
		}
	}
	for _, header := range []string{"bytes=", "bytes=-0", "bytes=10-", "bytes=4-3", "bytes=0-1,3-4", "items=0-1", "bytes=a-b"} {
		if _, err := ResolveRecordingRange(header, 10); !errors.Is(err, ErrRecordingRangeNotSatisfiable) {
			t.Fatalf("ResolveRecordingRange(%q) error = %v", header, err)
		}
	}
}
