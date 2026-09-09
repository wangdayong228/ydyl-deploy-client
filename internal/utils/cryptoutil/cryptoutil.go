package cryptoutil

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

func EcdsaPrivToWeb3Hex(priv *ecdsa.PrivateKey) string {
	return fmt.Sprintf("0x%064x", priv.D)
}

var maxDeterministicIndex = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 80), big.NewInt(1))

func BuildDeterministicPrivateKey(groupID, chainID uint64, index *big.Int, l2type int) (string, error) {
	if l2type != 0 && l2type != 1 && l2type != 2 && l2type != 3 {
		return "", fmt.Errorf("invalid l2type: %d", l2type)
	}
	if index == nil {
		return "", errors.New("index is required")
	}
	if index.Sign() < 0 {
		return "", errors.New("index must be non-negative")
	}
	if index.Cmp(maxDeterministicIndex) > 0 {
		return "", fmt.Errorf("index exceeds 10-byte limit: %s", index.String())
	}

	chainOrGroupID := chainID
	if l2type == 2 {
		chainOrGroupID = groupID
	}
	if chainOrGroupID > math.MaxUint32 {
		return "", fmt.Errorf("selected chain/group id exceeds 4-byte limit: %d", chainOrGroupID)
	}

	suffix := fmt.Sprintf("%08x%s", chainOrGroupID, leftPadHex(index.Text(16), 20))
	pkHex := "0x" + leftPadHex(suffix, 64)
	if pkHex == "0x"+strings.Repeat("0", 64) {
		return "", errors.New("private key cannot be zero")
	}
	return pkHex, nil
}

func AddressFromPrivateKey(pkHex string, l2type int) (string, error) {
	if l2type != 0 && l2type != 1 && l2type != 2 {
		return "", fmt.Errorf("invalid l2type: %d", l2type)
	}
	trimmed := strings.TrimSpace(pkHex)
	trimmed = strings.TrimPrefix(trimmed, "0x")
	trimmed = strings.TrimPrefix(trimmed, "0X")
	priv, err := crypto.HexToECDSA(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid private key: %w", err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey).Hex()
	if l2type != 2 {
		return strings.ToLower(addr), nil
	}
	if len(addr) < 3 {
		return "", fmt.Errorf("invalid ethereum address: %s", addr)
	}
	return strings.ToLower("0x1" + addr[3:]), nil
}

func CoreBase32AddressFromPrivateKey(pkHex string, networkID uint64) (string, error) {
	if networkID == 0 {
		return "", fmt.Errorf("invalid networkId: %d", networkID)
	}
	trimmed := strings.TrimSpace(pkHex)
	trimmed = strings.TrimPrefix(trimmed, "0x")
	trimmed = strings.TrimPrefix(trimmed, "0X")
	priv, err := crypto.HexToECDSA(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid private key: %w", err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey).Hex()
	if len(addr) < 3 {
		return "", fmt.Errorf("invalid ethereum address: %s", addr)
	}
	typed := strings.ToLower("0x1" + addr[3:])
	return encodeCIP37(typed, networkID)
}

func leftPadHex(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return strings.Repeat("0", width-len(value)) + value
}
