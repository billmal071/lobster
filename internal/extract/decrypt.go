package extract

import (
	"math/big"
	"strconv"
)

// decryptSrc2 performs a 3-layer decryption on the encrypted source string.
// Ported from aniwatch src/utils/methods.ts decryptSrc2.
func decryptSrc2(src, clientKey, megacloudKey string) string {
	const layers = 3

	genKey := keygen2(megacloudKey, clientKey)

	// Base64 decode (atob equivalent)
	decSrc := atob(src)

	// Printable ASCII characters 32-126
	charArray := make([]string, 95)
	for i := 0; i < 95; i++ {
		charArray[i] = string(rune(32 + i))
	}

	// Reverse each layer from layers down to 1
	for i := layers; i > 0; i-- {
		decSrc = reverseLayer(decSrc, genKey, i, charArray)
	}

	// Extract data: first 4 chars are the length
	if len(decSrc) < 4 {
		return ""
	}
	dataLen, err := strconv.Atoi(decSrc[:4])
	if err != nil || 4+dataLen > len(decSrc) {
		return ""
	}
	return decSrc[4 : 4+dataLen]
}

// reverseLayer undoes one layer of encryption.
func reverseLayer(decSrc, genKey string, iteration int, charArray []string) string {
	layerKey := genKey + strconv.Itoa(iteration)

	// Compute hash for seed
	hashVal := big.NewInt(0)
	thirty1 := big.NewInt(31)
	mask32 := new(big.Int).SetUint64(0xffffffff)
	for i := 0; i < len(layerKey); i++ {
		hashVal.Mul(hashVal, thirty1)
		hashVal.Add(hashVal, big.NewInt(int64(layerKey[i])))
		hashVal.And(hashVal, mask32)
	}
	seed := hashVal.Uint64()

	seedRand := func(arg int) int {
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		return int(seed % uint64(arg))
	}

	// Build char index map for fast lookups
	charIndex := make(map[string]int, len(charArray))
	for i, c := range charArray {
		charIndex[c] = i
	}

	// Seed shift (reverse)
	runes := []rune(decSrc)
	shifted := make([]rune, len(runes))
	for idx, ch := range runes {
		c := string(ch)
		cIdx, ok := charIndex[c]
		if !ok {
			shifted[idx] = ch
			continue
		}
		randNum := seedRand(95)
		newIdx := (cIdx - randNum + 95) % 95
		shifted[idx] = []rune(charArray[newIdx])[0]
	}
	decSrc = string(shifted)

	// Columnar transposition cipher (reverse)
	decSrc = columnarCipher2(decSrc, layerKey)

	// Generate substitution array via Fisher-Yates shuffle
	subValues := seedShuffle2(charArray, layerKey)

	// Build reverse character map: subValues[i] -> charArray[i]
	charMap := make(map[string]string, len(subValues))
	for i, ch := range subValues {
		charMap[ch] = charArray[i]
	}

	// Apply substitution
	result := make([]rune, len(decSrc))
	for i, ch := range decSrc {
		c := string(ch)
		if replacement, ok := charMap[c]; ok {
			result[i] = []rune(replacement)[0]
		} else {
			result[i] = ch
		}
	}

	return string(result)
}

// keygen2 generates a decryption key from megacloudKey and clientKey.
func keygen2(megacloudKey, clientKey string) string {
	const keygenXORVal = 247
	const keygenShiftVal = 5

	tempKey := megacloudKey + clientKey

	// Numeric hash: hashVal = charCode + hashVal * 31 + (hashVal << 7) - hashVal
	hashVal := big.NewInt(0)
	for i := 0; i < len(tempKey); i++ {
		charVal := big.NewInt(int64(tempKey[i]))
		oldHash := new(big.Int).Set(hashVal)
		// hashVal = BigInt(charCode) + hashVal * 31n + (hashVal << 7n) - hashVal
		mul := new(big.Int).Mul(oldHash, big.NewInt(31))
		shift := new(big.Int).Lsh(oldHash, 7)
		hashVal = new(big.Int).Add(charVal, mul)
		hashVal.Add(hashVal, shift)
		hashVal.Sub(hashVal, oldHash)
	}

	// Absolute value
	if hashVal.Sign() < 0 {
		hashVal.Abs(hashVal)
	}

	// Limit to 63-bit
	maxVal := new(big.Int).SetUint64(0x7fffffffffffffff)
	lHash := new(big.Int).Mod(hashVal, maxVal)
	lHashInt := lHash.Int64()
	if lHashInt < 0 {
		lHashInt = -lHashInt
	}

	// XOR each character
	xored := make([]byte, len(tempKey))
	for i := 0; i < len(tempKey); i++ {
		xored[i] = tempKey[i] ^ keygenXORVal
	}
	tempKeyStr := string(xored)

	// Circular shift
	pivot := int(lHashInt%int64(len(tempKeyStr))) + keygenShiftVal
	if pivot >= len(tempKeyStr) {
		pivot = pivot % len(tempKeyStr)
	}
	tempKeyStr = tempKeyStr[pivot:] + tempKeyStr[:pivot]

	// Leaf interleave with reversed clientKey
	leafStr := reverseString(clientKey)
	maxLen := len(tempKeyStr)
	if len(leafStr) > maxLen {
		maxLen = len(leafStr)
	}
	var returnKey []byte
	for i := 0; i < maxLen; i++ {
		if i < len(tempKeyStr) {
			returnKey = append(returnKey, tempKeyStr[i])
		}
		if i < len(leafStr) {
			returnKey = append(returnKey, leafStr[i])
		}
	}

	// Limit length based on hash
	limit := 96 + int(lHashInt%33)
	if limit > len(returnKey) {
		limit = len(returnKey)
	}
	returnKey = returnKey[:limit]

	// Normalize to printable ASCII (32-126)
	normalized := make([]byte, len(returnKey))
	for i, c := range returnKey {
		normalized[i] = byte(int(c)%95 + 32)
	}

	return string(normalized)
}

// seedShuffle2 performs a Fisher-Yates shuffle on a character array using a seeded PRNG.
func seedShuffle2(charArray []string, iKey string) []string {
	// Hash the key
	hashVal := uint64(0)
	for i := 0; i < len(iKey); i++ {
		hashVal = (hashVal*31 + uint64(iKey[i])) & 0xffffffff
	}

	shuffleNum := hashVal
	pseudoRand := func(arg int) int {
		shuffleNum = (shuffleNum*1103515245 + 12345) & 0x7fffffff
		return int(shuffleNum % uint64(arg))
	}

	// Copy the array
	result := make([]string, len(charArray))
	copy(result, charArray)

	// Fisher-Yates shuffle
	for i := len(result) - 1; i > 0; i-- {
		j := pseudoRand(i + 1)
		result[i], result[j] = result[j], result[i]
	}

	return result
}

// columnarCipher2 performs a columnar transposition cipher.
func columnarCipher2(src, ikey string) string {
	columnCount := len(ikey)
	if columnCount == 0 {
		return src
	}
	rowCount := (len(src) + columnCount - 1) / columnCount

	// Initialize cipher array with spaces
	cipher := make([][]byte, rowCount)
	for i := range cipher {
		cipher[i] = make([]byte, columnCount)
		for j := range cipher[i] {
			cipher[i][j] = ' '
		}
	}

	// Build key-index map sorted by character code
	type keyIdx struct {
		char byte
		idx  int
	}
	keyMap := make([]keyIdx, columnCount)
	for i := 0; i < columnCount; i++ {
		keyMap[i] = keyIdx{char: ikey[i], idx: i}
	}

	// Sort by character code (stable)
	sortedMap := make([]keyIdx, len(keyMap))
	copy(sortedMap, keyMap)
	// Simple insertion sort (stable)
	for i := 1; i < len(sortedMap); i++ {
		j := i
		for j > 0 && sortedMap[j].char < sortedMap[j-1].char {
			sortedMap[j], sortedMap[j-1] = sortedMap[j-1], sortedMap[j]
			j--
		}
	}

	// Fill cipher array column by column in sorted key order
	srcIndex := 0
	for _, ki := range sortedMap {
		for row := 0; row < rowCount; row++ {
			if srcIndex < len(src) {
				cipher[row][ki.idx] = src[srcIndex]
				srcIndex++
			}
		}
	}

	// Read row by row
	var result []byte
	for row := 0; row < rowCount; row++ {
		for col := 0; col < columnCount; col++ {
			result = append(result, cipher[row][col])
		}
	}

	return string(result)
}

// atob decodes a base64-encoded string (matches JavaScript's atob behavior).
func atob(s string) string {
	// Standard base64 decode
	const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

	// Remove any whitespace
	var encoded []byte
	for _, c := range s {
		if c != '=' && c != '\n' && c != '\r' && c != ' ' {
			encoded = append(encoded, byte(c))
		}
	}

	// Pad to multiple of 4
	for len(encoded)%4 != 0 {
		encoded = append(encoded, '=')
	}

	// Build lookup
	lookup := make(map[byte]int)
	for i, c := range base64Chars {
		lookup[byte(c)] = i
	}

	var result []byte
	for i := 0; i < len(encoded); i += 4 {
		var vals [4]int
		count := 0
		for j := 0; j < 4 && i+j < len(encoded); j++ {
			if encoded[i+j] == '=' {
				vals[j] = 0
			} else {
				v, ok := lookup[encoded[i+j]]
				if !ok {
					vals[j] = 0
				} else {
					vals[j] = v
					count = j + 1
				}
			}
		}

		if count >= 2 {
			result = append(result, byte((vals[0]<<2)|(vals[1]>>4)))
		}
		if count >= 3 {
			result = append(result, byte(((vals[1]&0xf)<<4)|(vals[2]>>2)))
		}
		if count >= 4 {
			result = append(result, byte(((vals[2]&0x3)<<6)|vals[3]))
		}
	}

	return string(result)
}

// reverseString reverses a string.
func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}
