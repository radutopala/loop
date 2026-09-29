package container

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ImageGate is the part of ImageLifecycleManager the runner uses to hold
// container creation while a build of an image it depends on runs.
type ImageGate interface {
	WaitBuilds(ctx context.Context, images []string, onWait func(image string)) error
}

// SetImageGate makes container creation wait out builds of its image and of
// baseImage, the agent image project images are built FROM, and refuse
// images built by an older loop than loopVersion, the daemon's own.
func (r *DockerRunner) SetImageGate(gate ImageGate, loopVersion, baseImage string) {
	r.imageGate = gate
	r.loopVersion = loopVersion
	r.baseImage = baseImage
}

// awaitImage waits for a build of image, or of the base agent image, in
// progress; a rebuilt base is followed by rebuilds of the project images on
// it, which the lifecycle manager marks before the base build ends, so a
// project image is never used between the two. Builds of other images (another
// project's, the Chrome sidecar's) don't hold it. It then checks that image
// wasn't built by an older loop than the daemon. The daemon hands the
// container policy the image's own loop binary enforces (the docker proxy's,
// for one), so an older image can fail to start on a policy it can't read;
// the check turns that into an error that says what to do. Project images
// FROM the agent image inherit its loop.version label, so they're checked
// too. Images without the label, and non-release versions, aren't compared.
func (r *DockerRunner) awaitImage(ctx context.Context, image string, onActivity func(activity, detail string)) error {
	if r.imageGate == nil {
		return nil
	}
	if onActivity == nil {
		onActivity = func(string, string) {}
	}
	waited := false
	err := r.imageGate.WaitBuilds(ctx, []string{image, r.baseImage}, func(building string) {
		waited = true
		onActivity("image_build", fmt.Sprintf("Waiting for the %s image build to finish", building))
	})
	if err != nil {
		return fmt.Errorf("waiting for the image build: %w", err)
	}
	if waited {
		// Replaces the waiting notice, which would otherwise stay up until
		// the agent's first reply; the chat shows nothing for this one.
		onActivity("image_ready", "")
	}
	labels, err := r.client.ImageInspectLabels(ctx, image)
	if err != nil {
		return nil // a missing image fails the create with its own error
	}
	if built := labels["loop.version"]; versionOlder(built, r.loopVersion) {
		return fmt.Errorf("image %s is out of date: it was built by loop %s, but loop %s is running; its rebuild failed or hasn't run, so rebuild the image and try again", image, built, r.loopVersion)
	}
	return nil
}

// versionOlder reports whether release version a (YEAR.MONTH.COUNTER, with
// an optional "v") is older than b. Anything else compares as not older.
func versionOlder(a, b string) bool {
	pa, okA := parseRelease(a)
	pb, okB := parseRelease(b)
	if !okA || !okB {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

// parseRelease splits a YEAR.MONTH.COUNTER release version into numbers.
func parseRelease(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != len(out) {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
