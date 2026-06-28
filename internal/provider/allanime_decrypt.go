package provider

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// decryptTobeparsed AES-256-CTR-decrypts the AllAnime episode-sources blob.
// Layout: [0]=version, [1:13]=nonce, [13:N-16]=ciphertext, [N-16:N]=discarded.
func decryptTobeparsed(blob, passphrase string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return nil, fmt.Errorf("base64: %w", err)
	}
	if len(data) < 1+12+16 {
		return nil, fmt.Errorf("blob too short: %d bytes", len(data))
	}
	nonce := data[1:13]
	ct := data[13 : len(data)-16]
	key := sha256.Sum256([]byte(passphrase))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	iv := make([]byte, 16)
	copy(iv, nonce)
	binary.BigEndian.PutUint32(iv[12:], 2)
	pt := make([]byte, len(ct))
	cipher.NewCTR(block, iv).XORKeyStream(pt, ct)
	return pt, nil
}

// decodeSourceURL decodes a "--"-prefixed internal-CDN sourceUrl (hex bytes XOR
// xor), rewrites /clock -> /clock.json, and absolutizes against clockBase.
// Returns "" for any non-"--" URL (those go through the embed extractors).
func decodeSourceURL(sourceURL string, xor byte, clockBase string) string {
	if !strings.HasPrefix(sourceURL, "--") {
		return ""
	}
	h := sourceURL[2:]
	var sb strings.Builder
	for i := 0; i+1 < len(h); i += 2 {
		b, err := strconv.ParseUint(h[i:i+2], 16, 8)
		if err != nil {
			return ""
		}
		sb.WriteByte(byte(b) ^ xor)
	}
	path := strings.Replace(sb.String(), "/clock", "/clock.json", 1)
	if strings.HasPrefix(path, "/") {
		return clockBase + path
	}
	return path
}

// encodeEpisodeID packs the AllAnime episode identity into a single string so
// Watch is self-contained (the sub/dub toggle can't race it).
func encodeEpisodeID(showID, episodeString, trans string) string {
	return showID + "|" + episodeString + "|" + trans
}

func parseEpisodeID(id string) (showID, episodeString, trans string, ok bool) {
	parts := strings.SplitN(id, "|", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}
