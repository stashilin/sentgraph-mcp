package redact

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Golden vectors freeze the exact input -> output behaviour of Secrets so a
// reimplementation in another language can be checked against it. They are the
// only artefact that survives this package being deleted, so they are generated
// from the live implementation and committed.
//
// Regenerate with: go test ./internal/redact -update-golden
var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/redact-golden.json from the current implementation")

const goldenPath = "testdata/redact-golden.json"

type goldenCase struct {
	Name string `json:"name"`
	Why  string `json:"why"`
	In   string `json:"in"`
	Out  string `json:"out"`
	// InRunes is the input length in code points, not bytes. A port that uses
	// UTF-16 units (JavaScript .length) will disagree here on non-BMP text.
	InRunes int `json:"in_runes"`
}

// goldenInputs are assembled from fragments so this file holds no contiguous
// secret-shaped literal.
func goldenInputs() []struct{ name, why, in string } {
	sk1 := "sk" + "-" + strings.Repeat("aA1bB2cC", 3)
	sk2 := "sk" + "-" + strings.Repeat("zZ9yY8xX", 3)
	ghp := "gh" + "p_" + strings.Repeat("a", 36)
	pat := "github" + "_pat_" + strings.Repeat("b", 30)
	akia := "AK" + "IA" + strings.Repeat("Q", 16)
	aiza := "AI" + "za" + strings.Repeat("c", 35)
	xoxb := "xo" + "xb-" + strings.Repeat("9", 12) + "-abcdefghij"
	jwt := "ey" + "J0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9" + "." + strings.Repeat("a", 20) + "." + strings.Repeat("b", 20)
	bearer := strings.Repeat("xy7Z", 5)

	return []struct{ name, why, in string }{
		{"openai_key", "single sk- key mid-sentence", "key is " + sk1 + " keep going"},
		{"github_token", "classic gh token", "token " + ghp + " end"},
		{"github_pat", "fine-grained PAT", "pat " + pat},
		{"aws_key_id", "AWS access key id", "aws " + akia + " region us-east-1"},
		{"google_key", "Google API key", "google " + aiza},
		{"slack_token", "Slack bot token", "slack " + xoxb},
		{"jwt", "three-segment JWT", "auth " + jwt + " done"},
		{"bearer_header", "Authorization header", "Authorization: Bearer " + bearer},
		{"bearer_lowercase", "case-insensitive match: Go uses (?i), JS needs the i flag", "authorization: bearer " + bearer},
		{"bearer_uppercase", "same, upper case", "AUTHORIZATION: BEARER " + bearer},

		// The cases a naive port fails on.
		{"two_same_shape", "two sk- keys: JS String.replace without /g redacts only the first", sk1 + " and " + sk2},
		{"three_same_shape", "three of a kind in one line", sk1 + " " + sk2 + " " + sk1},
		{"same_shape_multiline", "repeats across lines", "line1 " + ghp + "\nline2 " + ghp + "\nline3 clean"},
		{"mixed_shapes_repeated", "several shapes, each twice", sk1 + " " + akia + " " + sk2 + " " + akia},

		// Text handling.
		{"clean_text", "no secrets: output must equal input byte for byte", "Пользователь предпочитает табы и Go 1.25."},
		{"cyrillic_around_secret", "Cyrillic neighbours must survive intact", "ключ " + sk1 + " конец"},
		{"emoji_around_secret", "non-BMP characters adjacent to a match", "🔑👨‍👩‍👧‍👦 " + sk1 + " 🚀"},
		{"secret_at_start", "match at offset 0", sk1 + " trailing"},
		{"secret_at_end", "match touching end of string", "leading " + sk1},
		{"only_secret", "the whole string is one secret", sk1},
		{"adjacent_secrets", "two matches with no separator", ghp + sk1},
		{"empty", "empty input", ""},
		{"near_miss_short_sk", "too short to match: must NOT be redacted", "sk" + "-short"},
		{"near_miss_word_bearer", "the word bearer without a token stays", "the bearer of bad news"},
	}
}

func TestGoldenVectors(t *testing.T) {
	inputs := goldenInputs()
	got := make([]goldenCase, 0, len(inputs))
	for _, in := range inputs {
		got = append(got, goldenCase{
			Name:    in.name,
			Why:     in.why,
			In:      in.in,
			Out:     Secrets(in.in),
			InRunes: utf8.RuneCountInString(in.in),
		})
	}

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		blob, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, append(blob, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d vectors to %s", len(got), goldenPath)
		return
	}

	blob, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden file missing: %v (regenerate with -update-golden)", err)
	}
	var want []goldenCase
	if err := json.Unmarshal(blob, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(got) {
		t.Fatalf("golden has %d cases, implementation produced %d -- regenerate", len(want), len(got))
	}
	for i := range want {
		if want[i].Name != got[i].Name {
			t.Fatalf("case %d: golden %q, got %q", i, want[i].Name, got[i].Name)
		}
		if want[i].In != got[i].In || want[i].Out != got[i].Out {
			t.Fatalf("case %q drifted:\n  in   %q\n  want %q\n  got  %q", want[i].Name, want[i].In, want[i].Out, got[i].Out)
		}
	}
}

// Every secret in the golden set must actually disappear. Guards against a
// vector being regenerated from a broken implementation and freezing the bug.
func TestGoldenVectorsLeakNothing(t *testing.T) {
	blob, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Skipf("golden file not generated yet: %v", err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(blob, &cases); err != nil {
		t.Fatal(err)
	}
	markers := []string{"sk" + "-a", "sk" + "-z", "gh" + "p_a", "github" + "_pat_", "AK" + "IA", "AI" + "za", "xo" + "xb-", "ey" + "J0"}
	for _, c := range cases {
		if strings.HasPrefix(c.Name, "near_miss") || c.Name == "clean_text" {
			continue
		}
		for _, m := range markers {
			if strings.Contains(c.In, m) && strings.Contains(c.Out, m) {
				t.Errorf("case %q: secret starting %q survives in output %q", c.Name, m, c.Out)
			}
		}
	}
}
