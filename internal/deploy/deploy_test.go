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
	chainID := 10001

	got, err := d.resolveL1VaultPrivateKey(mnemonic, serviceType, chainID)
	if err != nil {
		t.Fatalf("resolveL1VaultPrivateKey returned error: %v", err)
	}
	if got == nil {
		t.Fatalf("resolveL1VaultPrivateKey returned nil key")
	}

	want, err := privatekeyhelper.NewFromMnemonic(mnemonic, chainID, &privatekeyhelper.MnemonicOption{
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
