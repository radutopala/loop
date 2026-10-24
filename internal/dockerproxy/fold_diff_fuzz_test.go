package dockerproxy

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

// foldProbes are fields the body rules read, with where the daemon's
// CreateRequest keeps them.
var foldProbes = []struct {
	path  []string
	field func(container.CreateRequest) any
}{
	{[]string{"HostConfig", "Binds"}, func(c container.CreateRequest) any { return c.HostConfig.Binds }},
	{[]string{"HostConfig", "Privileged"}, func(c container.CreateRequest) any { return c.HostConfig.Privileged }},
	{[]string{"HostConfig", "CapAdd"}, func(c container.CreateRequest) any { return []string(c.HostConfig.CapAdd) }},
	{[]string{"Image"}, func(c container.CreateRequest) any { return c.Image }},
}

// FuzzDecodeBodyMatchesDaemon checks the proxy against the daemon's own
// decoding of a container create body: whenever decodeBody accepts a body,
// each field the rules read (looked up case-insensitively, as walkPath does)
// holds the value encoding/json puts in the daemon's CreateRequest. A body
// where the two differ would let a rule judge one value while the daemon
// acts on another.
func FuzzDecodeBodyMatchesDaemon(f *testing.F) {
	for _, seed := range []string{
		`{"Image":"a","HostConfig":{"Binds":["/w:/w"],"Privileged":false}}`,
		`{"image":"a","hostconfig":{"binds":["/w:/w"],"privileged":true}}`,
		`{"HostConfig":{"Binds":["/w:/w"],"BINDS":["/:/h"]}}`,
		`{"HostConfig":{"Binds":["/w:/w"],"Bindſ":["/:/h"]}}`,
		`{"HoſtConfig":{"Privileged":true},"HostConfig":{"Privileged":false}}`,
		`{"HostConfig":{"CapAdd":["NET_ADMIN"],"CAPADD":[]}}`,
	} {
		f.Add([]byte(seed))
	}
	names := map[string]bool{}
	for _, p := range foldProbes {
		addFoldNames(names, p.path...)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		decoded, err := decodeBody(body, names)
		if err != nil {
			return
		}
		var cr container.CreateRequest
		if json.Unmarshal(body, &cr) != nil {
			return // the daemon refuses it too
		}
		if cr.Config == nil {
			cr.Config = &container.Config{}
		}
		if cr.HostConfig == nil {
			cr.HostConfig = &container.HostConfig{}
		}
		for _, p := range foldProbes {
			want := p.field(cr)
			got := reflect.New(reflect.TypeOf(want))
			if v, ok := foldWalk(decoded, p.path); ok && v != nil {
				raw, err := json.Marshal(v)
				require.NoError(t, err)
				if json.Unmarshal(raw, got.Interface()) != nil {
					continue
				}
			}
			require.Equal(t, want, got.Elem().Interface(), "%v in %s", p.path, body)
		}
	})
}

func foldWalk(v any, path []string) (any, bool) {
	for _, name := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = foldGet(m, name); !ok {
			return nil, false
		}
	}
	return v, true
}
