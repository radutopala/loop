package dockerproxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type DetailsSuite struct {
	suite.Suite
}

func TestDetailsSuite(t *testing.T) {
	suite.Run(t, new(DetailsSuite))
}

// decode parses raw JSON to the same shape evaluateBody hands to the
// extractor (i.e. encoding/json default of map[string]any / []any).
func (s *DetailsSuite) decode(raw string) any {
	var v any
	require.NoError(s.T(), json.Unmarshal([]byte(raw), &v))
	return v
}

func (s *DetailsSuite) TestNonObjectBodyReturnsNil() {
	require.Nil(s.T(), extractApprovalDetails("POST", "/containers/create", nil))
	require.Nil(s.T(), extractApprovalDetails("POST", "/containers/create", "string-body"))
	require.Nil(s.T(), extractApprovalDetails("POST", "/containers/create", []any{1, 2}))
}

func (s *DetailsSuite) TestUnknownPathReturnsNil() {
	body := s.decode(`{"Image":"alpine"}`)
	require.Nil(s.T(), extractApprovalDetails("POST", "/something/else", body))
	require.Nil(s.T(), extractApprovalDetails("GET", "/containers/create", body))
}

func (s *DetailsSuite) TestContainerCreateExtractsAllFields() {
	body := s.decode(`{
		"Image": "alpine:latest",
		"Cmd": ["sh","-c","echo hi"],
		"Entrypoint": ["/bin/sh"],
		"User": "1000:1000",
		"WorkingDir": "/app",
		"HostConfig": {
			"Binds": ["/host/etc:/etc:ro","/var/run/docker.sock:/var/run/docker.sock"],
			"Privileged": true,
			"NetworkMode": "host",
			"PidMode": "host",
			"IpcMode": "host",
			"UsernsMode": "host",
			"CapAdd": ["SYS_ADMIN"],
			"Devices": [{"PathOnHost":"/dev/sda"}],
			"SecurityOpt": ["seccomp=unconfined"]
		}
	}`)
	d := extractApprovalDetails("POST", "/containers/create", body)
	require.Equal(s.T(), "alpine:latest", d["image"])
	require.Equal(s.T(), "sh, -c, echo hi", d["cmd"])
	require.Equal(s.T(), "/bin/sh", d["entrypoint"])
	require.Equal(s.T(), "1000:1000", d["user"])
	require.Equal(s.T(), "/app", d["working_dir"])
	require.Contains(s.T(), d["binds"], "/host/etc:/etc:ro")
	require.Equal(s.T(), "true", d["privileged"])
	require.Equal(s.T(), "host", d["network_mode"])
	require.Equal(s.T(), "host", d["pid_mode"])
	require.Equal(s.T(), "host", d["ipc_mode"])
	require.Equal(s.T(), "host", d["userns_mode"])
	require.Equal(s.T(), "SYS_ADMIN", d["cap_add"])
	require.Equal(s.T(), "seccomp=unconfined", d["security_opt"])
}

func (s *DetailsSuite) TestContainerCreateOmitsAbsentFields() {
	body := s.decode(`{"Image":"alpine"}`)
	d := extractApprovalDetails("POST", "/containers/create", body)
	require.Equal(s.T(), map[string]string{"image": "alpine"}, d)
}

func (s *DetailsSuite) TestContainerCreateOmitsDefaultNetworkMode() {
	body := s.decode(`{"Image":"alpine","HostConfig":{"NetworkMode":"default"}}`)
	d := extractApprovalDetails("POST", "/containers/create", body)
	_, hasNet := d["network_mode"]
	require.False(s.T(), hasNet)
}

func (s *DetailsSuite) TestContainerCreatePrivilegedFalseOmitted() {
	body := s.decode(`{"Image":"alpine","HostConfig":{"Privileged":false}}`)
	d := extractApprovalDetails("POST", "/containers/create", body)
	_, has := d["privileged"]
	require.False(s.T(), has)
}

func (s *DetailsSuite) TestContainerCreateEmptyReturnsNil() {
	body := s.decode(`{}`)
	require.Nil(s.T(), extractApprovalDetails("POST", "/containers/create", body))
}

func (s *DetailsSuite) TestExecCreateExtracts() {
	body := s.decode(`{"Cmd":["bash","-c","whoami"],"User":"root","Privileged":true,"AttachStdin":true,"Tty":true}`)
	d := extractApprovalDetails("POST", "/containers/abc123def456/exec", body)
	require.Equal(s.T(), "bash, -c, whoami", d["cmd"])
	require.Equal(s.T(), "root", d["user"])
	require.Equal(s.T(), "true", d["privileged"])
	require.Equal(s.T(), "true", d["attach_stdin"])
	require.Equal(s.T(), "true", d["tty"])
}

func (s *DetailsSuite) TestExecCreateEmptyReturnsNil() {
	body := s.decode(`{}`)
	require.Nil(s.T(), extractApprovalDetails("POST", "/containers/abc/exec", body))
}

func (s *DetailsSuite) TestNetworkCreate() {
	body := s.decode(`{"Name":"net1","Driver":"bridge","Internal":true,"Attachable":true}`)
	d := extractApprovalDetails("POST", "/networks/create", body)
	require.Equal(s.T(), "net1", d["name"])
	require.Equal(s.T(), "bridge", d["driver"])
	require.Equal(s.T(), "true", d["internal"])
	require.Equal(s.T(), "true", d["attachable"])
}

func (s *DetailsSuite) TestVolumeCreate() {
	body := s.decode(`{"Name":"vol1","Driver":"local"}`)
	d := extractApprovalDetails("POST", "/volumes/create", body)
	require.Equal(s.T(), "vol1", d["name"])
	require.Equal(s.T(), "local", d["driver"])
}

func (s *DetailsSuite) TestImagesCreateReturnsNil() {
	// /images/create carries pull info on the query string, not the body.
	body := s.decode(`{}`)
	require.Nil(s.T(), extractApprovalDetails("POST", "/images/create", body))
}

func (s *DetailsSuite) TestTruncateLongImage() {
	body := map[string]any{"Image": strings.Repeat("a", 300)}
	d := extractApprovalDetails("POST", "/containers/create", body)
	require.LessOrEqual(s.T(), len([]rune(d["image"])), 201)
	require.True(s.T(), strings.HasSuffix(d["image"], "…"))
}

func (s *DetailsSuite) TestDetailsKeysSortedDeterministic() {
	got := detailsKeysSorted(map[string]string{"b": "1", "a": "2", "c": "3"})
	require.Equal(s.T(), []string{"a", "b", "c"}, got)
}

func (s *DetailsSuite) TestNetworkCreateEmptyReturnsNil() {
	body := s.decode(`{}`)
	require.Nil(s.T(), extractApprovalDetails("POST", "/networks/create", body))
}

func (s *DetailsSuite) TestVolumeCreateEmptyReturnsNil() {
	body := s.decode(`{}`)
	require.Nil(s.T(), extractApprovalDetails("POST", "/volumes/create", body))
}

func (s *DetailsSuite) TestTruncateMultibyteShorterThanByteLen() {
	// "é" is 2 bytes but 1 rune. The string fits comfortably below max in
	// rune-count even though byte-len exceeds max, so truncate must NOT clip.
	got := truncate(strings.Repeat("é", 10), 15) // 20 bytes, 10 runes, max 15
	require.Equal(s.T(), strings.Repeat("é", 10), got)
}
