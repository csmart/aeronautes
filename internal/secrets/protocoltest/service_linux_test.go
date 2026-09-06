package protocoltest

import (
	"errors"
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
	ss "github.com/zalando/go-keyring/secret_service"
)

const (
	rootPath     = dbus.ObjectPath("/org/freedesktop/secrets")
	loginPath    = rootPath + "/collection/login"
	sessionPath  = rootPath + "/session/test"
	serviceIF    = "org.freedesktop.Secret.Service"
	collectionIF = "org.freedesktop.Secret.Collection"
	itemIF       = "org.freedesktop.Secret.Item"
	sessionIF    = "org.freedesktop.Secret.Session"
)

type secretService struct {
	mu    sync.Mutex
	conn  *dbus.Conn
	next  int
	items map[dbus.ObjectPath]*secretItem
}

type collection struct{ service *secretService }
type session struct{}
type secretItem struct {
	service    *secretService
	path       dbus.ObjectPath
	attributes map[string]string
	value      string
}

func newSecretService(address string) (*secretService, error) {
	conn, err := dbus.Connect(address)
	if err != nil {
		return nil, err
	}
	s := &secretService{conn: conn, items: map[dbus.ObjectPath]*secretItem{}}
	fail := func(err error) (*secretService, error) {
		conn.Close()
		return nil, err
	}
	for _, export := range []struct {
		value any
		path  dbus.ObjectPath
		iface string
	}{
		{s, rootPath, serviceIF},
		{&collection{s}, loginPath, collectionIF},
		{&session{}, sessionPath, sessionIF},
	} {
		if err := conn.Export(export.value, export.path, export.iface); err != nil {
			return fail(err)
		}
	}
	if _, err := prop.Export(conn, rootPath, prop.Map{
		serviceIF: {"Collections": {Value: []dbus.ObjectPath{loginPath}}},
	}); err != nil {
		return fail(err)
	}
	reply, err := conn.RequestName("org.freedesktop.secrets", dbus.NameFlagDoNotQueue)
	if err != nil {
		return fail(err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fail(errors.New("fixture does not own Secret Service"))
	}
	return s, nil
}

func (*secretService) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	if algorithm != "plain" {
		return dbus.MakeVariant(""), "/", dbus.MakeFailedError(errors.New("unsupported fixture algorithm"))
	}
	return dbus.MakeVariant(""), sessionPath, nil
}

func (*secretService) Unlock(paths []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	return paths, "/", nil
}

func (*session) Close() *dbus.Error { return nil }

func (c *collection) CreateItem(properties map[string]dbus.Variant, secret ss.Secret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	s := c.service
	s.mu.Lock()
	defer s.mu.Unlock()
	attributes, ok := properties[itemIF+".Attributes"].Value().(map[string]string)
	if !ok || secret.Session != sessionPath {
		return "/", "/", dbus.MakeFailedError(errors.New("invalid fixture create request"))
	}
	if replace {
		for _, item := range s.items {
			if sameAttributes(item.attributes, attributes) {
				item.value = string(secret.Value)
				return item.path, "/", nil
			}
		}
	}
	s.next++
	path := dbus.ObjectPath(fmt.Sprintf("%s/i%d", loginPath, s.next))
	item := &secretItem{s, path, attributes, string(secret.Value)}
	if err := s.conn.Export(item, path, itemIF); err != nil {
		return "/", "/", dbus.MakeFailedError(err)
	}
	s.items[path] = item
	return path, "/", nil
}

func sameAttributes(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func (c *collection) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, *dbus.Error) {
	s := c.service
	s.mu.Lock()
	defer s.mu.Unlock()
	paths := []dbus.ObjectPath{}
	for path, item := range s.items {
		if sameAttributes(item.attributes, attributes) {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func (i *secretItem) GetSecret(session dbus.ObjectPath) (ss.Secret, *dbus.Error) {
	i.service.mu.Lock()
	defer i.service.mu.Unlock()
	if i.service.items[i.path] != i || session != sessionPath {
		return ss.Secret{}, dbus.MakeFailedError(errors.New("unknown fixture item or session"))
	}
	return ss.NewSecret(session, i.value), nil
}

func (i *secretItem) Delete() (dbus.ObjectPath, *dbus.Error) {
	i.service.mu.Lock()
	defer i.service.mu.Unlock()
	delete(i.service.items, i.path)
	return "/", nil
}
