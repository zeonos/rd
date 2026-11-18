// Package policy defines the settings that can be pushed to devices and
// resolves the effective value for a device: account default, then each
// level of its location group ("Office" before "Office/Rack 1").
//
// Delivery uses RustDesk's heartbeat "strategy": the client applies
// config_options and remembers modified_at; we send a new strategy whenever
// the hash of a device's effective options differs from what it reports.
package policy

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
)

type Kind string

const (
	Bool   Kind = "bool"   // "Y" / "N"
	Number Kind = "number" // positive integer
	Text   Kind = "text"   // free text (validated per key)
)

type Setting struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Kind        Kind   `json:"kind"`
	Default     string `json:"default"` // what the client does when unset
	Group       string `json:"group"`
}

// Catalogue lists the RustDesk options we manage. Only these keys can be set.
// Options the RD build overrides (server, WebSocket, approve mode, ...) are
// not here: the client ignores pushed values for them.
var Catalogue = []Setting{
	{Key: "enable-keyboard", Label: "Keyboard and mouse control", Kind: Bool, Default: "Y", Group: "Permissions",
		Description: "Off = view only."},
	{Key: "enable-clipboard", Label: "Clipboard sync", Kind: Bool, Default: "Y", Group: "Permissions",
		Description: "Copy and paste text between viewer and device."},
	{Key: "enable-file-transfer", Label: "File transfer", Kind: Bool, Default: "Y", Group: "Permissions",
		Description: "File copy/paste in sessions and the file transfer window."},
	{Key: "enable-audio", Label: "Audio", Kind: Bool, Default: "Y", Group: "Permissions",
		Description: "Send the device's sound to the viewer."},
	{Key: "enable-remote-restart", Label: "Remote restart", Kind: Bool, Default: "Y", Group: "Permissions",
		Description: "Allow restarting the device from a session."},
	{Key: "enable-terminal", Label: "Terminal", Kind: Bool, Default: "Y", Group: "Permissions",
		Description: "Remote command line from the viewer."},
	{Key: "enable-tunnel", Label: "TCP tunnelling", Kind: Bool, Default: "Y", Group: "Permissions",
		Description: "Port forwarding through the session (e.g. RDP over RD)."},
	{Key: "enable-block-input", Label: "Block local input", Kind: Bool, Default: "Y", Group: "Permissions",
		Description: "Let the viewer lock the device's own keyboard and mouse."},
	{Key: "enable-privacy-mode", Label: "Privacy mode", Kind: Bool, Default: "Y", Group: "Permissions",
		Description: "Let the viewer black out the device's monitor."},
	{Key: "enable-camera", Label: "Camera", Kind: Bool, Default: "Y", Group: "Permissions",
		Description: "View the device's camera."},
	{Key: "enable-remote-printer", Label: "Remote printer", Kind: Bool, Default: "Y", Group: "Permissions",
		Description: "Print from the device to the viewer's printer."},
	{Key: "allow-auto-disconnect", Label: "Disconnect idle sessions", Kind: Bool, Default: "N", Group: "Sessions",
		Description: "End a session after the idle timeout."},
	{Key: "auto-disconnect-timeout", Label: "Idle timeout (minutes)", Kind: Number, Default: "10", Group: "Sessions",
		Description: "Used when idle sessions are disconnected."},
	{Key: "whitelist", Label: "IP allow-list", Kind: Text, Default: "", Group: "Security",
		Description: "Comma-separated IPs or CIDR ranges allowed to connect. Empty = anyone with access. Relayed sessions show the relay's view of the viewer IP."},
	{Key: "enable-abr", Label: "Adaptive bitrate", Kind: Bool, Default: "Y", Group: "Performance",
		Description: "Lower quality automatically on slow links."},
	{Key: "enable-hwcodec", Label: "Hardware encoding", Kind: Bool, Default: "Y", Group: "Performance",
		Description: "Use the GPU to encode video when available."},
	{Key: "enable-directx-capture", Label: "DirectX capture", Kind: Bool, Default: "Y", Group: "Performance",
		Description: "Faster screen capture on Windows."},
	{Key: "allow-remove-wallpaper", Label: "Hide wallpaper during sessions", Kind: Bool, Default: "N", Group: "Performance",
		Description: "Saves bandwidth."},
}

var byKey = func() map[string]Setting {
	m := map[string]Setting{}
	for _, s := range Catalogue {
		m[s.Key] = s
	}
	return m
}()

func Lookup(key string) (Setting, bool) {
	s, ok := byKey[key]
	return s, ok
}

// Validate checks and normalises values; unknown keys are rejected.
func Validate(values map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(values))
	for k, v := range values {
		s, ok := byKey[k]
		if !ok {
			return nil, fmt.Errorf("unknown setting %q", k)
		}
		v = strings.TrimSpace(v)
		switch s.Kind {
		case Bool:
			switch strings.ToUpper(v) {
			case "Y", "YES", "TRUE", "1", "ON":
				v = "Y"
			case "N", "NO", "FALSE", "0", "OFF":
				v = "N"
			default:
				return nil, fmt.Errorf("%s: expected Y or N", k)
			}
		case Number:
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 || n > 100000 {
				return nil, fmt.Errorf("%s: expected a positive number", k)
			}
			v = strconv.Itoa(n)
		case Text:
			if len(v) > 4000 {
				return nil, fmt.Errorf("%s: too long", k)
			}
			if k == "whitelist" {
				v = normaliseList(v)
			}
		}
		out[k] = v
	}
	return out, nil
}

func normaliseList(v string) string {
	var parts []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' || r == ';' }) {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ",")
}

// Layers are applied in order; later layers win.
type Layer struct {
	Name   string // "Account" or a group path
	Values map[string]string
}

type Resolved struct {
	Value  string `json:"value"`
	Source string `json:"source"` // layer name, or "" = client default
}

// Resolve returns the effective value and its source for every catalogue key.
func Resolve(layers []Layer) map[string]Resolved {
	out := make(map[string]Resolved, len(Catalogue))
	for _, s := range Catalogue {
		out[s.Key] = Resolved{}
	}
	for _, l := range layers {
		for k, v := range l.Values {
			if _, ok := byKey[k]; ok {
				out[k] = Resolved{Value: v, Source: l.Name}
			}
		}
	}
	return out
}

// ConfigOptions is what goes to the client: every managed key, "" where
// unset so the client falls back to its default (this also clears values
// that were removed from the policy).
func ConfigOptions(r map[string]Resolved) map[string]string {
	out := make(map[string]string, len(r))
	for k, v := range r {
		out[k] = v.Value
	}
	return out
}

// Version is a stable positive number for a set of options.
func Version(opts map[string]string) int64 {
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := fnv.New64a()
	for _, k := range keys {
		b, _ := json.Marshal([2]string{k, opts[k]})
		h.Write(b)
	}
	v := int64(h.Sum64() & 0x7fffffffffff) // fits in a JSON number exactly
	if v == 0 {
		v = 1
	}
	return v
}
