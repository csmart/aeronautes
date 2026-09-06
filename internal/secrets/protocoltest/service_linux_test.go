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
	promptIF     = "org.freedesktop.Secret.Prompt"
)

type secretService struct {
	mu          sync.Mutex
	conn        *dbus.Conn
	next        int
	items       map[dbus.ObjectPath]*secretItem
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

type collection struct{ service *secretService }
type session struct{}
type prompt struct {
	conn   *dbus.Conn
	path   dbus.ObjectPath
	result dbus.Variant
}
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

func (c *collection) CreateItem(properties map[string]dbus.Variant, secret ss.Secret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	s := c.service
	s.mu.Lock()
	defer s.mu.Unlock()
	attributes, ok := properties[itemIF+".Attributes"].Value().(map[string]string)
	if !ok || secret.Session != sessionPath {
		return "/", "/", dbus.MakeFailedError(errors.New("invalid fixture create request"))
	}
	s.lastWrite = attributes["username"]
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
