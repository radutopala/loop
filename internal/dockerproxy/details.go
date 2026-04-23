package dockerproxy

import (
	"fmt"
	"sort"
	"strings"
)

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
	host, ok := obj["HostConfig"].(map[string]any)
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
	if len(d) == 0 {
		return nil
	}
	return d
}

// stringField returns the named string field, "" if missing or wrong type.
func stringField(obj map[string]any, key string) string {
	v, _ := obj[key].(string)
	return strings.TrimSpace(v)
}

// boolField returns the named bool field, false on absence/wrong type.
func boolField(obj map[string]any, key string) bool {
	v, _ := obj[key].(bool)
	return v
}

// stringSliceField joins a JSON array of strings (or stringly values) with
// ", "; returns "" if missing or empty.
func stringSliceField(obj map[string]any, key string) string {
	arr, ok := obj[key].([]any)
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
