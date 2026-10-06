package container

import (
	"archive/tar"
	"bytes"
	"context"
	"path"
	"strconv"
	"strings"

	"github.com/radutopala/loop/internal/httpapprover"
)

// APITokenFile is where an agent container finds its API token: owned by the
// agent user, mode 0400.
const APITokenFile = "/run/loop/api-token"

// TokenIssuer mints the per-container API token the agent authenticates to
// loop-server with. Issue runs once per spawn; Revoke runs when the container
// is removed, so a token never outlives its container.
type TokenIssuer interface {
	Issue(containerID, channelID, dirPath string) (string, error)
	Revoke(containerID string)
}

// SetTokenIssuer wires the API token issuer. Nil means no API token is minted.
func (r *DockerRunner) SetTokenIssuer(issuer TokenIssuer) {
	r.tokenIssuer = issuer
}

// runToken is one file writeRunTokens places under /run/loop.
type runToken struct {
	path     string
	value    string
	uid, gid int
}

// writeRunTokens copies the container's tokens in as files rather than env
// vars, which the agent process and `docker inspect` would both see. The gate
// token is root's (the in-container dockerproxy and syscallwrap parent read
// it); the API token is the agent's, owned by the IDs the entrypoint gives
// the agent user (see agentIDs). Empty values are skipped.
func (r *DockerRunner) writeRunTokens(ctx context.Context, containerID string, env []string, gateToken, apiToken string) error {
	uid, gid := r.agentIDs(env)
	tokens := []runToken{
		{path: httpapprover.GateTokenFile, value: gateToken},
		{path: APITokenFile, value: apiToken, uid: uid, gid: gid},
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dirs := map[string]bool{}
	wrote := false
	for _, t := range tokens {
		if t.value == "" {
			continue
		}
		dir := path.Dir(t.path)
		for _, d := range append(ancestorDirs(dir), dir[1:]) {
			if dirs[d] {
				continue
			}
			dirs[d] = true
			_ = tw.WriteHeader(&tar.Header{Name: d + "/", Typeflag: tar.TypeDir, Mode: 0o755})
		}
		_ = tw.WriteHeader(&tar.Header{
			Name: t.path[1:],
			Mode: 0o400,
			Size: int64(len(t.value)),
			Uid:  t.uid,
			Gid:  t.gid,
		})
		_, _ = tw.Write([]byte(t.value))
		wrote = true
	}
	if !wrote {
		return nil
	}
	_ = tw.Close()
	return r.client.CopyToContainer(ctx, containerID, "/", &buf)
}

// agentIDs returns the uid and gid the entrypoint creates the agent user
// with: the LOOP_HOST_UID/LOOP_HOST_GID the container starts with. Config envs are
// appended after the daemon's own values and Docker keeps the last of a
// duplicated key, so the last numeric value wins here too. A missing or
// non-numeric value falls back to the daemon's own ID.
func (r *DockerRunner) agentIDs(env []string) (uid, gid int) {
	uid, gid = r.sys.Getuid(), r.sys.Getgid()
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			continue
		}
		switch key {
		case "LOOP_HOST_UID":
			uid = n
		case "LOOP_HOST_GID":
			gid = n
		}
	}
	return uid, gid
}
