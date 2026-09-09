package cmd

import (
	"fmt"
	"math/big"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/utils/cryptoutil"
)

func init() {
	rootCmd.AddCommand(newGenPrivateKeyCommand())
}

func newGenPrivateKeyCommand() *cobra.Command {
	var (
		groupID uint64
		chainID uint64
		index   string
		l2type  int
	)

	cmd := &cobra.Command{
		Use:           "gen-private-key",
		Short:         "按 ydyl-gen-accounts 规则生成确定性私钥",
		Long:          "根据 groupID/chainID/index/l2type 生成确定性私钥，规则与 ydyl-gen-accounts/scripts/utils.ts 保持一致。\n\n生成公式：privateKey = 0x + 左补零到 64 位 hex(selectedID[4字节] + index[10字节])。\n其中 l2type=0/1/3 时 selectedID=chainID，l2type=2 时 selectedID=groupID。l2type=3 输出 CIP-37 地址，chainID 同时作为 networkId。",
		Example:       "  ydyl-deploy-client gen-private-key --chainID 324 --index 42 --l2type 0\n  ydyl-deploy-client gen-private-key --groupID 77 --index 42 --l2type 2\n  ydyl-deploy-client gen-private-key --chainID 7654 --index 200000 --l2type 3",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			indexValue, err := validateGenPrivateKeyFlags(cmd, groupID, chainID, index, l2type)
			if err != nil {
				return err
			}

			privateKey, err := cryptoutil.BuildDeterministicPrivateKey(groupID, chainID, indexValue, l2type)
			if err != nil {
				return err
			}
			address, err := buildAddressByL2Type(privateKey, l2type, chainID)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "privateKey=%s\naddress=%s\n", privateKey, address)
			return err
		},
	}

	cmd.Flags().Uint64Var(&groupID, "groupID", 0, "分组 ID；仅 l2type=2 时参与私钥拼接，编码为 4 字节")
	cmd.Flags().Uint64Var(&chainID, "chainID", 0, "链 ID；l2type=0/1/3 时参与私钥拼接（4 字节）；l2type=3 时同时作为 CIP-37 networkId")
	cmd.Flags().StringVar(&index, "index", "", "账户索引（支持十进制或 0x 十六进制；编码为 10 字节）")
	cmd.Flags().IntVar(&l2type, "l2type", 0, "链类型：0/1 使用 chainID，2 使用 groupID，3 使用 chainID 并输出 CIP-37 地址")
	_ = cmd.MarkFlagRequired("index")

	return cmd
}

func validateGenPrivateKeyFlags(cmd *cobra.Command, groupID, chainID uint64, index string, l2type int) (*big.Int, error) {
	if l2type != 0 && l2type != 1 && l2type != 2 && l2type != 3 {
		return nil, fmt.Errorf("l2type 必须为 0/1/2/3，当前为 %d", l2type)
	}
	if l2type == 2 && !cmd.Flags().Changed("groupID") {
		return nil, fmt.Errorf("l2type=2 时必须传入 --groupID")
	}
	if l2type != 2 && !cmd.Flags().Changed("chainID") {
		return nil, fmt.Errorf("l2type=%d 时必须传入 --chainID", l2type)
	}
	if l2type == 2 && groupID == 0 {
		return nil, fmt.Errorf("l2type=2 时 groupID 必须大于 0")
	}
	if l2type == 3 && chainID < 1 {
		return nil, fmt.Errorf("l2type=3 时 chainID 必须大于等于 1")
	}

	indexValue, ok := new(big.Int).SetString(index, 0)
	if !ok {
		return nil, fmt.Errorf("index 必须是有效整数，当前为 %q", index)
	}
	if indexValue.Sign() < 0 {
		return nil, fmt.Errorf("index 必须大于等于 0")
	}

	return indexValue, nil
}

func buildAddressByL2Type(privateKey string, l2type int, chainID uint64) (string, error) {
	if l2type == 3 {
		return cryptoutil.CoreBase32AddressFromPrivateKey(privateKey, chainID)
	}
	return cryptoutil.AddressFromPrivateKey(privateKey, l2type)
}
