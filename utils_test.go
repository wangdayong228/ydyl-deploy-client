package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"math/big"
	"testing"
)

// ecdsaPrivToWeb3Hex 将 ECDSA 私钥转换为以太坊/web3 常用的 0x 前缀 64 位十六进制字符串。
// 注意：这里仅使用 priv.D（标量），不包含曲线或其他元信息。
func ecdsaPrivToWeb3Hex(priv *ecdsa.PrivateKey) string {
	// 始终补足 64 个十六进制字符（32 字节），不带负号。
	return fmt.Sprintf("0x%064x", priv.D)
}

func TestEcdsaPrivToWeb3Hex_KnownValue(t *testing.T) {
	// 构造一个曲线上的私钥，D=1（曲线选哪个不影响字符串形式）
	priv := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
		},
		D: big.NewInt(1),
	}

	got := ecdsaPrivToWeb3Hex(priv)
	want := "0x0000000000000000000000000000000000000000000000000000000000000001"

	if got != want {
		t.Fatalf("ecdsaPrivToWeb3Hex(D=1) = %s, want %s", got, want)
	}
}

func TestEcdsaPrivToWeb3Hex_RandomKey_Format(t *testing.T) {
	// 用标准库生成一把随机 ECDSA 私钥（曲线无关紧要，我们只关心 D 的编码形式）
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey error: %v", err)
	}

	s := ecdsaPrivToWeb3Hex(priv)

	if len(s) != 66 { // 0x + 64 hex chars
		t.Fatalf("unexpected length: got %d, want 66, value=%s", len(s), s)
	}
	if s[:2] != "0x" {
		t.Fatalf("missing 0x prefix: %s", s)
	}
}
