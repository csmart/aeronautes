//go:build linux

package secrets

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
	ss "github.com/zalando/go-keyring/secret_service"
)

const (
	ssServiceName  = "org.freedesktop.secrets"
	ssServicePath  = "/org/freedesktop/secrets"
	ssAliasDefault = "/org/freedesktop/secrets/aliases/default"
	ssReadAlias    = "org.freedesktop.Secret.Service.ReadAlias"
)

func newKeychainStore(ctx context.Context) (*KeychainStore, error) {
	svc, err := ss.NewSecretService()
	if err != nil {
		return nil, fmt.Errorf("keychain unavailable: %w", err)
	}
	pin, persistent, err := resolveCollection(svc)
	if err != nil {
		return nil, fmt.Errorf("keychain unavailable: %w", err)
	}
	origin := newOrigin(BackendKeychain)
	origin.Persistent = persistent
	store := &KeychainStore{origin: origin, pin: pin}

	probe, err := probeName()
	if err != nil {
		return nil, err
	}
	if err := store.setValue(ctx, probe, "probe"); err != nil {
		_, _ = awaitKeychain(context.Background(), "delete", func() (string, error) {
			return "", store.deletePinned(probe)
		})
		return nil, fmt.Errorf("keychain unavailable: %w", err)
	}
	if _, err := awaitKeychain(ctx, "delete", func() (string, error) {
		return "", store.deletePinned(probe)
	}); err != nil {
		return nil, fmt.Errorf("keychain delete failed: %w", err)
	}
	_, err = awaitKeychain(ctx, "read", func() (string, error) {
		return store.readPinned(probe)
	})
	if !errors.Is(err, ErrNotFound) {
		if err == nil {
			return nil, errors.New("keychain probe deletion could not be verified")
		}
		return nil, fmt.Errorf("keychain probe verify: %w", err)
	}
	return store, nil
}

func resolveCollection(svc *ss.SecretService) (pin, persistent string, err error) {
	obj := svc.GetLoginCollection()
	path := string(obj.Path())
	if path != ssAliasDefault {
		if err := svc.CheckCollectionPath(obj.Path()); err != nil {
			return "", "", err
		}
		return path, path, nil
	}
	var resolved dbus.ObjectPath
	callErr := svc.Object(ssServiceName, dbus.ObjectPath(ssServicePath)).Call(ssReadAlias, 0, "default").Store(&resolved)
	if callErr != nil || resolved == "" || resolved == "/" || string(resolved) == ssAliasDefault {
		return path, "", nil
	}
	if err := svc.CheckCollectionPath(resolved); err != nil {
		return path, "", nil
	}
	return string(resolved), string(resolved), nil
}

func (s *KeychainStore) Get(ctx context.Context, name string) ([]byte, error) {
	encoded, err := awaitKeychain(ctx, "read", func() (string, error) {
		return s.readPinned(name)
	})
	if errors.Is(err, ErrNotFound) {
		return nil, s.miss()
	}
	if err != nil {
		return nil, err
	}
	return decodeKeychainValue(encoded)
}

func (s *KeychainStore) Put(ctx context.Context, name string, value []byte) error {
	return s.setValue(ctx, name, base64.StdEncoding.EncodeToString(value))
}

func (s *KeychainStore) Delete(ctx context.Context, name string) error {
	_, err := awaitKeychain(ctx, "delete", func() (string, error) {
		return "", s.deletePinned(name)
	})
	if errors.Is(err, ErrNotFound) {
		return s.miss()
	}
	return err
}

func (s *KeychainStore) miss() error {
	if s.origin.Persistent == "" {
		return ErrAuthorityUnreachable
	}
	return ErrNotFound
}

func (s *KeychainStore) setValue(ctx context.Context, name, value string) error {
	if _, err := awaitKeychain(ctx, "write", func() (string, error) {
		return "", s.writePinned(name, value)
	}); err != nil {
		return err
	}
	stored, err := awaitKeychain(ctx, "read", func() (string, error) {
		return s.readPinned(name)
	})
	if err != nil {
		return readBackError(err)
	}
	if stored != value {
		return errors.New("keychain write read-back did not match")
	}
	return nil
}

func (s *KeychainStore) collection() (dbus.ObjectPath, error) {
	if s.pin == "" {
		return "", ErrAuthorityUnreachable
	}
	path := dbus.ObjectPath(s.pin)
	if s.origin.Persistent == "" {
		return path, nil
	}
	svc, err := ss.NewSecretService()
	if err != nil {
		return "", ErrAuthorityUnreachable
	}
	if err := svc.CheckCollectionPath(path); err != nil {
		return "", ErrAuthorityUnreachable
	}
	return path, nil
}

func (s *KeychainStore) writePinned(name, value string) error {
	path, err := s.collection()
	if err != nil {
		return errors.New("keychain write failed")
	}
	svc, err := ss.NewSecretService()
	if err != nil {
		return errors.New("keychain write failed")
	}
	session, err := svc.OpenSession()
	if err != nil {
		return errors.New("keychain write failed")
	}
	defer svc.Close(session)
	if err := svc.Unlock(path); err != nil {
		return errors.New("keychain write failed")
	}
	secret := ss.NewSecret(session.Path(), value)
	if err := svc.CreateItem(svc.Object(ssServiceName, path),
		fmt.Sprintf("Password for '%s' on '%s'", name, service),
		map[string]string{"username": name, "service": service}, secret); err != nil {
		return errors.New("keychain write failed")
	}
	return nil
}

func (s *KeychainStore) readPinned(name string) (string, error) {
	path, err := s.collection()
	if err != nil {
		return "", err
	}
	svc, err := ss.NewSecretService()
	if err != nil {
		return "", err
	}
	session, err := svc.OpenSession()
	if err != nil {
		return "", err
	}
	defer svc.Close(session)
	if err := svc.Unlock(path); err != nil {
		return "", err
	}
	results, err := svc.SearchItems(svc.Object(ssServiceName, path), map[string]string{
		"username": name,
		"service":  service,
	})
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", ErrNotFound
	}
	if err := svc.Unlock(results[0]); err != nil {
		return "", err
	}
	secret, err := svc.GetSecret(results[0], session.Path())
	if err != nil {
		return "", err
	}
	return string(secret.Value), nil
}

func (s *KeychainStore) deletePinned(name string) error {
	path, err := s.collection()
	if err != nil {
		return err
	}
	svc, err := ss.NewSecretService()
	if err != nil {
		return err
	}
	if err := svc.Unlock(path); err != nil {
		return err
	}
	results, err := svc.SearchItems(svc.Object(ssServiceName, path), map[string]string{
		"username": name,
		"service":  service,
	})
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return ErrNotFound
	}
	return svc.Delete(results[0])
}
