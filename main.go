package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"math/rand"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/fatih/color"
)

const (
	rpcURL         = "https://rpc.testnet.soniclabs.com/"
	explorerURL    = "https://testnet.soniclabs.com/tx/"
	minEthToSend   = 0.000001
	maxEthToSend   = 0.000001
	minGasPrice    = 10000000000 // 10 gwei
	maxGasPrice    = 20000000000 // 20 gwei
	gasLimit       = 21000
	retryDelay     = 1 * time.Second
	maxRetries     = 2
	chainID int64  = 64165
	weiPerEth      = 1e18
	gweiPerWei     = 1e9
)

func main() {
    printHeader()

    privateKeys := loadPrivateKeys("privateKeys.json")

    client, err := ethclient.Dial(rpcURL)
    if err != nil {
        log.Fatalf("Failed to connect to the Ethereum client: %v", err)
    }

    fmt.Print("Enter the number of transactions per wallet: ")
    var numTransactions int
    fmt.Scanf("%d", &numTransactions)

    fmt.Print("Enter time between transactions (in seconds): ")
    var waitTimeSeconds int
    fmt.Scanf("%d", &waitTimeSeconds)
    if waitTimeSeconds <= 0 {
        waitTimeSeconds = 1 // Set minimum 1 second
    }
    waitTime := time.Duration(waitTimeSeconds) * time.Second

    wallets := make([]*bind.TransactOpts, len(privateKeys))
    transactionCounts := make([]int, len(privateKeys))
    for i, key := range privateKeys {
        wallets[i] = createWallet(client, key)
        address := wallets[i].From.Hex()
        fmt.Printf("Wallet %s will perform %d transactions\n", shortenAddress(address), numTransactions)
    }

    totalTransactions := numTransactions * len(wallets)
    for i := 0; i < totalTransactions; i++ {
        walletIndex := i % len(wallets)
        wallet := wallets[walletIndex]
        senderAddress := wallet.From

        senderBalance := checkBalanceWithRetry(client, senderAddress, maxRetries)
        if senderBalance.Cmp(ethToWei(0.001)) < 0 {
            fmt.Printf("Wallet %s has insufficient balance, skipping to next transaction\n", shortenAddress(senderAddress.Hex()))
            continue
        }

        receiverAddress := common.HexToAddress(generateAddress())
        amountToSend := randomEth(minEthToSend, maxEthToSend)
        gasPrice := randomGasPrice(minGasPrice, maxGasPrice)

        tx := types.NewTransaction(
            nonceAtWithRetry(client, senderAddress, maxRetries),
            receiverAddress,
            amountToSend,
            gasLimit,
            gasPrice,
            nil,
        )

        signedTx, err := wallet.Signer(wallet.From, tx)
        if err != nil {
            fmt.Println("Transaction signing failed, skipping to next transaction")
            continue
        }

        err = retry(maxRetries, retryDelay, func() error {
            return client.SendTransaction(context.Background(), signedTx)
        })
        if err != nil {
            fmt.Println("Failed to send transaction, skipping to next transaction")
            continue
        }

        transactionCounts[walletIndex]++

		// Format timestamp with RGB color
	    timestamp := time.Now().Format("2006/01/02 15:04:05")

        // Format log output
        txURL := fmt.Sprintf("%s%s", explorerURL, signedTx.Hash().Hex())
        fromColor := color.New(color.FgCyan).SprintFunc()
        toColor := color.New(color.FgMagenta).SprintFunc()
        amountColor := color.New(color.FgYellow).SprintFunc()
        txURLColor := color.New(color.FgGreen).SprintFunc()

        fmt.Printf("Time  : %s\nNumber: %d\nFrom  : %s\nTo Ann: %s\nAmount: %s ETH,\nTx Ann: %s\n\n",
            timestamp, // Ensure timestamp is used here
            transactionCounts[walletIndex],
            fromColor(shortenAddress(senderAddress.Hex())),
            toColor(shortenAddress(receiverAddress.Hex())),
            amountColor(weiToEth(amountToSend)),
            txURLColor(txURL),
        )
        // Print separator line
		fmt.Println("******************************************************")
        time.Sleep(waitTime)
    }
}

func printHeader() {
	c := color.New(color.FgCyan).Add(color.Bold)
	c.Println("***************************************************")
	c.Println(`               Batch Send EVM Wallet               `)
	c.Println(`                                                   `)
	c.Println(`                Editor : FlipZ3ro0                 `)
	c.Println("***************************************************")
}

func loadPrivateKeys(filename string) []string {
	file, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatalf("Failed to read private keys file: %v", err)
	}
	var keys []string
	if err := json.Unmarshal(file, &keys); err != nil {
		log.Fatalf("Failed to unmarshal private keys: %v", err)
	}
	return keys
}

func createWallet(client *ethclient.Client, privateKeyHex string) *bind.TransactOpts {
	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		log.Fatalf("Failed to load private key: %v", err)
	}
	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		log.Fatalf("Failed to cast public key to ECDSA")
	}
	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)
	nonce := nonceAtWithRetry(client, fromAddress, maxRetries)

	chainID := big.NewInt(chainID)
	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		log.Fatalf("Failed to suggest gas price: %v", err)
	}

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, chainID)
	if err != nil {
		log.Fatalf("Failed to create transactor: %v", err)
	}

	auth.Nonce = big.NewInt(int64(nonce))
	auth.Value = big.NewInt(0) // in wei
	auth.GasLimit = uint64(gasLimit)
	auth.GasPrice = gasPrice

	return auth
}

func nonceAtWithRetry(client *ethclient.Client, address common.Address, retries int) uint64 {
	var nonce uint64
	var err error
	for i := 0; i < retries; i++ {
		nonce, err = client.PendingNonceAt(context.Background(), address)
		if err == nil {
			return nonce
		}
		fmt.Println("Failed to get nonce, retrying...")
		time.Sleep(retryDelay)
	}
	fmt.Println("Failed to get nonce after multiple attempts, skipping to next transaction")
	return 0
}

func checkBalanceWithRetry(client *ethclient.Client, address common.Address, retries int) *big.Int {
	var balance *big.Int
	var err error
	for i := 0; i < retries; i++ {
		balance, err = client.BalanceAt(context.Background(), address, nil)
		if err == nil {
			return balance
		}
		fmt.Println("Failed to get balance, retrying...")
		time.Sleep(retryDelay)
	}
	fmt.Println("Failed to get balance after multiple attempts, skipping to next transaction")
	return big.NewInt(0)
}

func generateAddress() string {
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		log.Fatalf("Failed to generate address: %v", err)
	}
	return crypto.PubkeyToAddress(privateKey.PublicKey).Hex()
}

func ethToWei(amount float64) *big.Int {
	amountStr := strconv.FormatFloat(amount, 'f', 18, 64)
	amountWei, _b := new(big.Float).SetString(amountStr)
	wei := new(big.Float).Mul(amountWei, big.NewFloat(weiPerEth))
	weiInt, _ := wei.Int(nil)
	return weiInt
}

func weiToEth(amount *big.Int) string {
	ethValue := new(big.Float).Quo(new(big.Float).SetInt(amount), big.NewFloat(weiPerEth))
	ethStr := fmt.Sprintf("%.6f", ethValue)
	return ethStr
}

func shortenAddress(address string) string {
	return address[:6] + "..." + address[len(address)-4:]
}

func randomEth(min, max float64) *big.Int {
	randomAmount := min + rand.Float64()*(max-min)
	return ethToWei(randomAmount)
}

func randomGasPrice(min, max int64) *big.Int {
	if min >= max {
		log.Fatalf("Invalid gas price range: min (%d) must be less than max (%d)", min, max)
	}
	return big.NewInt(min + rand.Int63n(max-min+1))
}

func retry(retries int, delay time.Duration, fn func() error) error {
	for i := 0; i < retries; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		fmt.Printf("Retrying due to error: %v\n", err)
		time.Sleep(delay)
	}
	return fmt.Errorf("failed after %d retries", retries)
}
