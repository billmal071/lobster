package httputil

import (
	"testing"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid HTTPS", "https://example.com/path", false},
		{"HTTP rejected", "http://example.com/path", true},
		{"javascript scheme rejected", "javascript:alert(1)", true},
		{"data scheme rejected", "data:text/html,<h1>Hi</h1>", true},
		{"FTP rejected", "ftp://example.com/file", true},
		{"empty string", "", true},
		{"no host", "https://", true},
		{"valid with port", "https://example.com:8080/path", false},
		{"valid with query", "https://example.com/path?q=test&a=b", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"valid movie ID", "movie/free-the-exorcist-hd-75043", false},
		{"valid tv ID", "tv/watch-breaking-bad-39516", false},
		{"valid numeric", "12345", false},
		{"empty", "", true},
		{"path traversal dots", "../../etc/passwd", true},
		{"shell injection semicolon", "123; rm -rf /", true},
		{"shell injection backtick", "123`whoami`", true},
		{"shell injection dollar", "$(cat /etc/passwd)", true},
		{"newline injection", "123\n456", true},
		{"pipe injection", "123|ls", true},
		{"ampersand injection", "123&whoami", true},
		{"too long", string(make([]byte, 300)), true},
		{"spaces", "movie id with spaces", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestValidateNumericID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"valid", "12345", false},
		{"zero", "0", false},
		{"empty", "", true},
		{"letters", "abc", true},
		{"mixed", "123abc", true},
		{"negative", "-1", true},
		{"decimal", "1.5", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNumericID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNumericID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"normal filename", "movie.mkv", "movie.mkv"},
		{"path traversal", "../../etc/passwd", "passwd"},
		{"directory components", "/home/user/secret.txt", "secret.txt"},
		{"shell metacharacters", "movie; rm -rf /.mkv", ".mkv"},         // filepath.Base strips to ".mkv"
		{"null bytes", "movie\x00.mkv", "movie.mkv"},
		{"Windows special chars", "movie<>:\"|?*.mkv", "movie_______.mkv"},
		{"double dots", "movie..mkv", "movie_mkv"},
		{"empty string", "", "untitled"},
		{"just dots", "..", "_"},                                                      // filepath.Base("..") = "..", replacer makes "_"
		{"just dot", ".", "untitled"},
		{"backslash traversal", "..\\..\\windows\\system32", "____windows_system32"}, // on linux, backslash isn't path sep
		{"XSS payload", "<script>alert(1)</script>.mkv", "script_.mkv"},              // filepath.Base handles angle brackets
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeFilename(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSafeDownloadPath(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		filename string
		wantErr  bool
	}{
		{"normal", "/tmp/downloads", "movie.mkv", false},
		{"path traversal attempt", "/tmp/downloads", "../../etc/passwd", false}, // sanitized to "passwd"
		{"shell injection", "/tmp/downloads", "$(whoami).mkv", false},          // sanitized
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := SafeDownloadPath(tt.dir, tt.filename)
			if (err != nil) != tt.wantErr {
				t.Errorf("SafeDownloadPath(%q, %q) error = %v, wantErr %v", tt.dir, tt.filename, err, tt.wantErr)
			}
			if err == nil && path == "" {
				t.Error("SafeDownloadPath returned empty path without error")
			}
		})
	}
}

func TestEncodeQuery(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"breaking bad", "breaking-bad"},
		{"the office", "the-office"},
		{"star wars", "star-wars"},
		{"  extra   spaces  ", "extra-spaces"},
		{"special&chars=here", "special&chars=here"},
		{"singleword", "singleword"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := EncodeQuery(tt.input)
			if got != tt.expected {
				t.Errorf("EncodeQuery(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
