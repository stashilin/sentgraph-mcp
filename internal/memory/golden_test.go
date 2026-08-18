package memory

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Golden vectors freeze how text is measured and cut. Zep counts characters
// (code points), not bytes -- and not UTF-16 units either, which is what a
// JavaScript port gets from .length and .slice(). These vectors are the only
// artefact that outlives this package, so they are generated from the live
// implementation and committed.
//
// Regenerate with: go test ./internal/memory -update-golden
var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/limits-golden.json from the current implementation")

const limitsGoldenPath = "testdata/limits-golden.json"

type truncateCase struct {
	Name  string `json:"name"`
	Why   string `json:"why"`
	In    string `json:"in"`
	Limit int    `json:"limit"`
	Out   string `json:"out"`
	// Lengths that a port must reproduce. UTF16Units is recorded so a
	// JavaScript implementation can see where naive .length would disagree.
	InRunes  int `json:"in_runes"`
	InUTF16  int `json:"in_utf16_units"`
	InBytes  int `json:"in_bytes"`
	OutRunes int `json:"out_runes"`
	OutUTF16 int `json:"out_utf16_units"`
}

type chunkCase struct {
	Name     string   `json:"name"`
	Why      string   `json:"why"`
	In       string   `json:"in"`
	Size     int      `json:"size"`
	Out      []string `json:"out"`
	InRunes  int      `json:"in_runes"`
	OutRunes []int    `json:"out_runes"`
}

type limitsGolden struct {
	Constants map[string]int `json:"constants"`
	Truncate  []truncateCase `json:"truncate"`
	Chunks    []chunkCase    `json:"chunks"`
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2 // surrogate pair
		} else {
			n++
		}
	}
	return n
}

// family is a ZWJ emoji sequence: 7 code points, 11 UTF-16 units, 1 grapheme.
// It is the case where code points, UTF-16 units and "what a human calls one
// character" all disagree -- exactly where a port silently drifts.
const family = "\U0001F468‍\U0001F469‍\U0001F467‍\U0001F466"

func truncateInputs() []struct {
	name, why, in string
	limit         int
} {
	return []struct {
		name, why, in string
		limit         int
	}{
		{"under_limit", "shorter than the limit: returned unchanged", "hello", 10},
		{"exactly_limit", "length equals the limit: no cut", "hello", 5},
		{"over_limit_ascii", "plain ASCII cut", "hello world", 5},
		{"cyrillic_cut", "Cyrillic: 2 bytes per code point, so a byte-based cut would land mid-character", "привет мир", 6},
		{"cyrillic_exact", "cut exactly at the code-point boundary", "привет", 6},
		{"emoji_cut", "cutting inside a ZWJ sequence: code-point cut, not grapheme", family + "tail", 3},
		{"emoji_whole", "sequence fits whole", family, 7},
		{"mixed_script", "Latin + Cyrillic + emoji together", "ab" + "вг" + "\U0001F600" + "de", 5},
		{"empty", "empty input", "", 10},
		{"zero_limit", "limit 0 yields empty output", "abc", 0},
		{"real_message_limit", "the production limit applied to a long Cyrillic text", strings.Repeat("я", 5000), MaxMessageChars},
	}
}

func chunkInputs() []struct {
	name, why, in string
	size          int
} {
	return []struct {
		name, why, in string
		size          int
	}{
		{"single_under", "fits in one chunk", "short text", 100},
		{"single_exact", "length equals chunk size: still one chunk", "abcde", 5},
		{"two_chunks", "split into two", "abcdefghij", 5},
		{"uneven_tail", "last chunk shorter", "abcdefg", 3},
		{"cyrillic_chunks", "Cyrillic split by code points", strings.Repeat("аб", 6), 4},
		{"emoji_boundary", "ZWJ sequence split across chunks by code point", family + family, 5},
		{"empty", "empty input yields one empty chunk", "", 10},
		{"real_graph_limit", "the production limit on a long Cyrillic text", strings.Repeat("э", 25000), MaxGraphDataChars},
	}
}

func TestLimitsGoldenVectors(t *testing.T) {
	got := limitsGolden{
		Constants: map[string]int{
			"MaxMessagesPerCall": MaxMessagesPerCall,
			"MaxMessageChars":    MaxMessageChars,
			"MaxGraphDataChars":  MaxGraphDataChars,
		},
	}
	for _, in := range truncateInputs() {
		out := truncate(in.in, in.limit)
		got.Truncate = append(got.Truncate, truncateCase{
			Name: in.name, Why: in.why, In: in.in, Limit: in.limit, Out: out,
			InRunes: utf8.RuneCountInString(in.in), InUTF16: utf16Len(in.in), InBytes: len(in.in),
			OutRunes: utf8.RuneCountInString(out), OutUTF16: utf16Len(out),
		})
	}
	for _, in := range chunkInputs() {
		out := chunks(in.in, in.size)
		runes := make([]int, len(out))
		for i, c := range out {
			runes[i] = utf8.RuneCountInString(c)
		}
		got.Chunks = append(got.Chunks, chunkCase{
			Name: in.name, Why: in.why, In: in.in, Size: in.size, Out: out,
			InRunes: utf8.RuneCountInString(in.in), OutRunes: runes,
		})
	}

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(limitsGoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		blob, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(limitsGoldenPath, append(blob, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d truncate and %d chunk vectors", len(got.Truncate), len(got.Chunks))
		return
	}

	blob, err := os.ReadFile(limitsGoldenPath)
	if err != nil {
		t.Fatalf("golden file missing: %v (regenerate with -update-golden)", err)
	}
	var want limitsGolden
	if err := json.Unmarshal(blob, &want); err != nil {
		t.Fatal(err)
	}
	for k, v := range got.Constants {
		if want.Constants[k] != v {
			t.Fatalf("constant %s: golden %d, code %d", k, want.Constants[k], v)
		}
	}
	if len(want.Truncate) != len(got.Truncate) || len(want.Chunks) != len(got.Chunks) {
		t.Fatalf("golden has %d/%d cases, code produced %d/%d -- regenerate", len(want.Truncate), len(want.Chunks), len(got.Truncate), len(got.Chunks))
	}
	for i := range want.Truncate {
		w, g := want.Truncate[i], got.Truncate[i]
		if w.Name != g.Name || w.Out != g.Out || w.OutRunes != g.OutRunes {
			t.Fatalf("truncate case %q drifted: want %q (%d runes), got %q (%d runes)", w.Name, w.Out, w.OutRunes, g.Out, g.OutRunes)
		}
	}
	for i := range want.Chunks {
		w, g := want.Chunks[i], got.Chunks[i]
		if w.Name != g.Name || len(w.Out) != len(g.Out) {
			t.Fatalf("chunk case %q: want %d chunks, got %d", w.Name, len(w.Out), len(g.Out))
		}
		for j := range w.Out {
			if w.Out[j] != g.Out[j] {
				t.Fatalf("chunk case %q part %d drifted: want %q, got %q", w.Name, j, w.Out[j], g.Out[j])
			}
		}
	}
}

// The whole point of the vectors: cutting never produces invalid text and never
// exceeds the limit in code points.
func TestTruncateNeverExceedsLimitInRunes(t *testing.T) {
	for _, in := range truncateInputs() {
		out := truncate(in.in, in.limit)
		if n := utf8.RuneCountInString(out); n > in.limit {
			t.Errorf("%s: output has %d runes, limit %d", in.name, n, in.limit)
		}
		if !utf8.ValidString(out) {
			t.Errorf("%s: output is not valid UTF-8", in.name)
		}
	}
}
