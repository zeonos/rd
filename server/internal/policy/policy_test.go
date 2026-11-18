package policy

import "testing"

func TestValidate(t *testing.T) {
	v, err := Validate(map[string]string{"enable-clipboard": "off", "auto-disconnect-timeout": " 15 ", "whitelist": "10.0.0.0/8; 192.168.1.5\n"})
	if err != nil {
		t.Fatal(err)
	}
	if v["enable-clipboard"] != "N" || v["auto-disconnect-timeout"] != "15" || v["whitelist"] != "10.0.0.0/8,192.168.1.5" {
		t.Fatalf("normalised: %v", v)
	}
	for _, bad := range []map[string]string{
		{"custom-rendezvous-server": "evil"},
		{"enable-clipboard": "maybe"},
		{"auto-disconnect-timeout": "-1"},
	} {
		if _, err := Validate(bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}

func TestResolveLayers(t *testing.T) {
	r := Resolve([]Layer{
		{Name: "Account", Values: map[string]string{"enable-clipboard": "N", "enable-audio": "N"}},
		{Name: "Office", Values: map[string]string{"enable-clipboard": "Y"}},
		{Name: "Office/Rack 1", Values: map[string]string{"enable-audio": "Y"}},
	})
	if r["enable-clipboard"] != (Resolved{"Y", "Office"}) || r["enable-audio"] != (Resolved{"Y", "Office/Rack 1"}) {
		t.Fatalf("resolve: %v", r)
	}
	if r["enable-keyboard"] != (Resolved{}) {
		t.Fatal("unset key should be empty")
	}
	opts := ConfigOptions(r)
	if len(opts) != len(Catalogue) || opts["enable-keyboard"] != "" {
		t.Fatal("config options must list every key")
	}
	if Version(opts) != Version(ConfigOptions(r)) {
		t.Fatal("version not stable")
	}
	opts["enable-keyboard"] = "N"
	if Version(opts) == Version(ConfigOptions(r)) {
		t.Fatal("version should change with values")
	}
}
