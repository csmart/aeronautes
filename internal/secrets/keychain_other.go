//go:build !linux

package secrets

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

func newKeychainStore(ctx context.Context) (*KeychainStore, error) {
	probe, err := probeName()
	if err != nil {
		return nil, err
	}
	if err := setKeychainValue(ctx, probe, "probe"); err != nil {
		_, _ = awaitKeychain(context.Background(), "delete", func() (string, error) {
			return "", keyring.Delete(service, probe)
		})
		return nil, fmt.Errorf("keychain unavailable: %w", err)
	}
	if _, err := awaitKeychain(ctx, "delete", func() (string, error) {
		return "", keyring.Delete(service, probe)
	}); err != nil {
		return nil, fmt.Errorf("keychain delete failed: %w", err)
	}
	_, err = awaitKeychain(ctx, "read", func() (string, error) {
		_, err := keyring.Get(service, probe)
		return "", err
	})
	if !errors.Is(err, keyring.ErrNotFound) {
		if err == nil {
			return nil, errors.New("keychain probe deletion could not be verified")
		}
		return nil, fmt.Errorf("keychain probe verify: %w", err)
	}
	return &KeychainStore{origin: newOrigin(BackendKeychain)}, nil
}

func (*KeychainStore) Get(ctx context.Context, name string) ([]byte, error) {
	encoded, err := awaitKeychain(ctx, "read", func() (string, error) {
		return keyring.Get(service, name)
	})
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decodeKeychainValue(encoded)
}

func (*KeychainStore) Put(ctx context.Context, name string, value []byte) error {
	return setKeychainValue(ctx, name, base64.StdEncoding.EncodeToString(value))
}

func setKeychainValue(ctx context.Context, name, value string) error {
	if _, err := awaitKeychain(ctx, "write", func() (string, error) {
		if err := keyring.Set(service, name, value); err != nil {
			return "", errors.New("keychain write failed")
		}
		return "", nil
	}); err != nil {
		return err
	}
	stored, err := awaitKeychain(ctx, "read", func() (string, error) {
		return keyring.Get(service, name)
	})
	if err != nil {
		return readBackError(err)
	}
	if stored != value {
		return errors.New("keychain write read-back did not match")
	}
	return nil
}

func (*KeychainStore) Delete(ctx context.Context, name string) error {
	_, err := awaitKeychain(ctx, "delete", func() (string, error) {
		return "", keyring.Delete(service, name)
	})
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
