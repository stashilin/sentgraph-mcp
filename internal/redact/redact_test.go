package redact

import (
	"strings"
	"testing"
)

func TestSecretsRedactsKnownTokens(t *testing.T) {
	// Secrets are assembled from fragments so this source file contains no
	// contiguous secret-shaped literal (keeps secret scanners meaningful).
	cases := []struct {
		name   string
		secret string
	}{
		{"openai", "sk" + "-" + strings.Repeat("aA1bB2cC", 3)},
		{"github", "gh" + "p_" + strings.Repeat("a", 36)},
		{"github_pat", "github" + "_pat_" + strings.Repeat("b", 30)},
		{"aws", "AK" + "IA" + strings.Repeat("Q", 16)},
		{"google", "AI" + "za" + strings.Repeat("c", 35)},
		{"slack", "xo" + "xb-" + strings.Repeat("9", 12) + "-abcdefghij"},
		{"jwt", "ey" + "J0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9" + "." + strings.Repeat("a", 20) + "." + strings.Repeat("b", 20)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := "before " + c.secret + " after"
			got := Secrets(in)
			if strings.Contains(got, c.secret) {
				t.Fatalf("secret %q not redacted: %q", c.secret, got)
			}
			if !strings.HasPrefix(got, "before ") || !strings.HasSuffix(got, " after") {
				t.Fatalf("surrounding text damaged: %q", got)
			}
		})
	}
}

func TestSecretsRedactsBearer(t *testing.T) {
	token := strings.Repeat("xy7Z", 5)
	in := "Authorization: Bearer " + token
	if got := Secrets(in); strings.Contains(got, token) {
		t.Fatalf("bearer token not redacted: %q", got)
	}
}

func TestSecretsRedactsMultiple(t *testing.T) {
	openAI := "sk" + "-" + strings.Repeat("aA1bB2cC", 3)
	aws := "AK" + "IA" + strings.Repeat("Q", 16)
	in := "first " + openAI + " second " + aws
	got := Secrets(in)
	for _, secret := range []string{openAI, aws} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q not redacted: %q", secret, got)
		}
	}
}

// Two secrets of the SAME shape must both go. TestSecretsRedactsMultiple uses
// two different patterns, so it passes even when a port replaces only the first
// match per pattern -- exactly what JavaScript's String.replace does without
// the /g flag, where Go's ReplaceAllString replaces every occurrence. This test
// is the one that fails on such a port, so it exists before any port does.
func TestSecretsRedactsRepeatsOfSamePattern(t *testing.T) {
	repeats := map[string][]string{
		"openai":     {"sk" + "-" + strings.Repeat("aA1bB2cC", 3), "sk" + "-" + strings.Repeat("zZ9yY8xX", 3)},
		"github":     {"gh" + "p_" + strings.Repeat("a", 36), "gh" + "p_" + strings.Repeat("b", 36)},
		"aws":        {"AK" + "IA" + strings.Repeat("Q", 16), "AK" + "IA" + strings.Repeat("R", 16)},
		"bearer":     {"Bearer " + strings.Repeat("xy7Z", 5), "bearer " + strings.Repeat("qW3e", 5)},
		"three_keys": {"sk" + "-" + strings.Repeat("a1B2c3D4", 2), "sk" + "-" + strings.Repeat("e5F6g7H8", 2), "sk" + "-" + strings.Repeat("i9J0k1L2", 2)},
	}
	for name, secrets := range repeats {
		t.Run(name, func(t *testing.T) {
			in := "start " + strings.Join(secrets, " middle ") + " end"
			got := Secrets(in)
			for i, secret := range secrets {
				if strings.Contains(got, secret) {
					t.Fatalf("secret #%d of the same shape survived: %q", i+1, got)
				}
			}
		})
	}
}

func TestSecretsRedactsAtBoundaries(t *testing.T) {
	secret := "gh" + "p_" + strings.Repeat("a", 36)
	cases := []string{
		secret + " at start",
		"at end " + secret,
		secret,
		secret + "sk" + "-" + strings.Repeat("z9", 12),
	}
	for _, in := range cases {
		if got := Secrets(in); strings.Contains(got, secret) {
			t.Fatalf("boundary secret not redacted: %q", got)
		}
	}
}

func TestSecretsKeepsNormalText(t *testing.T) {
	in := "The user prefers tabs over spaces and targets Go 1.25."
	if got := Secrets(in); got != in {
		t.Fatalf("normal text altered: %q", got)
	}
}
