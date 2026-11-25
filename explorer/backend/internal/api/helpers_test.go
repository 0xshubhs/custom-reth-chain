package api

import (
	"encoding/hex"
	"math/big"
)

type bigIntT = big.Int

func newBig(v int64) *big.Int { return big.NewInt(v) }

func wordHex(w []byte) string { return "0x" + hex.EncodeToString(w) }

func hexs(b []byte) string { return hex.EncodeToString(b) }
