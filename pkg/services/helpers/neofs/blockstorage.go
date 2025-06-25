package neofs

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nspcc-dev/neo-go/pkg/config"
	"github.com/nspcc-dev/neo-go/pkg/wallet"
	cid "github.com/nspcc-dev/neofs-sdk-go/container/id"
	"github.com/nspcc-dev/neofs-sdk-go/pool"
	"github.com/nspcc-dev/neofs-sdk-go/user"
)

// Constants related to NeoFS block storage.
const (
	// DefaultTimeout is the default timeout for NeoFS requests.
	DefaultTimeout = 10 * time.Minute
	// DefaultDownloaderWorkersCount is the default number of workers downloading blocks.
	DefaultDownloaderWorkersCount = 500
	// DefaultBatchSize is the default size of the batch to upload in parallel
	// and search latest fully uploaded batch.
	DefaultBatchSize = 128000
	// DefaultBlockAttribute is the default attribute name for block objects.
	DefaultBlockAttribute = "Block"
	// DefaultStateAttribute is the default attribute name for state objects.
	DefaultStateAttribute = "State"
	// DefaultKVBatchSize is a number of contract storage key-value objects to
	// flush to the node's DB in a batch.
	DefaultKVBatchSize = 1000
	// DefaultSearchBatchSize is a number of objects to search in a batch.
	DefaultSearchBatchSize = 1000
)

// Constants related to NeoFS pool request timeouts.
const (
	// DefaultDialTimeout is a default timeout used to establish connection with
	// NeoFS storage nodes.
	DefaultDialTimeout = 30 * time.Second
	// DefaultStreamTimeout is a default timeout used for NeoFS streams processing.
	// It has significantly large value to reliably avoid timeout problems with heavy
	// SEARCH requests.
	DefaultStreamTimeout = 10 * time.Minute
	// DefaultHealthcheckTimeout is a timeout for request to NeoFS storage node to
	// decide if it is alive.
	DefaultHealthcheckTimeout = 10 * time.Second
)

// Constants related to retry mechanism.
const (
	// MaxRetries is the maximum number of retries for a single operation.
	MaxRetries = 5
	// InitialBackoff is the initial backoff duration.
	InitialBackoff = 500 * time.Millisecond
	// BackoffFactor is the factor by which the backoff duration is multiplied.
	BackoffFactor = 2
	// MaxBackoff is the maximum backoff duration.
	MaxBackoff = 20 * time.Second
)

// BasicService is a minimal service structure for NeoFS fetchers.
type BasicService struct {
	Pool        *pool.Pool
	Account     *wallet.Account
	ContainerID cid.ID
	Ctx         context.Context
	CtxCancel   context.CancelFunc
}

// NewBasicService creates a new BasicService instance.
func NewBasicService(cfg config.NeoFSService) (BasicService, error) {
	var (
		account     *wallet.Account
		containerID cid.ID
		err         error
	)
	if cfg.UnlockWallet.Path != "" {
		walletFromFile, err := wallet.NewWalletFromFile(cfg.UnlockWallet.Path)
		if err != nil {
			return BasicService{}, err
		}
		for _, acc := range walletFromFile.Accounts {
			if err = acc.Decrypt(cfg.UnlockWallet.Password, walletFromFile.Scrypt); err == nil {
				account = acc
				break
			}
		}
		if account == nil {
			return BasicService{}, errors.New("failed to decrypt any account in the wallet")
		}
	} else {
		account, err = wallet.NewAccount()
		if err != nil {
			return BasicService{}, err
		}
	}
	params := pool.DefaultOptions()
	params.SetHealthcheckTimeout(DefaultHealthcheckTimeout)
	params.SetNodeDialTimeout(DefaultDialTimeout)
	params.SetNodeStreamTimeout(DefaultStreamTimeout)
	p, err := pool.New(pool.NewFlatNodeParams(cfg.Addresses), user.NewAutoIDSignerRFC6979(account.PrivateKey().PrivateKey), params)
	if err != nil {
		return BasicService{}, err
	}

	err = containerID.DecodeString(cfg.ContainerID)
	if err != nil {
		return BasicService{}, errors.New("failed to decode container ID: " + err.Error())
	}
	return BasicService{
		Account:     account,
		ContainerID: containerID,
		Pool:        p,
	}, nil
}

// Retry is a retry mechanism for executing an action with exponential backoff.
func (sfs *BasicService) Retry(action func() error) error {
	var (
		err     error
		backoff = InitialBackoff
		timer   = time.NewTimer(0)
	)

	for i := range MaxRetries {
		if err = action(); err == nil {
			return nil
		}
		if i == MaxRetries-1 {
			break
		}
		timer.Reset(backoff)

		select {
		case <-timer.C:
		case <-sfs.Ctx.Done():
			return sfs.Ctx.Err()
		}
		backoff *= time.Duration(BackoffFactor)
		if backoff > MaxBackoff {
			backoff = MaxBackoff
		}
	}
	return err
}

// IsContextCanceledErr returns whether error is a wrapped [context.Canceled].
// Ref. https://github.com/nspcc-dev/neofs-sdk-go/issues/624.
func IsContextCanceledErr(err error) bool {
	return errors.Is(err, context.Canceled) ||
		strings.Contains(err.Error(), "context canceled")
}
