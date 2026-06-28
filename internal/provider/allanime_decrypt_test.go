package provider

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"testing"
)

// helper: build a tobeparsed blob the same way the server does, to test the decrypt round-trip.
func makeBlob(plain, passphrase string) string {
	key := sha256.Sum256([]byte(passphrase))
	block, _ := aes.NewCipher(key[:])
	nonce := []byte("0123456789ab") // 12 fixed bytes
	iv := make([]byte, 16)
	copy(iv, nonce)
	binary.BigEndian.PutUint32(iv[12:], 2)
	ct := make([]byte, len(plain))
	cipher.NewCTR(block, iv).XORKeyStream(ct, []byte(plain))
	blob := []byte{0x01}
	blob = append(blob, nonce...)
	blob = append(blob, ct...)
	blob = append(blob, make([]byte, 16)...) // discarded tail
	return base64.StdEncoding.EncodeToString(blob)
}

func TestDecryptTobeparsed(t *testing.T) {
	want := `{"episode":{"sourceUrls":[]}}`
	got, err := decryptTobeparsed(makeBlob(want, "Xot36i3lK3:v1"), "Xot36i3lK3:v1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("decrypt = %q, want %q", got, want)
	}
	if _, err := decryptTobeparsed("!!notbase64!!", "k"); err == nil {
		t.Fatal("expected error on bad base64")
	}
}

func TestDecodeSourceURL(t *testing.T) {
	// 0x17^0x38='/', 0x08^0x38='0', 0x59^0x38='a' -> "/0a", starts with "/" so clockBase is prepended.
	got := decodeSourceURL("--170859", 0x38, "https://allanime.day")
	if got != "https://allanime.day/0a" {
		t.Fatalf("decode = %q, want https://allanime.day/0a", got)
	}
	// /clock is rewritten to /clock.json. Encode "/clock" with XOR 0x38:
	// '/'=0x2f^0x38=0x17,'c'=0x63^0x38=0x5b,'l'=0x6c^0x38=0x54,'o'=0x6f^0x38=0x57,'c'=0x5b,'k'=0x6b^0x38=0x53
	got2 := decodeSourceURL("--175b54575b53", 0x38, "https://allanime.day")
	if got2 != "https://allanime.day/clock.json" {
		t.Fatalf("decode = %q, want .../clock.json", got2)
	}
	if decodeSourceURL("https://streamwish.to/e/x", 0x38, "https://allanime.day") != "" {
		t.Fatal("non-'--' URL should return \"\" (handled by the extractor path, not here)")
	}
}

func TestEpisodeIDRoundTrip(t *testing.T) {
	id := encodeEpisodeID("ReHMC7TQnch3C6z8j", "1.5", "dub")
	showID, ep, trans, ok := parseEpisodeID(id)
	if !ok || showID != "ReHMC7TQnch3C6z8j" || ep != "1.5" || trans != "dub" {
		t.Fatalf("round-trip failed: %q -> (%q,%q,%q,%v)", id, showID, ep, trans, ok)
	}
	if _, _, _, ok := parseEpisodeID("garbage"); ok {
		t.Fatal("malformed id should not parse")
	}
}
