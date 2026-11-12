// Package journeys holds the real-cloud journey suite (docs/journeys.md).
// The journeys themselves build only with the realcloud tag; the rules in
// this file, which keep a run to what it created, build in the default
// build so their tests run everywhere.
package journeys

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/textproto"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// ContainerPrefix starts every container name a run creates.
const ContainerPrefix = "aeronautes-journey-"

// Owned records what one run created on one account, so that teardown
// removes exactly that and nothing else (docs/journeys.md, Safety).
type Owned struct {
	session *swiftcontrol.Session
	suffix  string
	// containers are the names this run's own create succeeded for; a
	// create that found the name taken is not here.
	containers []string
	// sidecars are segment containers a step of this run may have created:
	// each was read and found absent before that step started.
	sidecars []string
	// metaKey is the account metadata key this run set, "" when none.
	metaKey string
}

// NewOwned starts a record for one run on the account the session reads.
func NewOwned(session *swiftcontrol.Session) (*Owned, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	return &Owned{session: session, suffix: hex.EncodeToString(random)}, nil
}

// CreateContainer creates a container named for this run and records it.
// A name that is already taken is an error and is not recorded, so nothing
// this run does will ever write to or delete it.
func (o *Owned) CreateContainer(ctx context.Context, label string) (string, error) {
	name := ContainerPrefix + label + "-" + o.suffix
	if _, err := o.session.CreateContainer(ctx, swiftcontrol.ContainerCreateRequest{Name: name}); err != nil {
		return "", err
	}
	o.containers = append(o.containers, name)
	return name, nil
}

// ClaimSidecar must be called before a step that can write large-object
// segments for a container this run created. It reads the exact sidecar
// name the engine uses; a sidecar that already exists stops the step, and
// is never deleted, because this run did not create it.
func (o *Owned) ClaimSidecar(ctx context.Context, container string) error {
	if !o.created(container) {
		return fmt.Errorf("container %q: %w", container, ErrNotOwned)
	}
	sidecar := container + "_segments"
	_, err := o.session.ContainerDetail(ctx, sidecar)
	switch {
	case err == nil:
		return fmt.Errorf("the segment container %q already exists, so the step was not started", sidecar)
	case swiftcontrol.IsNotFound(err):
		o.sidecars = append(o.sidecars, sidecar)
		return nil
	default:
		return fmt.Errorf("the segment container could not be checked, so the step was not started: %w", err)
	}
}

// SetAccountKey sets the one account metadata key this run owns, carrying
// every other key and the quota over unchanged, and returns its name. The
// name is kept in the canonical spelling the client sends and the portrait
// returns, so that teardown deletes the key that is actually on the account:
// a suffix starting with a letter would otherwise be upper-cased on the way
// out and missed on the way back.
func (o *Owned) SetAccountKey(ctx context.Context, value string) (string, error) {
	key := textproto.CanonicalMIMEHeaderKey("Aeronautes-Journey-" + o.suffix)
	if err := o.editAccount(ctx, func(meta map[string]string) { meta[key] = value }); err != nil {
		return "", err
	}
	o.metaKey = key
	return key, nil
}

func (o *Owned) editAccount(ctx context.Context, change func(map[string]string)) error {
	portrait, err := o.session.AccountPortrait(ctx)
	if err != nil {
		return err
	}
	meta := make(map[string]string, len(portrait.Meta)+1)
	for k, v := range portrait.Meta {
		meta[k] = v
	}
	change(meta)
	_, err = o.session.UpdateAccount(ctx, "", swiftcontrol.AccountEdit{Meta: meta, QuotaBytes: portrait.QuotaBytes})
	return err
}

func (o *Owned) created(container string) bool {
	for _, name := range o.containers {
		if name == container {
			return true
		}
	}
	return false
}

// Teardown removes what this run created and nothing else: the objects in
// and then each claimed sidecar and each created container, and the account
// key. It goes on past a failure, so one stuck object does not leave the
// rest behind, and returns every failure. A sidecar no step created is
// absent and is skipped.
func (o *Owned) Teardown(ctx context.Context) []error {
	var failures []error
	for _, sidecar := range o.sidecars {
		if _, err := o.session.ContainerDetail(ctx, sidecar); swiftcontrol.IsNotFound(err) {
			continue
		}
		failures = append(failures, o.empty(ctx, sidecar)...)
	}
	for _, container := range o.containers {
		failures = append(failures, o.empty(ctx, container)...)
	}
	if o.metaKey != "" {
		if err := o.editAccount(ctx, func(meta map[string]string) { delete(meta, o.metaKey) }); err != nil {
			failures = append(failures, fmt.Errorf("account key: %w", err))
		}
	}
	return failures
}

func (o *Owned) empty(ctx context.Context, container string) []error {
	var failures []error
	for row, err := range o.session.Objects(ctx, container, swiftcontrol.ObjectListOptions{Deep: true}) {
		if err != nil {
			failures = append(failures, fmt.Errorf("list %s: %w", container, err))
			break
		}
		if object, ok := row.(swiftcontrol.ObjectEntry); ok {
			if err := o.session.DeleteObject(ctx, container, object.Name); err != nil && !swiftcontrol.IsNotFound(err) {
				failures = append(failures, fmt.Errorf("delete in %s: %w", container, err))
			}
		}
	}
	if err := o.session.DeleteContainer(ctx, container); err != nil && !swiftcontrol.IsNotFound(err) {
		failures = append(failures, fmt.Errorf("delete %s: %w", container, err))
	}
	return failures
}

// ErrNotOwned is returned for a container the run did not create.
var ErrNotOwned = errors.New("not created by this run")
