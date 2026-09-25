package dockerproxy

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// detailsBodyCap returns a body-decode cap (in bytes) for paths that have a
// details extractor but no body rule of their own. evaluateBody takes the
// max of this and the policy's MaxBodyBytes so the operator gets cmd / user /
// privileged / … on the approval card even on endpoints (exec, network /
// volume create) that aren't gated by JSON checks.
func detailsBodyCap(method, canonicalPath string) int64 {
	switch {
	case method == "POST" && canonicalPath == "/containers/create",
		method == "POST" && execCreateRe.MatchString(canonicalPath),
		method == "POST" && canonicalPath == "/networks/create",
		method == "POST" && canonicalPath == "/volumes/create":
		return 1 << 20 // 1 MiB
	}
	return 0
}

// extractApprovalDetails returns a small key/value summary of the docker
// request body the user is being asked to approve. Returns nil when the
// endpoint isn't recognised or the body has nothing useful to surface — the
// renderer falls back to Target alone in that case.
//
// Only fields meaningful to a humans-in-the-loop decision are included:
// what image, what command, what host paths, what privileged toggle, etc.
// Long values are truncated; lists are joined with ", " and capped.
func extractApprovalDetails(method, canonicalPath string, body any) map[string]string {
	if body == nil {
		return nil
	}
	obj, _ := body.(map[string]any)
	if obj == nil {
		return nil
	}
	switch {
	case method == "POST" && canonicalPath == "/containers/create":
		return detailsForContainerCreate(obj)
	case method == "POST" && execCreateRe.MatchString(canonicalPath):
		return detailsForExecCreate(obj)
	case method == "POST" && canonicalPath == "/networks/create":
		return detailsForNetworkCreate(obj)
	case method == "POST" && canonicalPath == "/volumes/create":
		return detailsForVolumeCreate(obj)
	case method == "POST" && canonicalPath == "/images/create":
		return nil // the relevant info is on the query string, not the body
	}
	return nil
}

// detailsForContainerCreate summarises the most security-relevant fields of
// a `POST /containers/create` request body.
func detailsForContainerCreate(obj map[string]any) map[string]string {
	d := map[string]string{}
	if v := stringField(obj, "Image"); v != "" {
		d["image"] = truncate(v, 200)
	}
	if v := stringSliceField(obj, "Cmd"); v != "" {
		d["cmd"] = truncate(v, 200)
	}
	if v := stringSliceField(obj, "Entrypoint"); v != "" {
		d["entrypoint"] = truncate(v, 200)
	}
	if v := stringField(obj, "User"); v != "" {
		d["user"] = v
	}
	if v := stringField(obj, "WorkingDir"); v != "" {
		d["working_dir"] = truncate(v, 200)
	}
	hv, _ := foldGet(obj, "HostConfig")
	host, ok := hv.(map[string]any)
	if ok {
		if v := stringSliceField(host, "Binds"); v != "" {
			d["binds"] = truncate(v, 400)
		}
		if v := boolField(host, "Privileged"); v {
			d["privileged"] = "true"
		}
		if v := stringField(host, "NetworkMode"); v != "" && v != "default" {
			d["network_mode"] = v
		}
		if v := stringField(host, "PidMode"); v != "" {
			d["pid_mode"] = v
		}
		if v := stringField(host, "IpcMode"); v != "" {
			d["ipc_mode"] = v
		}
		if v := stringField(host, "UsernsMode"); v != "" {
			d["userns_mode"] = v
		}
		if v := stringSliceField(host, "CapAdd"); v != "" {
			d["cap_add"] = truncate(v, 200)
		}
		if v := stringSliceField(host, "Devices"); v != "" {
			d["devices"] = truncate(v, 200)
		}
		if v := stringSliceField(host, "SecurityOpt"); v != "" {
			d["security_opt"] = truncate(v, 200)
		}
		if v := mountsField(host); v != "" {
			d["mounts"] = truncate(v, 400)
		}
	}
	if len(d) == 0 {
		return nil
	}
	return d
}

// detailsForExecCreate summarises a `POST /containers/{id}/exec` body.
func detailsForExecCreate(obj map[string]any) map[string]string {
	d := map[string]string{}
	if v := stringSliceField(obj, "Cmd"); v != "" {
		d["cmd"] = truncate(v, 400)
	}
	if v := stringField(obj, "User"); v != "" {
		d["user"] = v
	}
	if v := boolField(obj, "Privileged"); v {
		d["privileged"] = "true"
	}
	if v := boolField(obj, "AttachStdin"); v {
		d["attach_stdin"] = "true"
	}
	if v := boolField(obj, "Tty"); v {
		d["tty"] = "true"
	}
	if len(d) == 0 {
		return nil
	}
	return d
}

func detailsForNetworkCreate(obj map[string]any) map[string]string {
	d := map[string]string{}
	if v := stringField(obj, "Name"); v != "" {
		d["name"] = v
	}
	if v := stringField(obj, "Driver"); v != "" {
		d["driver"] = v
	}
	if v := boolField(obj, "Internal"); v {
		d["internal"] = "true"
	}
	if v := boolField(obj, "Attachable"); v {
		d["attachable"] = "true"
	}
	if len(d) == 0 {
		return nil
	}
	return d
}

func detailsForVolumeCreate(obj map[string]any) map[string]string {
	d := map[string]string{}
	if v := stringField(obj, "Name"); v != "" {
		d["name"] = v
	}
	if v := stringField(obj, "Driver"); v != "" {
		d["driver"] = v
	}
	if v := mapField(obj, "DriverOpts"); v != "" {
		d["driver_opts"] = truncate(v, 400)
	}
	if len(d) == 0 {
		return nil
	}
	return d
}

// mountsField summarises HostConfig.Mounts as "type source→target", with a
// volume's driver options in brackets; "" when there are none.
func mountsField(host map[string]any) string {
	v, _ := foldGet(host, "Mounts")
	arr, _ := v.([]any)
	parts := make([]string, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		part := fmt.Sprintf("%s %s→%s", foldString(m, "Type"), foldString(m, "Source"), foldString(m, "Target"))
		vo, _ := foldGet(m, "VolumeOptions")
		voMap, _ := vo.(map[string]any)
		dc, _ := foldGet(voMap, "DriverConfig")
		dcMap, _ := dc.(map[string]any)
		if opts := mapField(dcMap, "Options"); opts != "" {
			part += " [" + opts + "]"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

// mapField renders a JSON object field as sorted "k=v" pairs; "" when
// missing or empty.
func mapField(obj map[string]any, key string) string {
	v, _ := foldGet(obj, key)
	m, _ := v.(map[string]any)
	parts := make([]string, 0, len(m))
	for k, val := range m {
		parts = append(parts, fmt.Sprintf("%s=%v", k, val))
	}
	slices.Sort(parts)
	return strings.Join(parts, ", ")
}

// detailsFoldNames are the keys the approval details read. Bodies must not
// hold case variants of them, or the prompt could show a value the daemon
// doesn't use (see fold.go).
var detailsFoldNames = []string{
	"Image", "Cmd", "Entrypoint", "User", "WorkingDir", "HostConfig", "Binds",
	"Privileged", "NetworkMode", "PidMode", "IpcMode", "UsernsMode", "CapAdd",
	"Devices", "SecurityOpt", "AttachStdin", "Tty", "Name", "Driver",
	"Internal", "Attachable", "Mounts", "Type", "Source", "Target",
	"VolumeOptions", "DriverConfig", "Options", "DriverOpts",
}

// stringField returns the named string field (case-insensitive), "" if missing or wrong type.
func stringField(obj map[string]any, key string) string {
	return strings.TrimSpace(foldString(obj, key))
}

// boolField returns the named bool field, false on absence/wrong type.
func boolField(obj map[string]any, key string) bool {
	v, _ := foldGet(obj, key)
	return v == true
}

// stringSliceField joins a JSON array of strings (or stringly values) with
// ", "; returns "" if missing or empty.
func stringSliceField(obj map[string]any, key string) string {
	v, _ := foldGet(obj, key)
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}
	parts := make([]string, 0, len(arr))
	for _, e := range arr {
		switch x := e.(type) {
		case string:
			if x != "" {
				parts = append(parts, x)
			}
		default:
			parts = append(parts, fmt.Sprint(x))
		}
	}
	return strings.Join(parts, ", ")
}

// truncate clips s to max runes with a "…" suffix when it overflows.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// detailsKeysSorted returns the keys of d in deterministic order — handy for
// renderers that need stable layout (Discord embeds, Slack section blocks).
func detailsKeysSorted(d map[string]string) []string {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
