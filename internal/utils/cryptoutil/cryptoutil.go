package cryptoutil

import (
	"crypto/ecdsa"
	"fmt"
)

func EcdsaPrivToWeb3Hex(priv *ecdsa.PrivateKey) string {
	return fmt.Sprintf("0x%064x", priv.D)
}

