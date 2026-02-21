package extract

import "testing"

func TestExtractClientKey(t *testing.T) {
	tests := []struct {
		name    string
		html    string
		want    string
		wantErr bool
	}{
		{
			name: "meta tag pattern",
			html: `<html><head><meta name="_gg_fb" content="abc123XYZ"></head><body></body></html>`,
			want: "abc123XYZ",
		},
		{
			name: "comment pattern",
			html: `<html><!-- _is_th:secretKey42 --><body></body></html>`,
			want: "secretKey42",
		},
		{
			name: "lk_db 3-part key pattern",
			html: `<html><script>window._lk_db = {x: "partA", y: "partB", z: "partC"};</script></html>`,
			want: "partApartBpartC",
		},
		{
			name: "div data-dpi pattern",
			html: `<html><div data-dpi="myKey99" class="test"></div></html>`,
			want: "myKey99",
		},
		{
			name: "script nonce pattern",
			html: `<html><script nonce="nonceKey123">console.log('hi');</script></html>`,
			want: "nonceKey123",
		},
		{
			name: "window._xy_ws pattern",
			html: `<html><script>window._xy_ws = "wsKey456";</script></html>`,
			want: "wsKey456",
		},
		{
			name: "no match",
			html: `<html><body>nothing here</body></html>`,
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractClientKey(tt.html)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractClientKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("extractClientKey() = %q, want %q", got, tt.want)
			}
		})
	}
}
