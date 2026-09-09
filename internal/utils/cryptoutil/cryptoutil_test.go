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

func TestBuildDeterministicPrivateKey_UsesChainIDForCore(t *testing.T) {
	got, err := BuildDeterministicPrivateKey(999, 324, big.NewInt(42), 3)
	if err != nil {
		t.Fatalf("BuildDeterministicPrivateKey error: %v", err)
	}

	want := "0x000000000000000000000000000000000000000001440000000000000000002a"
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

func TestAddressFromPrivateKey_EVMVector(t *testing.T) {
	pk, err := BuildDeterministicPrivateKey(0, 10000, big.NewInt(200000), 1)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	got, err := AddressFromPrivateKey(pk, 1)
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	want := "0xfc737023702a09c01260252d853033ccaa587b5d"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestAddressFromPrivateKey_XJSTVector(t *testing.T) {
	pk, err := BuildDeterministicPrivateKey(1, 0, big.NewInt(12345), 2)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	got, err := AddressFromPrivateKey(pk, 2)
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	want := "0x1d22176670f087456f2760405469b25917eed45b"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestAddressFromPrivateKey_RejectsInvalidL2Type(t *testing.T) {
	pk, err := BuildDeterministicPrivateKey(0, 1, big.NewInt(1), 1)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if _, err := AddressFromPrivateKey(pk, 3); err == nil {
		t.Fatal("expected invalid l2type error")
	}
}

func TestCoreBase32AddressFromPrivateKey_SDKExample(t *testing.T) {
	pk := "0x0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err := CoreBase32AddressFromPrivateKey(pk, 1)
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	want := "cfxtest:aasm4c231py7j34fghntcfkdt2nm9xv1tu6jd3r1s7"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestCoreBase32AddressFromPrivateKey_Chain7654Index200000(t *testing.T) {
	pk, err := BuildDeterministicPrivateKey(0, 7654, big.NewInt(200000), 3)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	got, err := CoreBase32AddressFromPrivateKey(pk, 7654)
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	want := "net7654:aamwc2x2hjvcwsvnadhv2xxkrfkfhjspvjdkcyw1u8"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestCoreBase32AddressFromPrivateKey_NetworkPrefixes(t *testing.T) {
	pk, err := BuildDeterministicPrivateKey(0, 7654, big.NewInt(200000), 3)
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	cases := []struct {
		networkID uint64
		want      string
	}{
		{1, "cfxtest:aamwc2x2hjvcwsvnadhv2xxkrfkfhjspvjbx5wwbn9"},
		{1029, "cfx:aamwc2x2hjvcwsvnadhv2xxkrfkfhjspvjn2jcyntz"},
	}
	for _, tc := range cases {
		got, err := CoreBase32AddressFromPrivateKey(pk, tc.networkID)
		if err != nil {
			t.Fatalf("networkID=%d: %v", tc.networkID, err)
		}
		if got != tc.want {
			t.Fatalf("networkID=%d got %s want %s", tc.networkID, got, tc.want)
		}
	}
}

func TestCoreBase32AddressFromPrivateKey_RejectsZeroNetworkID(t *testing.T) {
	pk := "0x0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, err := CoreBase32AddressFromPrivateKey(pk, 0); err == nil {
		t.Fatal("expected zero networkID error")
	}
}
