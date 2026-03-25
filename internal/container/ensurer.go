package container

import (
	"context"
	"fmt"
	"sync"
)

// channelLabelKey is the Docker label key used to associate containers with channels.
const channelLabelKey = "loop-channel"

// containerLister abstracts the container listing needed by ChannelContainerEnsurer.
type containerLister interface {
	ContainerList(ctx context.Context, labelKey, labelValue string) ([]string, error)
}

// shellContainerCreator creates shell containers on-demand for terminal access.
type shellContainerCreator interface {
	CreateShellContainer(ctx context.Context, channelID, dirPath string) (string, error)
}

// ChannelContainerEnsurer finds running containers by channel ID label,
// creating a new shell container if none exists.
type ChannelContainerEnsurer struct {
	client  containerLister
	creator shellContainerCreator
	mu      sync.Mutex             // serializes container creation per call
	pending map[string]*sync.Mutex // per-channel mutex to prevent duplicate containers
}

// NewChannelContainerEnsurer creates a new ensurer.
func NewChannelContainerEnsurer(client containerLister, creator shellContainerCreator) *ChannelContainerEnsurer {
	return &ChannelContainerEnsurer{
		client:  client,
		creator: creator,
		pending: make(map[string]*sync.Mutex),
	}
}

// FindContainerByChannel returns the ID of a running container for the channel.
// If no container exists, a new shell container is created automatically.
// Uses a per-channel mutex to prevent duplicate containers when multiple
// terminal panes connect simultaneously (e.g. Swarm layout).
func (e *ChannelContainerEnsurer) FindContainerByChannel(ctx context.Context, channelID, dirPath string) (string, error) {
	// Get or create a per-channel mutex.
	e.mu.Lock()
	chMu, ok := e.pending[channelID]
	if !ok {
		chMu = &sync.Mutex{}
		e.pending[channelID] = chMu
	}
	e.mu.Unlock()

	// Serialize container lookup + creation for this channel.
	chMu.Lock()
	defer chMu.Unlock()

	ids, err := e.client.ContainerList(ctx, channelLabelKey, channelID)
	if err != nil {
		return "", fmt.Errorf("listing containers: %w", err)
	}
	if len(ids) > 0 {
		return ids[0], nil
	}

	return e.creator.CreateShellContainer(ctx, channelID, dirPath)
}
