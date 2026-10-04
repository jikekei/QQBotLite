package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRoundTripAndInvalidPreservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := testConfig()
	if e := saveJSON(path, c); e != nil {
		t.Fatal(e)
	}
	if r, e := loadConfig(path); e != nil || len(r.Servers) != 2 {
		t.Fatal(e)
	}
	b, _ := json.Marshal(c)
	os.WriteFile(path, append([]byte{0xef, 0xbb, 0xbf}, b...), 0600)
	if _, e := loadConfig(path); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{`{"unknown":true}`, string(b) + `{}`, `{"adapter":"official"}`, `null`} {
		os.WriteFile(path, []byte(bad), 0600)
		if _, e := loadConfig(path); e == nil {
			t.Fatal("accepted", bad)
		}
		stored, _ := os.ReadFile(path)
		if string(stored) != bad {
			t.Fatal("invalid config overwritten")
		}
	}
	c.Servers[1].ID = c.Servers[0].ID
	if validate(c) == nil {
		t.Fatal("duplicate accepted")
	}
	c = testConfig()
	c.BridgeListen = "invalid"
	if validate(c) == nil {
		t.Fatal("listener accepted")
	}
}
