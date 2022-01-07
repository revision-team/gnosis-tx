package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core"
)

const ZERO_ADDR = "0x0000000000000000000000000000000000000000"

type safeNonceResponse struct {
	Address         string   `json:"address"`
	Nonce           int64    `json:"nonce"`
	Threshold       int64    `json:"threshold"`
	Owners          []string `json:"owners"`
	MasterCopy      string   `json:"masterCopy"`
	Modules         []string `json:"modules"`
	FallbackHandler string   `json:"fallbackHandler"`
	Guard           string   `json:"guard"`
	Version         string   `json:"version"`
}

func getSafeNonce(safe string) (*int64, error) {
	resp, err := http.Get("https://safe-transaction.rinkeby.gnosis.io/api/v1/safes/" + safe)
	if err != nil {
		return nil, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data safeNonceResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	return &data.Nonce, nil
}

type gasEstimationRequest struct {
	To        string  `json:"to"`
	Value     int64   `json:"value"`
	Data      *string `json:"data"`
	Operation int     `json:"operation"`
	GasToken  *string `json:"gasToken"`
}

type gasEstimationResponse struct {
	SafeTxGas      string `json:"safeTxGas"`
	BaseGas        string `json:"baseGas"`
	DataGas        string `json:"dataGas"`
	OperationalGas string `json:"operationalGas"`
	GasPrice       string `json:"gasPrice"`
	LastUsedNonce  int64  `json:"lastUsedNonce"`
	GasToken       string `json:"gasToken"`
	RefundReceiver string `json:"refundReceiver"`
}

func getGasEstimation(to, safe string, value int64) (*int64, error) {
	request := gasEstimationRequest{
		To:        to,
		Value:     value,
		Data:      nil,
		Operation: 0,
		GasToken:  nil,
	}

	req, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post("https://safe-relay.rinkeby.gnosis.io/api/v2/safes/"+safe+"/transactions/estimate/", "application/json", bytes.NewBuffer(req))
	if err != nil {
		return nil, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data gasEstimationResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	safeTxGas, err := strconv.ParseInt(data.SafeTxGas, 10, 64)
	if err != nil {
		return nil, err
	}

	return &safeTxGas, nil
}

type gnosisTxRequest struct {
	To                      string  `json:"to"`
	Value                   int64   `json:"value"`
	Data                    *string `json:"data"`
	Operation               int64   `json:"operation"`
	GasToken                string  `json:"gasToken"`
	SafeTxGas               int64   `json:"safeTxGas"`
	BaseGas                 int64   `json:"baseGas"`
	GasPrice                int64   `json:"gasPrice"`
	RefundReceiver          string  `json:"refundReceiver"`
	Nonce                   int64   `json:"nonce"`
	ContractTransactionHash string  `json:"contractTransactionHash"`
	Sender                  string  `json:"sender"`
	Signature               string  `json:"signature"`
	Origin                  *string `json:"origin"`
}

type gnosisTxErrResponse struct {
	NonFieldErrors []string `json:"nonFieldErrors"`
}

func sendGnosisTx(from, to, safe string, amount, safeTxGas, nonce int64, hash, signature string) error {
	request := gnosisTxRequest{
		To:                      to,
		Value:                   amount,
		Data:                    nil,
		Operation:               0,
		GasToken:                ZERO_ADDR,
		SafeTxGas:               safeTxGas,
		BaseGas:                 0,
		GasPrice:                0,
		RefundReceiver:          ZERO_ADDR,
		Nonce:                   nonce,
		ContractTransactionHash: hash,
		Sender:                  from,
		Signature:               signature,
		Origin:                  nil,
	}

	req, err := json.Marshal(request)
	if err != nil {
		return err
	}

	resp, err := http.Post("https://safe-transaction.rinkeby.gnosis.io/api/v1/safes/"+safe+"/multisig-transactions/", "application/json", bytes.NewBuffer(req))
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var data gnosisTxErrResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return err
	}

	return errors.New(strings.Join(data.NonFieldErrors, "\n"))
}

func sendTransaction(from, to, safe string, amount int64, privKey string) error {
	// get current safe nonce
	nonce, err := getSafeNonce(safe)
	if err != nil {
		return err
	}

	fmt.Println("nonce:", *nonce)

	// get gas estimation
	safeTxGas, err := getGasEstimation(to, safe, amount)
	if err != nil {
		return err
	}

	fmt.Println("safeTxGas:", safeTxGas)

	// get contract transaction hash
	gnosisSafeTx := core.GnosisSafeTx{
		Sender:         common.NewMixedcaseAddress(common.HexToAddress(from)),
		Safe:           common.NewMixedcaseAddress(common.HexToAddress(safe)),
		To:             common.NewMixedcaseAddress(common.HexToAddress(to)),
		Value:          *math.NewDecimal256(amount),
		GasPrice:       *math.NewDecimal256(0),
		Data:           &hexutil.Bytes{},
		Operation:      0,
		GasToken:       common.HexToAddress(ZERO_ADDR),
		RefundReceiver: common.HexToAddress(ZERO_ADDR),
		BaseGas:        *common.Big0,
		SafeTxGas:      *big.NewInt(*safeTxGas),
		Nonce:          *big.NewInt(*nonce),
	}

	typedData := gnosisSafeTx.ToTypedData()

	domainHash, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return err
	}
	primaryTypeHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return err
	}

	encodedTx := []byte{1, 19}
	encodedTx = append(encodedTx, domainHash...)
	encodedTx = append(encodedTx, primaryTypeHash...)

	encodedTxHash := crypto.Keccak256Hash(encodedTx)

	fmt.Println("encodedTxHash:", encodedTxHash.Hex())

	// sign
	privateKey, err := crypto.HexToECDSA(privKey)
	if err != nil {
		return err
	}

	signature, err := crypto.Sign(encodedTxHash.Bytes(), privateKey)
	if err != nil {
		return err
	}

	if signature[64] == 0 || signature[64] == 1 {
		signature[64] += 27
	}

	// send transaction to gnosis
	if err := sendGnosisTx(from, to, safe, amount, *safeTxGas, *nonce, encodedTxHash.Hex(), hexutil.Encode(signature)); err != nil {
		return err
	}

	return nil
}

func main() {
	var (
		safe    string = "<SAFE_ADDRESS>"
		from    string = "<SIGNER_ADDRESS>"
		to      string = "<RECEIVER_ADDRESS>"
		privKey string = "<SIGNER_PRIVATE_KEY>"
		amount  int64  = 1000000
	)

	// on rinkeby network
	if err := sendTransaction(from, to, safe, amount, privKey); err != nil {
		fmt.Println("error:", err.Error())
	}
}
