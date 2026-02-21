package extract

import "testing"

func TestParseEmbedURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantDomain  string
		wantPrefix  string
		wantID      string
		wantErr     bool
	}{
		{
			name:       "standard embed-1 URL",
			url:        "https://streameeeeee.site/embed-1/v3/e-1/AbCdEf123?z=",
			wantDomain: "streameeeeee.site",
			wantPrefix: "embed-1",
			wantID:     "AbCdEf123",
		},
		{
			name:       "embed-2 URL",
			url:        "https://megacloud.blog/embed-2/v3/e-1/XyZ789?k=1",
			wantDomain: "megacloud.blog",
			wantPrefix: "embed-2",
			wantID:     "XyZ789",
		},
		{
			name:       "embed-4 URL with query",
			url:        "https://example.com/embed-4/v3/e-1/testId?z=",
			wantDomain: "example.com",
			wantPrefix: "embed-4",
			wantID:     "testId",
		},
		{
			name:    "empty URL",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			domain, prefix, id, err := parseEmbedURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseEmbedURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if domain != tt.wantDomain {
				t.Errorf("domain = %q, want %q", domain, tt.wantDomain)
			}
			if prefix != tt.wantPrefix {
				t.Errorf("prefix = %q, want %q", prefix, tt.wantPrefix)
			}
			if id != tt.wantID {
				t.Errorf("id = %q, want %q", id, tt.wantID)
			}
		})
	}
}

func TestNewReturnsExtractor(t *testing.T) {
	ext := New()
	if ext == nil {
		t.Fatal("New() returned nil")
	}
	// Verify it implements the Extractor interface
	var _ Extractor = ext
}
