package config

import (
	"bytes"
	"testing"

	"gopkg.in/yaml.v3"
)

func FuzzConfigParser(f *testing.F) {
	for _, seed := range []string{
		"version: 1\nprofiles: {}\n",
		"version: 1\nprofiles:\n  dev:\n    secrets:\n      - name: team/token\n        env: TOKEN\n        required: true\n",
		"version: 1\nprofiles: {}\n---\n",
		"version: [",
		"version: 1\nprofiles:\n  dev:\n    secrets: []\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 64<<10 {
			t.Skip()
		}
		cfg, err := parse([]byte(input))
		if err != nil {
			return
		}
		if err := Validate(cfg); err != nil {
			t.Fatal("parser accepted an invalid config")
		}
		encoded, err := yaml.Marshal(cfg)
		if err != nil {
			t.Fatal("accepted config cannot be encoded")
		}
		roundTrip, err := parse(encoded)
		if err != nil {
			t.Fatal("encoded config cannot be parsed")
		}
		// omitempty normalizes empty slices to nil. Compare the canonical
		// encoding so that harmless normalization is not a false failure.
		encodedAgain, err := yaml.Marshal(roundTrip)
		if err != nil || !bytes.Equal(encoded, encodedAgain) {
			t.Fatal("accepted config changed after encoding and parsing")
		}
	})
}
