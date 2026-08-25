package provider

import (
	"encoding/json"
	"testing"
)

func TestVaplayerResponseStatusCode(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "success returns status_code as string",
			body: `{"status_code":"200","data":{"stream_urls":["https://example.com/a.m3u8"]}}`,
			want: "200",
		},
		{
			name: "not found returns status_code as bare number",
			body: `{"status_code":404}`,
			want: "404",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp vaplayerResponse
			if err := json.Unmarshal([]byte(tt.body), &resp); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if got := resp.StatusCode.String(); got != tt.want {
				t.Errorf("StatusCode = %q, want %q", got, tt.want)
			}
		})
	}
}
