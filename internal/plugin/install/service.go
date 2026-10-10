package install

import (
	"context"
	"errors"
	"fmt"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
)

var (
	// ErrNotInstalled is returned for a plugin that isn't installed.
	ErrNotInstalled = errors.New("plugin is not installed")
	// ErrDigestChanged is returned when the image an install pulls is not
	// the one the owner inspected and approved.
	ErrDigestChanged = errors.New("the image changed since it was inspected; inspect it again")
	// ErrNotApproved is returned when the owner didn't approve every
	// capability a plugin asks for.
	ErrNotApproved = errors.New("capability not approved")
)

// Runtime runs installed plugins' containers.
type Runtime interface {
	StopPlugin(ctx context.Context, pluginID string)
	Remove(ctx context.Context, pluginID, channelID string, purge bool) error
}

// ServiceStore is the store the Service reads plugins and their containers
// from.
type ServiceStore interface {
	Store
	ListPluginStates(ctx context.Context) ([]*db.PluginState, error)
	ListPluginContainers(ctx context.Context, pluginID string) ([]*db.PluginContainer, error)
}

// Service installs, upgrades and removes plugins, keeping their running
// containers in step.
type Service struct {
	installer *Installer
	runtime   Runtime
	store     ServiceStore
}

// NewService returns a Service.
func NewService(installer *Installer, runtime Runtime, store ServiceStore) *Service {
	return &Service{installer: installer, runtime: runtime, store: store}
}

// List returns the installed plugins.
func (s *Service) List(ctx context.Context) ([]*db.PluginState, error) {
	return s.store.ListPluginStates(ctx)
}

// Inspect pulls and validates an image so the owner can review what it
// asks for, installing nothing.
func (s *Service) Inspect(ctx context.Context, ref string, dev bool) (*Candidate, error) {
	c, err := s.installer.Inspect(ctx, ref, dev)
	if err != nil {
		return nil, err
	}
	s.installer.Discard(c)
	return c, nil
}

// Install installs, or upgrades to, the image ref with the capabilities the
// owner approved. A non-empty digest must match what the pull resolves to,
// so the owner gets the image they reviewed. An upgrade stops the plugin's
// containers, which start on the new image when next used.
func (s *Service) Install(ctx context.Context, ref string, dev bool, digest string, approved []plugin.Capability) (*Candidate, error) {
	c, err := s.installer.Inspect(ctx, ref, dev)
	if err != nil {
		return nil, err
	}
	if digest != "" && c.Digest != digest {
		s.installer.Discard(c)
		return nil, ErrDigestChanged
	}
	if err := s.installer.Install(ctx, c, approved); err != nil {
		s.installer.Discard(c)
		return nil, err
	}
	if c.Previous != nil {
		s.runtime.StopPlugin(ctx, c.Manifest.ID)
	}
	return c, nil
}

// Remove removes a plugin's containers in every channel, then the plugin;
// purge also removes its data volumes.
func (s *Service) Remove(ctx context.Context, pluginID string, purge bool) error {
	st, err := s.store.GetPluginState(ctx, pluginID)
	if err != nil {
		return fmt.Errorf("reading the plugin: %w", err)
	}
	if st == nil {
		return ErrNotInstalled
	}
	containers, err := s.store.ListPluginContainers(ctx, pluginID)
	if err != nil {
		return fmt.Errorf("listing the plugin's containers: %w", err)
	}
	var errs []error
	for _, c := range containers {
		errs = append(errs, s.runtime.Remove(ctx, pluginID, c.ChannelID, purge))
	}
	errs = append(errs, s.installer.Remove(ctx, pluginID))
	return errors.Join(errs...)
}
