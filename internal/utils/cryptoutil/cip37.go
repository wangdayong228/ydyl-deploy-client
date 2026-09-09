package cryptoutil

import (
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strings"
)

const cip37Alphabet = "ABCDEFGHJKMNPRSTUVWXYZ0123456789"

var (
	cip37Mask        = new(big.Int).SetUint64(0x07ffffffff)
	cip37Gen0        = mustBigInt("98f2bc8e61")
	cip37Gen1        = mustBigInt("79b76d99e2")
	cip37Gen2        = mustBigInt("f33e5fb3c4")
	cip37Gen3        = mustBigInt("ae2eabe2a8")
	cip37Gen4        = mustBigInt("1e4f43e470")
	cip37One         = big.NewInt(1)
	cip37NetIDLimit  = uint64(math.MaxUint32)
	cip37VersionByte = byte(0)
)

func mustBigInt(hexDigits string) *big.Int {
	n, ok := new(big.Int).SetString(hexDigits, 16)
	if !ok {
		panic("invalid hex bigint: " + hexDigits)
	}
	return n
}

func encodeCIP37(hexAddress string, networkID uint64) (string, error) {
	if networkID == 0 || networkID > cip37NetIDLimit {
		return "", fmt.Errorf("invalid networkId: %d", networkID)
	}
	trimmed := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(hexAddress), "0x"), "0X")
	addrBytes, err := hex.DecodeString(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid hex address: %w", err)
	}
	if len(addrBytes) < 20 {
		return "", fmt.Errorf("hex address must be at least 20 bytes")
	}

	netName := strings.ToUpper(cip37Prefix(networkID))
	netName5Bits := make([]byte, len(netName))
	for i := 0; i < len(netName); i++ {
		netName5Bits[i] = netName[i] & 31
	}

	payloadIn := append([]byte{cip37VersionByte}, addrBytes...)
	payload5Bits := convertBit(payloadIn, 8, 5, true)

	checksumIn := make([]byte, 0, len(netName5Bits)+1+len(payload5Bits)+8)
	checksumIn = append(checksumIn, netName5Bits...)
	checksumIn = append(checksumIn, 0)
	checksumIn = append(checksumIn, payload5Bits...)
	checksumIn = append(checksumIn, 0, 0, 0, 0, 0, 0, 0, 0)

	checksumBytes, err := checksumToBytes(polyMod(checksumIn))
	if err != nil {
		return "", err
	}
	checksum5Bits := convertBit(checksumBytes, 8, 5, true)

	payload := encodeAlphabet(payload5Bits)
	checksum := encodeAlphabet(checksum5Bits)
	return strings.ToLower(netName + ":" + payload + checksum), nil
}

func cip37Prefix(networkID uint64) string {
	switch networkID {
	case 1:
		return "cfxtest"
	case 1029:
		return "cfx"
	default:
		return fmt.Sprintf("net%d", networkID)
	}
}

func encodeAlphabet(values []byte) string {
	var b strings.Builder
	b.Grow(len(values))
	for _, v := range values {
		b.WriteByte(cip37Alphabet[v])
	}
	return b.String()
}

func convertBit(data []byte, inBits, outBits uint, pad bool) []byte {
	mask := (1 << outBits) - 1
	array := make([]byte, 0, (len(data)*int(inBits)+int(outBits)-1)/int(outBits))
	var bits uint
	value := 0
	for _, b := range data {
		bits += inBits
		value = (value << inBits) | int(b)
		for bits >= outBits {
			bits -= outBits
			array = append(array, byte((value>>bits)&mask))
		}
	}
	value = (value << (outBits - bits)) & mask
	if bits > 0 && pad {
		array = append(array, byte(value))
	}
	return array
}

func polyMod(values []byte) *big.Int {
	checksum := big.NewInt(1)
	gens := []*big.Int{cip37Gen0, cip37Gen1, cip37Gen2, cip37Gen3, cip37Gen4}
	for _, v := range values {
		high := new(big.Int).Rsh(checksum, 35)
		checksum.And(checksum, cip37Mask)
		checksum.Lsh(checksum, 5)
		if v != 0 {
			checksum.Xor(checksum, big.NewInt(int64(v)))
		}
		for i := 0; i < 5; i++ {
			if high.Bit(i) == 1 {
				checksum.Xor(checksum, gens[i])
			}
		}
	}
	return checksum.Xor(checksum, cip37One)
}

func checksumToBytes(checksum *big.Int) ([]byte, error) {
	s := checksum.Text(16)
	if len(s)%2 == 1 {
		s = "0" + s
	}
	if len(s) < 10 {
		s = strings.Repeat("0", 10-len(s)) + s
	}
	return hex.DecodeString(s)
}
