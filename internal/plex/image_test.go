package plex

import "testing"

func TestItemImagePath(t *testing.T) {
	tests := []struct {
		name, key, path, want string
	}{
		{"thumb", "1001", "/library/metadata/1001/thumb/1700000050", "/library/metadata/1001/thumb/1700000050"},
		{"art", "1001", "/library/metadata/1001/art/1700000050", "/library/metadata/1001/art/1700000050"},
		{"bad key", "10a1", "/library/metadata/10a1/thumb/1", ""},
		{"other item", "1001", "/library/metadata/1002/thumb/1", ""},
		{"absolute URL", "1001", "https://x/library/metadata/1001/thumb/1", ""},
		{"scheme-relative", "1001", "//x/library/metadata/1001/thumb/1", ""},
		{"query", "1001", "/library/metadata/1001/thumb/1?x=1", ""},
		{"fragment", "1001", "/library/metadata/1001/thumb/1#f", ""},
		{"not digits", "1001", "/library/metadata/1001/thumb/abc", ""},
		{"escaped slash", "1001", "/library/metadata/1001%2Fthumb/1", ""},
		{"empty", "1001", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ItemImagePath(tt.key, tt.path); got != tt.want {
				t.Errorf("ItemImagePath(%q, %q) = %q, want %q", tt.key, tt.path, got, tt.want)
			}
		})
	}
}

func TestSafeRemoteImage(t *testing.T) {
	const ok = "https://metadata.example.invalid/thumb.jpg"
	tests := []struct {
		name, raw, want string
	}{
		{"https", ok, ok},
		{"http", "http://h/x.jpg", ""},
		{"user info", "https://user:pw@h/x.jpg", ""},
		{"no host", "https://:443/x.jpg", ""},
		{"plex token", "https://h/x.jpg?X-Plex-Token=abc", ""},
		{"token suffix", "https://h/x.jpg?apitoken=abc", ""},
		{"bad query", "https://h/x.jpg?a=%zz", ""},
		{"fragment", "https://h/x.jpg#f", ""},
		{"bare fragment", "https://h/x.jpg#", ""},
		{"relative", "/relative.jpg", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SafeRemoteImage(tt.raw); got != tt.want {
				t.Errorf("SafeRemoteImage(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
