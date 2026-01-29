package deploy

import (
	"fmt"
	"testing"

	"github.com/openweb3/go-sdk-common/privatekeyhelper"
	"github.com/wangdayong228/ydyl-deploy-client/internal/constants/enums"
	"github.com/wangdayong228/ydyl-deploy-client/internal/cryptoutil"
)

func TestResolveL1VaultPrivateKey_MatchesPrivateKeyHelper(t *testing.T) {
	t.Parallel()

	d := &Deployer{}
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
		BaseDerivePath: fmt.Sprintf("m/44'/60'/0'/%d", int(serviceType)),
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

func TestResolveXjstGroupIps(t *testing.T) {
	t.Parallel()

	d := &Deployer{}
	globalIps := []string{"192.168.1.1", "192.168.1.2", "192.168.1.3", "192.168.1.4"}
	groupId := 0
	got := d.resolveXjstGroupIps(globalIps, groupId)
	want := "[192.168.1.1,192.168.1.2,192.168.1.3,192.168.1.4]"
	if got != want {
		t.Fatalf("resolveXjstGroupIps returned wrong result: got=%s want=%s", got, want)
	}
}
