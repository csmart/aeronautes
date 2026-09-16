package protocoltest

import (
	"errors"
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
	ss "github.com/zalando/go-keyring/secret_service"
)

const (
	rootPath     = dbus.ObjectPath("/org/freedesktop/secrets")
	loginPath    = rootPath + "/collection/login"
	extraPath    = rootPath + "/collection/extra"
	aliasPath    = dbus.ObjectPath("/org/freedesktop/secrets/aliases/default")
	sessionPath  = rootPath + "/session/test"
	serviceIF    = "org.freedesktop.Secret.Service"
	collectionIF = "org.freedesktop.Secret.Collection"
	itemIF       = "org.freedesktop.Secret.Item"
	sessionIF    = "org.freedesktop.Secret.Session"
	promptIF     = "org.freedesktop.Secret.Prompt"
)

type secretService struct {
	mu          sync.Mutex
	conn        *dbus.Conn
	listed      []dbus.ObjectPath
	next        int
	items       map[dbus.ObjectPath]*secretItem
	aliases     map[string]dbus.ObjectPath
	failure     string
	faults      int
	lastWrite   string
	deleted     bool
	deleteCalls []string
	readGate    *readBarrier
}

type readBarrier struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type collection struct {
	service *secretService
	path    dbus.ObjectPath
}
type session struct{}
type prompt struct {
	conn   *dbus.Conn
	path   dbus.ObjectPath
	result dbus.Variant
}
type secretItem struct {
	service    *secretService
	path       dbus.ObjectPath
	collection dbus.ObjectPath
	attributes map[string]string
	value      string
}

func newSecretService(address string) (*secretService, error) {
	conn, err := dbus.Connect(address)
	if err != nil {
		return nil, err
	}
	s := &secretService{
		conn:    conn,
		listed:  []dbus.ObjectPath{loginPath},
		items:   map[dbus.ObjectPath]*secretItem{},
		aliases: map[string]dbus.ObjectPath{"default": loginPath},
	}
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
		{s, rootPath, "org.freedesktop.DBus.Properties"},
		{&collection{s, loginPath}, loginPath, collectionIF},
		{&collection{s, extraPath}, extraPath, collectionIF},
		{&collection{s, aliasPath}, aliasPath, collectionIF},
		{&session{}, sessionPath, sessionIF},
	} {
		if err := conn.Export(export.value, export.path, export.iface); err != nil {
			return fail(err)
		}
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

func (s *secretService) Unlock(paths []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure == "dismiss-read-unlock" && s.lastWrite != "" {
		path, err := s.dismissedPrompt(dbus.MakeVariant([]dbus.ObjectPath{}))
		return nil, path, err
	}
	return paths, "/", nil
}

func (*session) Close() *dbus.Error { return nil }

func (s *secretService) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if iface == serviceIF && property == "Collections" {
		return dbus.MakeVariant(append([]dbus.ObjectPath(nil), s.listed...)), nil
	}
	return dbus.MakeVariant(""), dbus.MakeFailedError(errors.New("unknown fixture property"))
}

func (s *secretService) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if iface != serviceIF {
		return nil, dbus.MakeFailedError(errors.New("unknown fixture interface"))
	}
	return map[string]dbus.Variant{
		"Collections": dbus.MakeVariant(append([]dbus.ObjectPath(nil), s.listed...)),
	}, nil
}

func (*secretService) Set(iface, property string, value dbus.Variant) *dbus.Error {
	return dbus.MakeFailedError(errors.New("fixture properties are read-only"))
}

func (c *collection) CreateItem(properties map[string]dbus.Variant, secret ss.Secret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	s := c.service
	s.mu.Lock()
	defer s.mu.Unlock()
	attributes, ok := properties[itemIF+".Attributes"].Value().(map[string]string)
	if !ok || secret.Session != sessionPath {
		return "/", "/", dbus.MakeFailedError(errors.New("invalid fixture create request"))
	}
	s.lastWrite = attributes["username"]
	target := c.targetLocked()
	if s.failure == "dismiss-set" {
		path, err := s.dismissedPrompt(dbus.MakeVariant(dbus.ObjectPath("/")))
		return "/", path, err
	}
	if s.failure == "set-error" {
		s.faults++
		return "/", "/", dbus.MakeFailedError(fmt.Errorf("injected write failure for %s containing %s", attributes["username"], secret.Value))
	}
	if replace {
		for _, item := range s.items {
			if item.collection == target && sameAttributes(item.attributes, attributes) {
				item.value = string(secret.Value)
				return item.path, "/", nil
			}
		}
	}
	s.next++
	path := dbus.ObjectPath(fmt.Sprintf("%s/i%d", target, s.next))
	item := &secretItem{s, path, target, attributes, string(secret.Value)}
	if err := s.conn.Export(item, path, itemIF); err != nil {
		return "/", "/", dbus.MakeFailedError(err)
	}
	s.items[path] = item
	if s.failure == "set-error-after" {
		s.faults++
		return "/", "/", dbus.MakeFailedError(fmt.Errorf("injected failure after writing %s containing %s", attributes["username"], secret.Value))
	}
	return path, "/", nil
}

func (s *secretService) dismissedPrompt(result dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	s.faults++
	s.next++
	path := dbus.ObjectPath(fmt.Sprintf("%s/prompt/p%d", rootPath, s.next))
	if err := s.conn.Export(&prompt{s.conn, path, result}, path, promptIF); err != nil {
		return "/", dbus.MakeFailedError(err)
	}
	return path, nil
}

func (p *prompt) Prompt(window string) *dbus.Error {
	if err := p.conn.Emit(p.path, promptIF+".Completed", true, p.result); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

func (s *secretService) failWith(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failure, s.faults, s.lastWrite, s.deleted = mode, 0, "", false
	s.deleteCalls = nil
	s.readGate = nil
}

func (s *secretService) blockReadBack() *readBarrier {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastWrite = ""
	s.readGate = &readBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	return s.readGate
}

func (s *secretService) observations() (int, string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.faults, s.lastWrite, append([]string(nil), s.deleteCalls...)
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
	if attributes["username"] == s.lastWrite {
		if s.failure == "read-missing" {
			s.faults++
			return nil, nil
		}
		if s.failure == "read-after-delete-error" && s.deleted {
			s.faults++
			return nil, dbus.MakeFailedError(errors.New("injected post-delete lookup failure"))
		}
	}
	target := c.targetLocked()
	paths := []dbus.ObjectPath{}
	for path, item := range s.items {
		if item.collection == target && sameAttributes(item.attributes, attributes) {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func (c *collection) targetLocked() dbus.ObjectPath {
	if c.path == aliasPath {
		if aliased := c.service.aliases["default"]; aliased != "" {
			return aliased
		}
	}
	return c.path
}

func (s *secretService) ReadAlias(name string) (dbus.ObjectPath, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure == "read-alias-error" {
		s.faults++
		return "/", dbus.MakeFailedError(errors.New("injected alias read failure"))
	}
	path := s.aliases[name]
	if path == "" {
		return "/", nil
	}
	return path, nil
}

func (s *secretService) SetAlias(name string, collection dbus.ObjectPath) *dbus.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aliases[name] = collection
	return nil
}

func (s *secretService) setCollections(paths []dbus.ObjectPath) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listed = append([]dbus.ObjectPath(nil), paths...)
}

func (s *secretService) restoreDefaults() {
	s.failWith("")
	s.setCollections([]dbus.ObjectPath{loginPath})
	_ = s.SetAlias("default", loginPath)
}

func (s *secretService) valueIn(collection dbus.ObjectPath, name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if item.collection == collection && item.attributes["username"] == name {
			return item.value, true
		}
	}
	return "", false
}

func (i *secretItem) GetSecret(session dbus.ObjectPath) (ss.Secret, *dbus.Error) {
	i.service.mu.Lock()
	value, err := i.getSecretLocked(session)
	var gate *readBarrier
	if err == nil && i.attributes["username"] == i.service.lastWrite {
		gate = i.service.readGate
	}
	i.service.mu.Unlock()
	// Other keychain requests must progress while this one waits, just as
	// bookmark access must progress while a native prompt is outstanding.
	if gate != nil {
		gate.once.Do(func() { close(gate.entered) })
		<-gate.release
	}
	return value, err
}

func (i *secretItem) getSecretLocked(session dbus.ObjectPath) (ss.Secret, *dbus.Error) {
	if i.service.items[i.path] != i || session != sessionPath {
		return ss.Secret{}, dbus.MakeFailedError(errors.New("unknown fixture item or session"))
	}
	if i.attributes["username"] == i.service.lastWrite {
		switch i.service.failure {
		case "read-error":
			i.service.faults++
			return ss.Secret{}, dbus.MakeFailedError(fmt.Errorf("injected read failure for %s containing %s", i.attributes["username"], i.value))
		case "read-different":
			i.service.faults++
			return ss.NewSecret(session, "ZGlmZmVyZW50LXZhbHVl"), nil
		case "read-noncanonical":
			i.service.faults++
			return ss.NewSecret(session, i.value+"\n"), nil
		}
	}
	return ss.NewSecret(session, i.value), nil
}

func (i *secretItem) Delete() (dbus.ObjectPath, *dbus.Error) {
	i.service.mu.Lock()
	defer i.service.mu.Unlock()
	i.service.deleteCalls = append(i.service.deleteCalls, i.attributes["username"])
	if i.service.failure == "dismiss-delete" {
		return i.service.dismissedPrompt(dbus.MakeVariant(dbus.ObjectPath("/")))
	}
	i.service.deleted = true
	delete(i.service.items, i.path)
	return "/", nil
}
