package deploy

import (
	"fmt"
	"testing"

	"github.com/openweb3/go-sdk-common/privatekeyhelper"
	"github.com/wangdayong228/ydyl-deploy-client/internal/constants/enums"
	"github.com/wangdayong228/ydyl-deploy-client/internal/utils/cryptoutil"
)

func TestResolveL1VaultPrivateKey_MatchesPrivateKeyHelper(t *testing.T) {
	t.Parallel()

	d := &Deployer{l1VaultDeriveRand: 20260302}
	mnemonic := "test test test test test test test test test test test junk"
	serviceType := enums.ServiceTypeOP
	index := 10001

	got, err := d.resolveL1VaultPrivateKey(mnemonic, serviceType, index)
	if err != nil {
		t.Fatalf("resolveL1VaultPrivateKey returned error: %v", err)
	}
	if got == nil {
		t.Fatalf("resolveL1VaultPrivateKey returned nil key")
	}

	want, err := privatekeyhelper.NewFromMnemonic(mnemonic, index, &privatekeyhelper.MnemonicOption{
		BaseDerivePath: fmt.Sprintf("m/44'/60'/%d/%d", d.l1VaultDeriveRand, int(serviceType)),
	})
	if err != nil {
		t.Fatalf("privatekeyhelper.NewFromMnemonic returned error: %v", err)
	}

	gotHex := cryptoutil.EcdsaPrivToWeb3Hex(got)
	wantHex := cryptoutil.EcdsaPrivToWeb3Hex(want)
	if gotHex != wantHex {
		t.Fatalf("derived key mismatch: got=%s want=%s", gotHex, wantHex)
	}
}

func TestResolveL1VaultPrivateKey_SameDeployerStable(t *testing.T) {
	t.Parallel()

	d := &Deployer{l1VaultDeriveRand: 88}
	mnemonic := "test test test test test test test test test test test junk"
	serviceType := enums.ServiceTypeCDK
	index := 10002

	first, err := d.resolveL1VaultPrivateKey(mnemonic, serviceType, index)
	if err != nil {
		t.Fatalf("first resolveL1VaultPrivateKey returned error: %v", err)
	}
	second, err := d.resolveL1VaultPrivateKey(mnemonic, serviceType, index)
	if err != nil {
		t.Fatalf("second resolveL1VaultPrivateKey returned error: %v", err)
	}

	if cryptoutil.EcdsaPrivToWeb3Hex(first) != cryptoutil.EcdsaPrivToWeb3Hex(second) {
		t.Fatalf("same deployer should derive same key for same input")
	}
}

func TestResolveL1VaultPrivateKey_DifferentDeployerDifferent(t *testing.T) {
	t.Parallel()

	mnemonic := "test test test test test test test test test test test junk"
	serviceType := enums.ServiceTypeOP
	index := 10001

	d1 := &Deployer{l1VaultDeriveRand: 1}
	d2 := &Deployer{l1VaultDeriveRand: 2}

	first, err := d1.resolveL1VaultPrivateKey(mnemonic, serviceType, index)
	if err != nil {
		t.Fatalf("d1 resolveL1VaultPrivateKey returned error: %v", err)
	}
	second, err := d2.resolveL1VaultPrivateKey(mnemonic, serviceType, index)
	if err != nil {
		t.Fatalf("d2 resolveL1VaultPrivateKey returned error: %v", err)
	}

	if cryptoutil.EcdsaPrivToWeb3Hex(first) == cryptoutil.EcdsaPrivToWeb3Hex(second) {
		t.Fatalf("different deployers with different random segment should derive different keys")
	}
}

func TestResolveXjstGroupIps(t *testing.T) {
	t.Parallel()

	d := &Deployer{}
	globalIps := []string{"192.168.1.1", "192.168.1.2", "192.168.1.3", "192.168.1.4"}
	groupId := 1
	got, err := d.resolveXjstGroupIps(globalIps, groupId)
	if err != nil {
		t.Fatalf("resolveXjstGroupIps returned error: %v", err)
	}
	want := "[192.168.1.1,192.168.1.2,192.168.1.3,192.168.1.4]"
	if got != want {
		t.Fatalf("resolveXjstGroupIps returned wrong result: got=%s want=%s", got, want)
	}
}

func TestResolveXjstGroupIps_OutOfRange(t *testing.T) {
	t.Parallel()

	d := &Deployer{}
	globalIps := []string{"192.168.1.1", "192.168.1.2", "192.168.1.3", "192.168.1.4"}

	_, err := d.resolveXjstGroupIps(globalIps, 2)
	if err == nil {
		t.Fatalf("resolveXjstGroupIps expected error for out-of-range groupId")
	}
}
