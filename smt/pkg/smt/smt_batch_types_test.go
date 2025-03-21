package smt_test

import (
	libcommon "github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon/core/types/accounts"
)

type BatchInsertDataHolder struct {
	acc             accounts.Account
	AddressAccount  libcommon.Address
	AddressContract libcommon.Address
	Bytecode        string
	Storage         map[string]string
}
