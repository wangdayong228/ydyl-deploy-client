package cryptoutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"math/big"
	"testing"
)

func TestEcdsaPrivToWeb3Hex_KnownValue(t *testing.T) {
	// 构造一个曲线上的私钥，D=1（曲线选哪个不影响字符串形式）
	priv := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
		},
		D: big.NewInt(1),
	}

	got := EcdsaPrivToWeb3Hex(priv)
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

	s := EcdsaPrivToWeb3Hex(priv)
	if len(s) != 66 { // 0x + 64 hex chars
		t.Fatalf("unexpected length: got %d, want 66, value=%s", len(s), s)
	}
	if s[:2] != "0x" {
		t.Fatalf("missing 0x prefix: %s", s)
	}
}

func TestBuildDeterministicPrivateKey_UsesChainIDForEVM(t *testing.T) {
	got, err := BuildDeterministicPrivateKey(999, 324, big.NewInt(42), 0)
	if err != nil {
		t.Fatalf("BuildDeterministicPrivateKey error: %v", err)
	}

	want := "0x000000000000000000000000000000000000000001440000000000000000002a"
	if got != want {
		t.Fatalf("deterministic key mismatch, got=%s want=%s", got, want)
	}
}

func TestBuildDeterministicPrivateKey_UsesGroupIDForXJST(t *testing.T) {
	got, err := BuildDeterministicPrivateKey(77, 324, big.NewInt(42), 2)
	if err != nil {
		t.Fatalf("BuildDeterministicPrivateKey error: %v", err)
	}

	want := "0x0000000000000000000000000000000000000000004d0000000000000000002a"
	if got != want {
		t.Fatalf("deterministic key mismatch, got=%s want=%s", got, want)
	}
}

func TestBuildDeterministicPrivateKey_Format(t *testing.T) {
	got, err := BuildDeterministicPrivateKey(0, 1, big.NewInt(1), 1)
	if err != nil {
		t.Fatalf("BuildDeterministicPrivateKey error: %v", err)
	}

	if len(got) != 66 {
		t.Fatalf("unexpected key length: got=%d value=%s", len(got), got)
	}
	if got[:2] != "0x" {
		t.Fatalf("missing 0x prefix: %s", got)
	}
}

func TestBuildDeterministicPrivateKey_RejectsZeroKey(t *testing.T) {
	_, err := BuildDeterministicPrivateKey(0, 0, big.NewInt(0), 0)
	if err == nil {
		t.Fatal("expected zero private key error")
	}
}

func TestBuildDeterministicPrivateKey_RejectsTooLargeIndex(t *testing.T) {
	tooLarge := new(big.Int).Lsh(big.NewInt(1), 80)
	_, err := BuildDeterministicPrivateKey(0, 1, tooLarge, 0)
	if err == nil {
		t.Fatal("expected index overflow error")
	}
}
