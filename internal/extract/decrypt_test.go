package extract

import "testing"

func TestAtob(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"SGVsbG8=", "Hello"},
		{"V29ybGQ=", "World"},
		{"", ""},
		{"YQ==", "a"},
		{"YWI=", "ab"},
		{"YWJj", "abc"},
	}

	for _, tt := range tests {
		got := atob(tt.input)
		if got != tt.want {
			t.Errorf("atob(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestReverseString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "olleh"},
		{"", ""},
		{"a", "a"},
		{"ab", "ba"},
	}

	for _, tt := range tests {
		got := reverseString(tt.input)
		if got != tt.want {
			t.Errorf("reverseString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSeedShuffle2Deterministic(t *testing.T) {
	charArray := make([]string, 95)
	for i := 0; i < 95; i++ {
		charArray[i] = string(rune(32 + i))
	}

	// Same key should produce same result
	result1 := seedShuffle2(charArray, "testkey123")
	result2 := seedShuffle2(charArray, "testkey123")

	for i := range result1 {
		if result1[i] != result2[i] {
			t.Errorf("seedShuffle2 not deterministic at index %d: %q vs %q", i, result1[i], result2[i])
		}
	}

	// Different key should produce different result
	result3 := seedShuffle2(charArray, "differentkey")
	same := true
	for i := range result1 {
		if result1[i] != result3[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("seedShuffle2 produced identical results for different keys")
	}
}

func TestColumnarCipher2Deterministic(t *testing.T) {
	input := "Hello, World! This is a test."
	key := "secret"

	result := columnarCipher2(input, key)
	if result == "" {
		t.Error("columnarCipher2 returned empty string")
	}
	if len(result) < len(input) {
		t.Errorf("columnarCipher2 result too short: got %d, want >= %d", len(result), len(input))
	}
}

func TestKeygen2Deterministic(t *testing.T) {
	result1 := keygen2("megakey123", "clientkey456")
	result2 := keygen2("megakey123", "clientkey456")

	if result1 != result2 {
		t.Error("keygen2 not deterministic")
	}

	if result1 == "" {
		t.Error("keygen2 returned empty string")
	}

	// All characters should be in printable ASCII range (32-126)
	for i, c := range result1 {
		if c < 32 || c > 126 {
			t.Errorf("keygen2 produced non-printable char at index %d: %d", i, c)
		}
	}
}

func TestDecryptSrc2EmptyInput(t *testing.T) {
	result := decryptSrc2("", "key", "megakey")
	if result != "" {
		t.Errorf("decryptSrc2 with empty input should return empty, got %q", result)
	}
}
