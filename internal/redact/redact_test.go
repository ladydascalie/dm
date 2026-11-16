package redact

import (
	"bytes"
	"strings"
	"testing"
)

// chunkFeed feeds src to the scrubber in chunks of the given size, then
// flushes, and returns the whole output.
func chunkFeed(t *testing.T, s *Scrubber, src []byte, size int) string {
	t.Helper()
	var out bytes.Buffer
	for i := 0; i < len(src); i += size {
		end := i + size
		if end > len(src) {
			end = len(src)
		}
		s.Feed(src[i:end], &out)
	}
	s.Flush(&out)
	return out.String()
}

func TestScrub(t *testing.T) {
	tests := []struct {
		name    string
		secrets [][2]string // name, value pairs
		input   string
		chunk   int
		want    string
	}{
		{
			name:    "value split across two chunks",
			secrets: [][2]string{{"api-key", "sk-live-abc123"}},
			input:   "token=sk-live-abc123;end",
			chunk:   4,
			want:    "token=[api-key redacted];end",
		},
		{
			name:    "value ends exactly at a chunk boundary",
			secrets: [][2]string{{"api-key", "sk-live-abc123"}},
			input:   "xysk-live-abc123",
			chunk:   5,
			want:    "xy[api-key redacted]",
		},
		{
			name:    "value starts exactly at a chunk boundary",
			secrets: [][2]string{{"api-key", "sk-live-abc123"}},
			input:   "xxysk-live-abc123",
			chunk:   2,
			want:    "xxy[api-key redacted]",
		},
		{
			name: "overlapping matches pick the leftmost, longest",
			secrets: [][2]string{
				{"two", "abab"},
				{"three", "ba"},
			},
			input: "ababaxbay",
			chunk: 3,
			want:  "[two redacted]ax[three redacted]y",
		},
		{
			name: "adjacent matches both fire",
			secrets: [][2]string{
				{"a", "aa"},
				{"b", "bb"},
			},
			input: "aabb",
			chunk: 1,
			want:  "[a redacted][b redacted]",
		},
		{
			name: "longer value wins over its prefix at the same position",
			secrets: [][2]string{
				{"short", "sk-"},
				{"long", "sk-live-abc123"},
			},
			input: "xsk-live-abc123ysk-z",
			chunk: 7,
			want:  "x[long redacted]y[short redacted]z",
		},
		{
			name:    "prefix stays literal when the longer value never completes",
			secrets: [][2]string{{"long", "sk-live-abc123"}},
			input:   "sk-live-abc",
			chunk:   6,
			want:    "sk-live-abc",
		},
		{
			name: "several distinct values in one stream",
			secrets: [][2]string{
				{"api-key", "AKIA123456"},
				{"db-pass", "hunter2!!"},
			},
			input: "AKIA123456 then hunter2!! then AKIA123456",
			chunk: 9,
			want:  "[api-key redacted] then [db-pass redacted] then [api-key redacted]",
		},
		{
			name:    "empty registry passes through",
			secrets: nil,
			input:   "sk-live-abc123\x00\xff raw bytes stay",
			chunk:   3,
			want:    "sk-live-abc123\x00\xff raw bytes stay",
		},
		{
			name:    "binary safe: secret inside NUL and 0xff bytes",
			secrets: [][2]string{{"tok", "\x00secret\xff"}},
			input:   "a\x00secret\xffb\x00\x00secret\xff\xff",
			chunk:   4,
			want:    "a[tok redacted]b\x00[tok redacted]\xff",
		},
		{
			name:    "binary safe: newlines and high bytes around the secret",
			secrets: [][2]string{{"pw", "s3cr3t"}},
			input:   "\n\r\x80s3cr3t\x7f\xfe\n",
			chunk:   2,
			want:    "\n\r\x80[pw redacted]\x7f\xfe\n",
		},
		{
			name:    "repeated value matches every occurrence",
			secrets: [][2]string{{"pw", "zz"}},
			input:   "zzzzzz",
			chunk:   5,
			want:    "[pw redacted][pw redacted][pw redacted]",
		},
		{
			name:    "empty value is ignored",
			secrets: [][2]string{{"none", ""}},
			input:   "unchanged",
			chunk:   4,
			want:    "unchanged",
		},
		{
			name:    "re-register renames the marker",
			secrets: [][2]string{{"first", "v"}, {"second", "v"}},
			input:   "vv",
			chunk:   1,
			want:    "[second redacted][second redacted]",
		},
		{
			name:    "one byte per chunk still matches",
			secrets: [][2]string{{"pw", "abc"}},
			input:   "zabcz",
			chunk:   1,
			want:    "z[pw redacted]z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			for _, pair := range tt.secrets {
				s.Register(pair[0], pair[1])
			}
			if got := chunkFeed(t, s, []byte(tt.input), tt.chunk); got != tt.want {
				t.Errorf("chunked feed = %q, want %q", got, tt.want)
			}
			// The same input in one chunk must agree.
			s2 := New()
			for _, pair := range tt.secrets {
				s2.Register(pair[0], pair[1])
			}
			var out bytes.Buffer
			s2.Feed([]byte(tt.input), &out)
			s2.Flush(&out)
			if got := out.String(); got != tt.want {
				t.Errorf("single feed = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLargeChunksThroughput guards the held-suffix logic on chunks far larger
// than any secret.
func TestLargeChunksThroughput(t *testing.T) {
	s := New()
	s.Register("pw", "needle")
	src := strings.Repeat("haystack", 4096) + "needle" + strings.Repeat("haystack", 4096)
	var out bytes.Buffer
	s.Feed([]byte(src), &out)
	s.Flush(&out)
	got := out.String()
	want := strings.Repeat("haystack", 4096) + Marker("pw") + strings.Repeat("haystack", 4096)
	if got != want {
		t.Errorf("large chunk scrub mismatch: got %d bytes, want %d", len(got), len(want))
	}
}
